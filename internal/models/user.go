package models

import (
	"time"

	"github.com/google/uuid"
)

const (
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
}
