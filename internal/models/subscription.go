package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	PlanFree = "free"
	PlanPro  = "pro"
)

// SubscriptionStatusNone to klient Stripe bez żadnej subskrypcji: założony
// przy pierwszym Checkout, który nie został (jeszcze) opłacony. Pozostałe
// wartości Status to statusy subskrypcji Stripe, przepisane bez zmian.
const SubscriptionStatusNone = "none"

// Subscription odbija stan subskrypcji użytkownika w Stripe. Źródłem prawdy
// jest Stripe: każda synchronizacja nadpisuje wiersz w całości, więc niczego
// tu nie zmieniamy ręcznie.
type Subscription struct {
	UserID               uuid.UUID `gorm:"type:uuid;primaryKey"`
	StripeCustomerID     string    `gorm:"type:varchar(255);uniqueIndex;not null"`
	StripeSubscriptionID string    `gorm:"type:varchar(255)"`
	Status               string    `gorm:"type:varchar(32);not null;default:none;index"`
	PriceID              string    `gorm:"type:varchar(255)"`
	CurrentPeriodEnd     *time.Time
	// CancelAt to chwila, w której subskrypcja wygaśnie, bo właściciel ją
	// anulował; nil, dopóki się odnawia.
	CancelAt *time.Time
	// CheckoutStartedAt pozwala dociągnąć płatność, po której nikt nie wrócił
	// na stronę sukcesu, kiedy webhooki do nas nie docierają.
	CheckoutStartedAt *time.Time
	UpdatedAt         time.Time
}

func (s *Subscription) Plan() string {
	if s == nil {
		return PlanFree
	}
	return PlanForStatus(s.Status)
}

// PlanForStatus przekłada status subskrypcji Stripe na plan. past_due zostawia
// Pro: Stripe ponawia wtedy płatność, a odebranie dostępu przy pierwszej
// odrzuconej karcie karałoby za wygasłą kartę, nie za brak zapłaty. Po
// ostatniej próbie Stripe sam zmienia status na canceled albo unpaid.
func PlanForStatus(status string) string {
	switch status {
	case "active", "trialing", "past_due":
		return PlanPro
	}
	return PlanFree
}
