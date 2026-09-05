package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	// Passkey albo domyka logowanie hasłem, albo je zastępuje. Domyślnie
	// domyka, bo dodanie klucza ma konto wzmacniać, a nie otwierać nową,
	// samodzielną drogę wejścia bez wiedzy właściciela.
	PasskeyModeSecondFactor = "second_factor"
	PasskeyModePasswordless = "passwordless"

	UserRoleUser  = "user"
	UserRoleAdmin = "admin"
)

type User struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;" json:"id"`
	FullName  string    `json:"full_name"`
	Email     string    `gorm:"uniqueIndex; not null" json:"email"`
	Role      string    `gorm:"type:varchar(32);not null;default:user;index" json:"role"`
	CreatedAt time.Time `json:"created_at"`
	Password  []byte    `json:"-"`

	EmailVerified  bool       `gorm:"not null;default:false" json:"email_verified"`
	EmailChangedAt *time.Time `json:"-"`
	TOTPSecret     []byte     `json:"-"`
	TOTPEnabledAt  *time.Time `json:"-"`
	PasskeyMode    string     `gorm:"type:varchar(32);not null;default:second_factor" json:"-"`
}

func (u *User) HasPassword() bool {
	return len(u.Password) > 0
}

// PasskeysReplacePassword mówi, czy sam passkey wystarczy do zalogowania.
func (u *User) PasskeysReplacePassword() bool {
	return u.PasskeyMode == PasskeyModePasswordless
}

func (u *User) TOTPEnabled() bool {
	return u.TOTPEnabledAt != nil
}
