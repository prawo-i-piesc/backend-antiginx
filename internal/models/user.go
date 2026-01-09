package models

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;" json:"id"`
	FullName  string    `json:"full_name"`
	Email     string    `gorm:"uniqueIndex; not null" json:"email"`
	CreatedAt time.Time `json:"created_at"`
	Password  []byte    `json:"-"`
}
