// Package handlers provides HTTP request handlers for the backend-antiginx API.
//
// This package contains the business logic for processing security scan requests,
// storing results in the database, and communicating with RabbitMQ for
// asynchronous scan task distribution.
package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

// ScanHandler handles HTTP requests related to security scans.
//
// It manages the lifecycle of scan operations including submission,
// result processing, and retrieval. The handler communicates with
// RabbitMQ to queue scan tasks for worker processing.
type ScanHandler struct {
	amqpChannel *amqp.Channel
	db          *gorm.DB
}

// NewScanHandler creates a new ScanHandler instance.
//
// Parameters:
//   - ch: RabbitMQ channel for publishing scan tasks to the queue
//   - db: GORM database instance for persisting scan data
//
// Returns:
//   - *ScanHandler: New handler instance ready to process requests
func NewScanHandler(ch *amqp.Channel, db *gorm.DB) *ScanHandler {
	return &ScanHandler{
		amqpChannel: ch,
		db:          db,
	}
}

// CreateScanRequest represents the JSON payload for creating a new scan.
//
// The TargetURL must be a valid URL that will be scanned for security issues.
type CreateScanRequest struct {
	TargetURL string `json:"target_url" binding:"required,url"`
}

// ResultSubmissionRequest represents the JSON payload for submitting scan results.
//
// This is typically sent by worker services after completing a security scan.
type ResultSubmissionRequest struct {
	// ScanID is the UUID of the scan these results belong to
	ScanID string `json:"scan_id" binding:"required,uuid"`
	// Status indicates the final scan status (COMPLETED or FAILED)
	Status string `json:"status" binding:"required,oneof=COMPLETED FAILED"`
	// StartedAt is the timestamp when the scan began execution
	StartedAt time.Time `json:"started_at" binding:"required"`
	// CompletedAt is the timestamp when the scan finished
	CompletedAt time.Time `json:"completed_at" binding:"required"`
	// Results contains the individual test results from the scan
	Results []ScanResultItem `json:"results" binding:"required,dive"`
}

// ScanResultItem represents a single test result within a security scan.
//
// Each item corresponds to one security check performed during the scan.
type ScanResultItem struct {
	// TestID is a unique identifier for the security test
	TestID string `json:"test_id" binding:"required"`
	// TestName is the human-readable name of the test
	TestName string `json:"test_name" binding:"required"`
	// Category groups related tests (e.g., "headers", "ssl", "cookies")
	Category string `json:"category" binding:"required"`
	// Severity indicates the importance level (e.g., "high", "medium", "low")
	Severity string `json:"severity" binding:"required"`
	// Passed indicates whether the security check passed
	Passed bool `json:"passed"`
	// Message provides details about the test result
	Message string `json:"message"`
	// Reference is a URL to documentation about the security issue
	Reference string `json:"reference"`
	// Remediation provides guidance on how to fix the issue
	Remediation string `json:"remediation"`
}

// ScanTaskMessage represents the message published to RabbitMQ for worker processing.
//
// Workers consume these messages from the scan_queue to perform security scans.
type ScanTaskMessage struct {
	// ID is the UUID of the scan to be performed
	ID string `json:"id"`
	// TargetURL is the URL to scan for security issues
	TargetURL string `json:"target_url"`
}

