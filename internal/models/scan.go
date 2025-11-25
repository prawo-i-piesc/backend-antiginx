package models

import (
	"time"

	"github.com/google/uuid"
)

type Scan struct {
	ID          uuid.UUID    `gorm:"type:uuid;primary_key;" json:"id"`
	TargetURL   string       `json:"target_url"`
	Status      string       `json:"status"`
	CreatedAt   time.Time    `json:"created_at"`
	StartedAt   *time.Time   `json:"started_at"`
	CompletedAt *time.Time   `json:"completed_at"`
	Results     []ScanResult `gorm:"foreignKey:ScanID" json:"results"`
}
