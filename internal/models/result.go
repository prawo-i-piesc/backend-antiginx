package models

import (
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

type ScanResult struct {
	ID       uint      `gorm:"primaryKey" json:"id"`
	ScanID   uuid.UUID `gorm:"type:uuid;index" json:"scan_id"`
	TestName string    `json:"test_name"`
	Severity string    `json:"severity"`
	Passed   bool      `json:"passed"`
	Message  string    `gorm:"type:text" json:"message"`

	Metadata datatypes.JSON `json:"metadata"`
}
