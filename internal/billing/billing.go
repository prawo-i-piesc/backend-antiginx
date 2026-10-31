// Package billing sprzedaje plan Pro przez Stripe i trzyma w bazie odbicie
// stanu subskrypcji.
//
// Stan dociera do nas trzema drogami, bo żadna nie wystarcza sama: powrót z
// Checkout (front woła Sync), webhook, o ile Stripe może do nas dotrzeć (za
// samym WireGuardem nie może), i cykliczne uzgadnianie w Run, które łapie
// odnowienia, anulowania i nieudane płatności także bez webhooków.
package billing

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrDisabled          = errors.New("płatności są wyłączone")
	ErrAlreadySubscribed = errors.New("użytkownik ma już aktywną subskrypcję")
	ErrNoCustomer        = errors.New("użytkownik nie jest klientem Stripe")
)

// Ścieżki frontu, na które Stripe odsyła użytkownika. Po powrocie z panelu
// front też synchronizuje stan, bo anulowanie w panelu inaczej dotarłoby do
// nas dopiero z webhookiem albo uzgadnianiem.
const (
	profilePath        = "/dashboard/profile"
	checkoutSuccessURL = profilePath + "?billing=success"
	checkoutCancelURL  = profilePath + "?billing=cancel"
	portalReturnURL    = profilePath + "?billing=portal"
)

// checkoutWindow to najdłuższy czas życia sesji Checkout w Stripe z zapasem.
// Tak długo uzgadnianie sprawdza konto, które zaczęło płatność, bo bez
// webhooków nie dowiemy się inaczej o opłaconej, po której nikt nie wrócił.
const checkoutWindow = 25 * time.Hour

// settledStatuses nie zmienią się bez nowego Checkout, więc uzgadnianie ich
// nie odpytuje. unpaid i paused się zmienią, gdy klient zapłaci w panelu.
var settledStatuses = []string{models.SubscriptionStatusNone, "canceled", "incomplete_expired"}

type Service struct {
	db      *gorm.DB
	gateway Gateway
	priceID string
	baseURL string
}

// New przyjmuje nil jako gateway, gdy płatności nie są skonfigurowane: plan da
// się wtedy odczytać, ale nic nie da się kupić.
func New(db *gorm.DB, gateway Gateway, priceID, baseURL string) *Service {
	return &Service{db: db, gateway: gateway, priceID: priceID, baseURL: baseURL}
}

func (s *Service) Enabled() bool {
	return s.gateway != nil
}

// UserPlan to plan użytkownika według ostatniej synchronizacji. Nie pyta
// Stripe, więc nadaje się do sprawdzania uprawnień przy każdym żądaniu.
func UserPlan(db *gorm.DB, userID uuid.UUID) (string, error) {
	var sub models.Subscription
	err := db.Where("user_id = ?", userID).Take(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.PlanFree, nil
	}
	if err != nil {
		return models.PlanFree, err
	}
	return sub.Plan(), nil
}

// Subscription zwraca nil, gdy użytkownik nigdy nie zaczął płatności.
func (s *Service) Subscription(userID uuid.UUID) (*models.Subscription, error) {
	var sub models.Subscription
	err := s.db.Where("user_id = ?", userID).Take(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

// Checkout zwraca adres strony płatności za Pro.
func (s *Service) Checkout(ctx context.Context, user *models.User) (string, error) {
	if !s.Enabled() {
		return "", ErrDisabled
	}

	sub, err := s.Subscription(user.ID)
	if err != nil {
		return "", err
	}
	if sub != nil {
		// Wiersz może być sprzed godziny, a druga subskrypcja to podwójna
		// opłata, więc o tym, czy już płaci, decyduje Stripe.
		if sub, err = s.Sync(ctx, sub.StripeCustomerID); err != nil {
			return "", err
		}
	}
	if sub.Plan() != models.PlanFree {
		return "", ErrAlreadySubscribed
	}

	var customerID string
	if sub != nil {
		customerID = sub.StripeCustomerID
	} else if customerID, err = s.createCustomer(ctx, user); err != nil {
		return "", err
	}

	url, err := s.gateway.CreateCheckoutSession(ctx, CheckoutParams{
		CustomerID: customerID,
		PriceID:    s.priceID,
		UserID:     user.ID,
		SuccessURL: s.baseURL + checkoutSuccessURL,
		CancelURL:  s.baseURL + checkoutCancelURL,
	})
	if err != nil {
		return "", err
	}

	if err := s.db.Model(&models.Subscription{}).
		Where("user_id = ?", user.ID).
		Update("checkout_started_at", time.Now()).Error; err != nil {
		// Płatność i tak dotrze przez powrót ze Stripe albo webhook.
		log.Printf("Billing: nie udało się zapisać startu płatności %s: %v", user.ID, err)
	}
	return url, nil
}

// createCustomer zakłada klienta Stripe i zapisuje go przy koncie. Gdy dwa
// żądania zrobią to naraz, zostaje klient zapisany pierwszy, a drugiego
// usuwamy, żeby nie wisiał w Stripe bez konta.
func (s *Service) createCustomer(ctx context.Context, user *models.User) (string, error) {
	customerID, err := s.gateway.CreateCustomer(ctx, user.ID, user.Email, user.FullName)
	if err != nil {
		return "", err
	}

	row := models.Subscription{
		UserID:           user.ID,
		StripeCustomerID: customerID,
		Status:           models.SubscriptionStatusNone,
	}
	if err := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return "", err
	}

	saved, err := s.Subscription(user.ID)
	if err != nil {
		return "", err
	}
	if saved == nil {
		return "", errors.New("klient Stripe zniknął zaraz po zapisaniu")
	}
	if saved.StripeCustomerID != customerID {
		if err := s.gateway.DeleteCustomer(ctx, customerID); err != nil {
			log.Printf("Billing: nie udało się usunąć zdublowanego klienta %s: %v", customerID, err)
		}
	}
	return saved.StripeCustomerID, nil
}

// Portal zwraca adres panelu klienta Stripe, w którym użytkownik anuluje
// subskrypcję, zmienia kartę i pobiera faktury.
func (s *Service) Portal(ctx context.Context, userID uuid.UUID) (string, error) {
	if !s.Enabled() {
		return "", ErrDisabled
	}

	sub, err := s.Subscription(userID)
	if err != nil {
		return "", err
	}
	if sub == nil {
		return "", ErrNoCustomer
	}

	url, err := s.gateway.CreatePortalSession(ctx, sub.StripeCustomerID, s.baseURL+portalReturnURL)
	if errors.Is(err, ErrCustomerGone) {
		if err := s.forget(sub); err != nil {
			return "", err
		}
		return "", ErrNoCustomer
	}
	return url, err
}

// Sync pobiera ze Stripe stan subskrypcji klienta i zapisuje go. Zwraca nil,
// gdy klienta usunięto w Stripe; wiersz znika wtedy razem z nim.
func (s *Service) Sync(ctx context.Context, customerID string) (*models.Subscription, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}

	var sub models.Subscription
	err := s.db.Where("stripe_customer_id = ?", customerID).Take(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNoCustomer
	}
	if err != nil {
		return nil, err
	}

	remote, err := s.gateway.LatestSubscription(ctx, customerID)
	if errors.Is(err, ErrCustomerGone) {
		return nil, s.forget(&sub)
	}
	if err != nil {
		return nil, err
	}

	apply(&sub, remote)
	// Updates zamiast Save: Save dopisałby wiersz, który w międzyczasie
	// zniknął razem z kontem.
	if err := s.db.Model(&sub).
		Select("stripe_subscription_id", "status", "price_id", "current_period_end", "cancel_at", "checkout_started_at", "updated_at").
		Updates(&sub).Error; err != nil {
		return nil, err
	}
	return &sub, nil
}

