package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// UserPreference to jedno nazwane ustawienie konta zapisane jako JSON, np.
// układ widgetów pulpitu. Klucze tworzy tylko API ze stałej listy, więc
// tabela nie rośnie o dowolne wpisy.
type UserPreference struct {
	UserID uuid.UUID `gorm:"type:uuid;primaryKey"`
	// Klucz obcy z kaskadą: zapis, który dotrze już po usunięciu konta (np.
	// odroczony zapis układu z otwartej karty), nie zostawi osieroconego wiersza.
	User      User           `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	Key       string         `gorm:"type:varchar(64);primaryKey"`
	Value     datatypes.JSON `gorm:"not null"`
	UpdatedAt time.Time
}

// DeleteOrphanPreferences usuwa preferencje kont, których już nie ma. Musi
// pójść przed AutoMigrate: inaczej klucz obcy nie powstanie na tabeli
// z osieroconymi wierszami.
func DeleteOrphanPreferences(db *gorm.DB) error {
	if !db.Migrator().HasTable(&UserPreference{}) {
		return nil
	}
	return db.Exec(`DELETE FROM user_preferences WHERE user_id NOT IN (SELECT id FROM users)`).Error
}