// HandleScanSubmission processes POST /api/scans requests to create new security scans.
//
// The handler performs the following operations:
//  1. Validates the incoming JSON request
//  2. Generates a new UUIDv7 for the scan
//  3. Persists the scan record to the database with PENDING status
//  4. Publishes a task message to RabbitMQ for worker processing
//
// Request body:
//
//	{
//	  "target_url": "https://example.com"
//	}
//
// Response (202 Accepted):
//
//	{
//	  "scanId": "uuid-string",
//	  "status": "PENDING"
//	}
//
// Errors:
//   - 400 Bad Request: Invalid or missing target_url
//   - 500 Internal Server Error: Database or queue operation failed
func (h *ScanHandler) HandleScanSubmission(c *gin.Context) {
	var req CreateScanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	newScanID, err := uuid.NewV7()
	if err != nil {
		log.Printf("Failed to generate UUIDv7: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate scan ID"})
		return
	}

	newScan := models.Scan{
		ID:        newScanID,
		TargetURL: req.TargetURL,
		Status:    "PENDING",
		CreatedAt: time.Now(),
	}

	result := h.db.Create(&newScan)
	if result.Error != nil {
		log.Printf("Failed to create scan in DB: %v", result.Error)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create scan"})
		return
	}

	task := ScanTaskMessage{
		ID:        newScan.ID.String(),
		TargetURL: newScan.TargetURL,
	}

	jsonBytes, err := json.Marshal(task)
	if err != nil {
		log.Printf("Failed to marshal task: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal Server Error"})
		return
	}

	err = h.amqpChannel.PublishWithContext(c.Request.Context(),
		"",           // exchange
		"scan_queue", // routing key
		false,        // mandatory
		false,        // immediate
		amqp.Publishing{
			DeliveryMode: amqp.Persistent, // zachowaj na dysku
			ContentType:  "application/json",
			Body:         jsonBytes,
		})

	if err != nil {
		log.Printf("Failed to publish message: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to queue scan"})
		return
	}

	log.Printf(" [x] Sent task for ID: %s\n", newScan.ID)

	c.JSON(http.StatusAccepted, gin.H{
		"scanId": newScan.ID.String(),
		"status": newScan.Status,
	})
}

// HandleResultSubmission processes POST /api/results requests from scan workers.
//
// This handler receives scan results from worker services and persists them
// to the database. The operation is performed within a transaction to ensure
// data consistency between scan results and scan status updates.
//
// Request body:
//
//	{
//	  "scan_id": "uuid-string",
//	  "status": "COMPLETED",
//	  "started_at": "2024-01-01T00:00:00Z",
//	  "completed_at": "2024-01-01T00:01:00Z",
//	  "results": [...]
//	}
//
// Response (200 OK):
//
//	{
//	  "message": "Results received and scan updated"
//	}
//
// Errors:
//   - 400 Bad Request: Invalid request payload or scan ID format
//   - 500 Internal Server Error: Database transaction failed
func (h *ScanHandler) HandleResultSubmission(c *gin.Context) {
	var req ResultSubmissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	scanUUID, err := uuid.Parse(req.ScanID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Scan ID format"})
		return
	}

	newResults := make([]models.ScanResult, len(req.Results))
	for i, result := range req.Results {
		newResults[i] = models.ScanResult{
			ScanID:      scanUUID,
			TestID:      result.TestID,
			TestName:    result.TestName,
			Category:    result.Category,
			Severity:    result.Severity,
			Passed:      result.Passed,
			Message:     result.Message,
			Reference:   result.Reference,
			Remediation: result.Remediation,
		}
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {

		if err := tx.Create(&newResults).Error; err != nil {
			return err
		}

		if err := tx.Model(&models.Scan{ID: scanUUID}).Updates(map[string]interface{}{
			"status":       req.Status,
			"started_at":   req.StartedAt,
			"completed_at": req.CompletedAt,
		}).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		log.Printf("Transaction failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save results and update scan status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Results received and scan updated"})
}

// HandleGetScan processes GET /api/scans/:id requests to retrieve scan details.
//
// This handler fetches a scan record by its UUID, including all associated
// scan results. The results are preloaded using GORM's Preload feature.
//
// URL Parameters:
//   - id: UUID of the scan to retrieve
//
// Response (200 OK):
//
//	{
//	  "id": "uuid-string",
//	  "target_url": "https://example.com",
//	  "status": "COMPLETED",
//	  "created_at": "2024-01-01T00:00:00Z",
//	  "started_at": "2024-01-01T00:00:01Z",
//	  "completed_at": "2024-01-01T00:01:00Z",
//	  "results": [...]
//	}
//
// Errors:
//   - 400 Bad Request: Invalid UUID format
//   - 404 Not Found: Scan with given ID does not exist
//   - 500 Internal Server Error: Database query failed
func (h *ScanHandler) HandleGetScan(c *gin.Context) {
	scanIDParam := c.Param("id")
	scanUUID, err := uuid.Parse(scanIDParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Scan ID format"})
		return
	}

	var scan models.Scan
	result := h.db.Preload("Results").First(&scan, "id = ?", scanUUID)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Scan not found"})
		} else {
			log.Printf("Failed to retrieve scan: %v", result.Error)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve scan"})
		}
		return
	}

	c.JSON(http.StatusOK, scan)
}
