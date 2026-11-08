package models

import (
	"time"

	"github.com/google/uuid"
)

// PlanGrant to plan Pro nadany ręcznie przez administratora, obok Stripe.
// Synchronizacja ze Stripe zapisuje tylko tabelę subscriptions, więc nadania
// nie nadpisze. Konto ma najwyżej jedno: kolejne zastępuje poprzednie.
type PlanGrant struct {
	UserID uuid.UUID `gorm:"type:uuid;primaryKey"`
	// Klucz obcy z kaskadą: nadanie znika razem z kontem.
	User User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	// GrantedBy to administrator, który nadał plan. Bez klucza obcego, żeby
	// ślad zostawał także po usunięciu jego konta.
	GrantedBy uuid.UUID `gorm:"type:uuid;not null"`
	GrantedAt time.Time `gorm:"not null"`
	// ExpiresAt to koniec nadania; nil znaczy bezterminowo.
	ExpiresAt *time.Time
}

// ActiveAt mówi, czy nadanie obowiązuje w danej chwili.
func (g *PlanGrant) ActiveAt(now time.Time) bool {
	return g != nil && (g.ExpiresAt == nil || g.ExpiresAt.After(now))
}
