package models

import (
	"github.com/google/uuid"
)

type ScanResult struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ScanID      uuid.UUID `gorm:"type:uuid;index" json:"scan_id"`
	TestID      string    `json:"test_id"`
	TestName    string    `json:"test_name"`
	Category    string    `json:"category"`
	Severity    string    `json:"severity"`
	Passed      bool      `json:"passed"`
	Message     string    `gorm:"type:text" json:"message"`
	Reference   string    `gorm:"type:text" json:"reference"`
	Remediation string    `gorm:"type:text" json:"remediation"`
}
