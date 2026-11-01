package models

import (
	"time"

	"github.com/google/uuid"
)

type PremiumScan struct {
	ID          uuid.UUID    `gorm:"type:uuid;primary_key;" json:"id"`
	UserID      uuid.UUID    `gorm:"type:uuid;index;index:idx_premium_scans_user_status_created,priority:1" json:"user_id"`
	User        User         `gorm:"foreignKey:UserID" json:"user,omitempty"`
	TargetURL   string       `json:"target_url"`
	Status      string       `gorm:"index:idx_premium_scans_user_status_created,priority:2" json:"status"`
	CreatedAt   time.Time    `gorm:"index:idx_premium_scans_user_status_created,priority:3" json:"created_at"`
	StartedAt   *time.Time   `json:"started_at"`
	CompletedAt *time.Time   `json:"completed_at"`
	Results     []ScanResult `gorm:"foreignKey:ScanID;constraint:-" json:"results"`
}
