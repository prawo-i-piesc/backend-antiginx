package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	AMRPassword    = "pwd"
	AMRPasswordOTP = "pwd+otp"
	AMRWebAuthn    = "webauthn"
	AMROAuth       = "oauth"
)

type Session struct {
	ID         uuid.UUID `gorm:"type:uuid;primary_key;"`
	UserID     uuid.UUID `gorm:"type:uuid;index;not null"`
	FamilyID   uuid.UUID `gorm:"type:uuid;index;not null"`
	TokenHash  []byte    `gorm:"uniqueIndex;not null"`
	UserAgent  string    `gorm:"type:varchar(512)"`
	IP         string    `gorm:"type:varchar(64)"`
	AMR        string    `gorm:"type:varchar(64)"`
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time `gorm:"index;not null"`
	RevokedAt  *time.Time
}

func (s *Session) Active(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}
