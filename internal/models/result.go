package models

import (
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

type ScanResult struct {
	ID       uint      `gorm:"primaryKey" json:"id"`
	ScanID   uuid.UUID `gorm:"type:uuid;index:idx_scan_results_scan_test,priority:1" json:"scan_id"`
	TestName string    `gorm:"index:idx_scan_results_scan_test,priority:2" json:"test_name"`
	Severity string    `json:"severity"`
	Passed   bool      `json:"passed"`
	Message  string    `gorm:"type:text" json:"message"`
	// Notice: komunikat silnika (test przerwany, zablokowany przez anty-bota),
	// a nie wynik testu. Statystyki go pomijają, żeby skan, który niczego nie
	// sprawdził, nie liczył się jako bezpieczny.
	Notice bool `gorm:"not null;default:false" json:"notice"`

	Metadata datatypes.JSON `json:"metadata"`
}
