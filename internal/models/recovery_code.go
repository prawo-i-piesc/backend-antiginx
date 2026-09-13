package models

import (
	"time"

	"github.com/google/uuid"
)

type RecoveryCode struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	UserID    uuid.UUID `gorm:"type:uuid;index;not null"`
	CodeHash  []byte    `gorm:"not null"`
	CreatedAt time.Time
	UsedAt    *time.Time
}
