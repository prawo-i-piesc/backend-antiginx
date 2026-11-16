package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// Częstotliwości harmonogramu. Najczęściej raz na dobę: skan co godzinę to
// już obciążenie celu, nie monitoring.
const (
	ScheduleDaily   = "daily"
	ScheduleWeekly  = "weekly"
	ScheduleMonthly = "monthly"
)

// Powody, dla których termin harmonogramu minął bez skanu (LastError).
// Kody są takie jak w odpowiedziach API, żeby front miał dla nich teksty.
const (
	ScheduleErrorProRequired      = "PRO_REQUIRED"
	ScheduleErrorQueueUnavailable = "QUEUE_UNAVAILABLE"
	ScheduleErrorInvalidTarget    = "INVALID_TARGET"
)

// Schedule to cykliczny skan konta. Termin to godzina w strefie czasowej
// użytkownika, więc po zmianie czasu skan dalej rusza o tej samej godzinie.
type Schedule struct {
	ID     uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	UserID uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	// Klucz obcy z kaskadą: harmonogramy znikają razem z kontem.
	User      User   `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	TargetURL string `gorm:"not null" json:"target_url"`
	// Tests to testy silnika, jak w PremiumScan.Tests.
	Tests datatypes.JSON `json:"tests"`

	Frequency string `gorm:"not null" json:"frequency"`
	// Time to godzina "HH:MM" w strefie Timezone.
	Time string `gorm:"not null" json:"time"`
	// Weekday to dzień tygodnia przy weekly, 0 to niedziela (jak time.Weekday
	// i Date.getDay w JS).
	Weekday *int `json:"weekday"`
	// MonthDay to dzień miesiąca przy monthly, 1–28, żeby był w każdym miesiącu.
	MonthDay *int   `json:"month_day"`
	Timezone string `gorm:"not null" json:"timezone"`

	// Enabled to pauza użytkownika.
	Enabled bool `gorm:"not null" json:"enabled"`
	// BlockedAt ustawia administrator. Zablokowanego harmonogramu użytkownik
	// nie wznowi, dopóki administrator nie zdejmie blokady.
	BlockedAt   *time.Time `json:"blocked_at"`
	BlockedBy   *uuid.UUID `gorm:"type:uuid" json:"-"`
	BlockReason string     `json:"block_reason"`

	// NextRunAt to najbliższy termin. Scheduler bierze harmonogramy, którym on
	// minął, i przesuwa go dalej, zanim zleci skan.
	NextRunAt  *time.Time `gorm:"index" json:"next_run_at"`
	LastRunAt  *time.Time `json:"last_run_at"`
	LastScanID *uuid.UUID `gorm:"type:uuid" json:"last_scan_id"`
	// LastError mówi, dlaczego ostatni termin minął bez skanu; pusty, gdy
	// skan ruszył.
	LastError string `json:"last_error"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Active mówi, czy scheduler ma uruchamiać ten harmonogram.
func (s *Schedule) Active() bool {
	return s.Enabled && s.BlockedAt == nil
}
