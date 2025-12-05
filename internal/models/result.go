package models

import (
	"github.com/google/uuid"
)

// ScanResult represents a single security test result within a scan.
//
// Each ScanResult corresponds to one specific security check performed
// during a scan. Multiple ScanResults are associated with a single Scan
// via the ScanID foreign key.
//
// The result includes information about whether the test passed,
// the severity of the issue if it failed, and remediation guidance.
type ScanResult struct {
	// ID is the auto-incrementing primary key
	ID uint `gorm:"primaryKey" json:"id"`
	// ScanID references the parent Scan this result belongs to
	ScanID uuid.UUID `gorm:"type:uuid;index" json:"scan_id"`
	// TestID is a unique identifier for the security test (e.g., "SEC-001")
	TestID string `json:"test_id"`
	// TestName is the human-readable name of the test (e.g., "X-Frame-Options Header")
	TestName string `json:"test_name"`
	// Category groups related tests (e.g., "headers", "ssl", "cookies", "content")
	Category string `json:"category"`
	// Severity indicates the importance level: "critical", "high", "medium", "low", "info"
	Severity string `json:"severity"`
	// Passed indicates whether the security check passed (true) or failed (false)
	Passed bool `json:"passed"`
	// Message provides detailed information about the test result
	Message string `gorm:"type:text" json:"message"`
	// Reference is a URL to external documentation about this security issue
	Reference string `gorm:"type:text" json:"reference"`
	// Remediation provides guidance on how to fix the security issue
	Remediation string `gorm:"type:text" json:"remediation"`
}
