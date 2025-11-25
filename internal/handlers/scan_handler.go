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

type ScanHandler struct {
	amqpChannel *amqp.Channel
	db          *gorm.DB
}

func NewScanHandler(ch *amqp.Channel, db *gorm.DB) *ScanHandler {
	return &ScanHandler{
		amqpChannel: ch,
		db:          db,
	}
}

type CreateScanRequest struct {
	TargetURL string `json:"target_url" binding:"required,url"`
}

type ResultSubmissionRequest struct {
	ScanID      string           `json:"scan_id" binding:"required,uuid"`
	Status      string           `json:"status" binding:"required,oneof=COMPLETED FAILED"`
	StartedAt   time.Time        `json:"started_at" binding:"required"`
	CompletedAt time.Time        `json:"completed_at" binding:"required"`
	Results     []ScanResultItem `json:"results" binding:"required,dive"`
}

type ScanResultItem struct {
	TestID      string `json:"test_id" binding:"required"`
	TestName    string `json:"test_name" binding:"required"`
	Category    string `json:"category" binding:"required"`
	Severity    string `json:"severity" binding:"required"`
	Passed      bool   `json:"passed"`
	Message     string `json:"message"`
	Reference   string `json:"reference"`
	Remediation string `json:"remediation"`
}

type ScanTaskMessage struct {
	ID        string `json:"id"`
	TargetURL string `json:"target_url"`
}

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
