package handlers

import (
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/billing"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"github.com/stripe/stripe-go/v87"
	"gorm.io/gorm"
)

// Front woła sync raz, po powrocie z Checkout. Więcej to już ktoś, kto chce
// naszym kosztem odpytywać Stripe.
const (
	billingSyncLimit  = 10
	billingSyncWindow = time.Minute
)

// webhookSyncEvents to zdarzenia, po których stan subskrypcji mógł się
// zmienić. Treści zdarzenia nie bierzemy za stan: wyciągamy z niego tylko
// klienta i pobieramy jego stan ze Stripe, więc kolejność i powtórzenia
// zdarzeń nie mają znaczenia.
var webhookSyncEvents = map[stripe.EventType]bool{
	stripe.EventTypeCheckoutSessionCompleted:    true,
	stripe.EventTypeCustomerSubscriptionCreated: true,
	stripe.EventTypeCustomerSubscriptionUpdated: true,
	stripe.EventTypeCustomerSubscriptionDeleted: true,
	stripe.EventTypeCustomerSubscriptionPaused:  true,
	stripe.EventTypeCustomerSubscriptionResumed: true,
	stripe.EventTypeInvoicePaid:                 true,
	stripe.EventTypeInvoicePaymentFailed:        true,
	stripe.EventTypeCustomerDeleted:             true,
}

type BillingHandler struct {
	db            *gorm.DB
	billing       *billing.Service
	webhookSecret string
	limiter       *auth.RateLimiter
}

func NewBillingHandler(db *gorm.DB, service *billing.Service, webhookSecret string) *BillingHandler {
	return &BillingHandler{
		db:            db,
		billing:       service,
		webhookSecret: webhookSecret,
		limiter:       auth.NewRateLimiter(),
	}
}

type BillingStatusResponse struct {
	// Enabled mówi frontowi, czy w ogóle pokazywać zakup Pro.
	Enabled          bool       `json:"enabled"`
	Plan             string     `json:"plan"`
	Status           string     `json:"status"`
	CurrentPeriodEnd *time.Time `json:"current_period_end"`
	CancelAt         *time.Time `json:"cancel_at"`
	// Manageable mówi, czy jest po co otwierać panel klienta Stripe.
	Manageable bool `json:"manageable"`
}

func billingStatus(enabled bool, sub *models.Subscription) BillingStatusResponse {
	status := BillingStatusResponse{
		Enabled: enabled,
		Plan:    sub.Plan(),
		Status:  models.SubscriptionStatusNone,
	}
	if sub != nil {
		status.Status = sub.Status
		status.CurrentPeriodEnd = sub.CurrentPeriodEnd
		status.CancelAt = sub.CancelAt
		status.Manageable = enabled && sub.Status != models.SubscriptionStatusNone
	}
	return status
}

func (h *BillingHandler) HandleStatus(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	sub, err := h.billing.Subscription(userID)
	if err != nil {
		log.Printf("Billing: nie udało się odczytać subskrypcji %s: %v", userID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	c.JSON(http.StatusOK, billingStatus(h.billing.Enabled(), sub))
}

func (h *BillingHandler) HandleCheckout(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	var user models.User
	if err := h.db.Where("id = ?", userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpx.Fail(c, httpx.CodeSessionExpired)
			return
		}
		log.Printf("Billing: nie udało się pobrać użytkownika %s: %v", userID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	url, err := h.billing.Checkout(c.Request.Context(), &user)
	if err != nil {
		failBilling(c, "Checkout", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url})
}

func (h *BillingHandler) HandlePortal(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	url, err := h.billing.Portal(c.Request.Context(), userID)
	if err != nil {
		failBilling(c, "Portal", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url})
}

// HandleSync dociąga stan ze Stripe po powrocie z Checkout, żeby użytkownik
// zobaczył Pro od razu, a nie po najbliższym webhooku albo uzgadnianiu.
func (h *BillingHandler) HandleSync(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	if allowed, retryAfter := h.limiter.Allow("billing-sync:"+userID.String(), billingSyncLimit, billingSyncWindow); !allowed {
		httpx.FailRetryAfter(c, httpx.CodeRateLimited, retryAfter)
		return
	}

	sub, err := h.billing.Subscription(userID)
	if err != nil {
		failBilling(c, "Sync", err)
		return
	}
	if sub != nil && h.billing.Enabled() {
		if sub, err = h.billing.Sync(c.Request.Context(), sub.StripeCustomerID); err != nil {
			failBilling(c, "Sync", err)
			return
		}
	}
	c.JSON(http.StatusOK, billingStatus(h.billing.Enabled(), sub))
}

// HandleWebhook przyjmuje zdarzenia Stripe. Błąd synchronizacji kończy się
// 5xx, żeby Stripe ponowił dostarczenie.
func (h *BillingHandler) HandleWebhook(c *gin.Context) {
	if !h.billing.Enabled() || h.webhookSecret == "" {
		httpx.Fail(c, httpx.CodeBillingDisabled)
		return
	}

	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		httpx.Fail(c, httpx.CodeValidationFailed)
		return
	}

	// Wersja API zdarzenia nie musi zgadzać się z wersją SDK: z treści
	// czytamy tylko typ i ID klienta.
	event, err := stripe.ConstructEvent(payload, c.GetHeader("Stripe-Signature"), h.webhookSecret,
		stripe.WithIgnoreAPIVersionMismatch())
	if err != nil {
		httpx.Fail(c, httpx.CodeWebhookSignatureFailed)
		return
	}

	customerID := eventCustomer(event)
	if !webhookSyncEvents[event.Type] || customerID == "" {
		c.Status(http.StatusOK)
		return
	}

	if _, err := h.billing.Sync(c.Request.Context(), customerID); err != nil && !errors.Is(err, billing.ErrNoCustomer) {
		log.Printf("Billing: webhook %s (%s) nie zsynchronizował klienta %s: %v", event.ID, event.Type, customerID, err)
		httpx.Fail(c, httpx.CodeBillingUnavailable)
		return
	}
	c.Status(http.StatusOK)
}

// eventCustomer wyciąga ID klienta z obiektu zdarzenia. W subskrypcji, sesji
// Checkout i fakturze to pole customer; w customer.deleted obiektem jest sam
// klient.
func eventCustomer(event stripe.Event) string {
	if event.Data == nil {
		return ""
	}
	if event.Type == stripe.EventTypeCustomerDeleted {
		id, _ := event.Data.Object["id"].(string)
		return id
	}
	customer, _ := event.Data.Object["customer"].(string)
	return customer
}

func failBilling(c *gin.Context, operation string, err error) {
	switch {
	case errors.Is(err, billing.ErrDisabled):
		httpx.Fail(c, httpx.CodeBillingDisabled)
	case errors.Is(err, billing.ErrAlreadySubscribed):
		httpx.Fail(c, httpx.CodeAlreadySubscribed)
	case errors.Is(err, billing.ErrNoCustomer):
		httpx.Fail(c, httpx.CodeNoBillingAccount)
	default:
		log.Printf("Billing %s: %v", operation, err)
		httpx.Fail(c, httpx.CodeBillingUnavailable)
	}
}
