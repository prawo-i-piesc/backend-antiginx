package models

import (
	"time"

	"github.com/google/uuid"
)

type WebAuthnCredential struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;"`
	UserID          uuid.UUID `gorm:"type:uuid;index;not null"`
	CredentialID    []byte    `gorm:"uniqueIndex;not null"`
	PublicKey       []byte    `gorm:"not null"`
	AttestationType string    `gorm:"type:varchar(32)"`
	Transports      string    `gorm:"type:varchar(128)"`
	AAGUID          []byte
	SignCount       uint32 `gorm:"not null;default:0"`
	BackupEligible  bool   `gorm:"not null;default:false"`
	BackupState     bool   `gorm:"not null;default:false"`
	Name            string `gorm:"type:varchar(128)"`
	CreatedAt       time.Time
	LastUsedAt      *time.Time
}