func apply(sub *models.Subscription, remote *RemoteSubscription) {
	if remote == nil {
		sub.StripeSubscriptionID = ""
		sub.Status = models.SubscriptionStatusNone
		sub.PriceID = ""
		sub.CurrentPeriodEnd = nil
		sub.CancelAt = nil
		return
	}

	sub.StripeSubscriptionID = remote.ID
	sub.Status = remote.Status
	sub.PriceID = remote.PriceID
	sub.CurrentPeriodEnd = remote.CurrentPeriodEnd
	sub.CancelAt = remote.CancelAt
	if sub.Plan() != models.PlanFree {
		// Płatność doszła, więc nie ma już na co czekać.
		sub.CheckoutStartedAt = nil
	}
}

func (s *Service) forget(sub *models.Subscription) error {
	return s.db.Where("user_id = ?", sub.UserID).Delete(&models.Subscription{}).Error
}

// CloseAccount usuwa klienta Stripe przed usunięciem konta, co od razu kończy
// jego subskrypcje. Bez tego Stripe dalej pobierałby opłaty za konto, którego
// już nie ma. Wiersz w bazie usuwa kaskada usuwania konta.
func (s *Service) CloseAccount(ctx context.Context, userID uuid.UUID) error {
	sub, err := s.Subscription(userID)
	if err != nil || sub == nil {
		return err
	}
	if !s.Enabled() {
		// Bez kluczy nie anulujemy płatnej subskrypcji, a usunięcie konta
		// zostawiłoby ją bez właściciela.
		if sub.Plan() != models.PlanFree {
			return ErrDisabled
		}
		return nil
	}
	return s.gateway.DeleteCustomer(ctx, sub.StripeCustomerID)
}

// Reconcile synchronizuje konta, których stan może się zmienić bez udziału
// użytkownika: z żywą subskrypcją (odnowienie, anulowanie, nieudana płatność)
// i z niedawno rozpoczętym Checkout.
func (s *Service) Reconcile(ctx context.Context) error {
	if !s.Enabled() {
		return ErrDisabled
	}

	var customerIDs []string
	if err := s.db.Model(&models.Subscription{}).
		Where("status NOT IN ? OR checkout_started_at > ?", settledStatuses, time.Now().Add(-checkoutWindow)).
		Pluck("stripe_customer_id", &customerIDs).Error; err != nil {
		return err
	}

	for _, customerID := range customerIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Usunięte w międzyczasie konto to nie błąd, tylko mniej do roboty.
		if _, err := s.Sync(ctx, customerID); err != nil && !errors.Is(err, ErrNoCustomer) {
			log.Printf("Billing: nie udało się zsynchronizować klienta %s: %v", customerID, err)
		}
	}
	return nil
}

// Run uzgadnia stan od razu po starcie, a potem co interval, aż do
// zamknięcia ctx. Przy kilku replikach każda robi to samo; Sync jest
// idempotentny, więc kosztuje to tylko dodatkowe zapytania do Stripe.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if !s.Enabled() {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
			log.Printf("Billing: uzgadnianie subskrypcji nie powiodło się: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
