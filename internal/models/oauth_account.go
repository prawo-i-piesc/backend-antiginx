package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	ProviderGoogle = "google"
	ProviderGitHub = "github"
)

type OAuthAccount struct {
	ID             uuid.UUID `gorm:"type:uuid;primary_key;"`
	UserID         uuid.UUID `gorm:"type:uuid;index;not null"`
	Provider       string    `gorm:"type:varchar(32);not null;uniqueIndex:idx_provider_subject"`
	ProviderUserID string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_provider_subject"`
	Email          string    `gorm:"type:varchar(320)"`
	CreatedAt      time.Time
}
