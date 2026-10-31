package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// UserPreference to jedno nazwane ustawienie konta zapisane jako JSON, np.
// układ widgetów pulpitu. Klucze tworzy tylko API ze stałej listy, więc
// tabela nie rośnie o dowolne wpisy.
type UserPreference struct {
	UserID    uuid.UUID      `gorm:"type:uuid;primaryKey"`
	Key       string         `gorm:"type:varchar(64);primaryKey"`
	Value     datatypes.JSON `gorm:"not null"`
	UpdatedAt time.Time
}
