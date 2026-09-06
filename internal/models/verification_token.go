package models

import (
	"time"

	"github.com/google/uuid"
)

type EmailVerificationToken struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	UserID    uuid.UUID `gorm:"type:uuid;index;not null"`
	Email     string    `gorm:"type:varchar(320);not null"`
	TokenHash []byte    `gorm:"uniqueIndex;not null"`
	CreatedAt time.Time
	ExpiresAt time.Time `gorm:"index;not null"`
	UsedAt    *time.Time
}

type PasswordResetToken struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	UserID    uuid.UUID `gorm:"type:uuid;index;not null"`
	TokenHash []byte    `gorm:"uniqueIndex;not null"`
	CreatedAt time.Time
	ExpiresAt time.Time `gorm:"index;not null"`
	UsedAt    *time.Time
}
