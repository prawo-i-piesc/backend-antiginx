// Package models defines the data structures and database models
// for the backend-antiginx service.
//
// This package contains GORM models that represent database tables
// and are used for ORM operations throughout the application.
package models

import (
	"time"

	"github.com/google/uuid"
)

// Scan represents a security scan request and its current state.
//
// Each scan targets a specific URL and progresses through various
// statuses (PENDING, RUNNING, COMPLETED, FAILED) as it is processed
// by worker services.
//
// The Results field contains all individual test results associated
// with this scan, loaded via GORM's foreign key relationship.
type Scan struct {
	// ID is the unique identifier for the scan (UUIDv7)
	ID uuid.UUID `gorm:"type:uuid;primary_key;" json:"id"`
	// TargetURL is the URL that was scanned for security issues
	TargetURL string `json:"target_url"`
	// Status indicates the current state of the scan (PENDING, RUNNING, COMPLETED, FAILED)
	Status string `json:"status"`
	// CreatedAt is the timestamp when the scan was submitted
	CreatedAt time.Time `json:"created_at"`
	// StartedAt is the timestamp when a worker began processing the scan (nil if not started)
	StartedAt *time.Time `json:"started_at"`
	// CompletedAt is the timestamp when the scan finished (nil if not completed)
	CompletedAt *time.Time `json:"completed_at"`
	// Results contains all individual test results for this scan
	Results []ScanResult `gorm:"foreignKey:ScanID" json:"results"`
}
