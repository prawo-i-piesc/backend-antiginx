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
	"gorm.io/datatypes"
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
	TargetURL string `json:"target_url" binding:"required"`
}

type EngineTestResult struct {
	Name        string      `json:"Name"`
	Certainty   int         `json:"Certainty"`
	ThreatLevel string      `json:"ThreatLevel"`
	Metadata    interface{} `json:"Metadata"`
	Description string      `json:"Description"`
}

type AsyncResultRequest struct {
	Target string           `json:"target"`
	TestID string           `json:"testId"`
	Result EngineTestResult `json:"result"`
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

func (h *ScanHandler) HandleHealthCheck(c *gin.Context) {
	c.JSON(200, gin.H{
		"message": "Running...",
	})
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

	task := struct {
		ID        string `json:"id"`
		TargetURL string `json:"target_url"`
	}{
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
		"",
		"scan_queue",
		false,
		false,
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			ContentType:  "application/json",
			Body:         jsonBytes,
		})

	if err != nil {
		log.Printf("Failed to publish message: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to queue scan"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"scanId": newScan.ID.String(),
		"status": newScan.Status,
	})
}

func (h *ScanHandler) HandleResultSubmission(c *gin.Context) {
	var req AsyncResultRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("Binding error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	scanUUID, err := uuid.Parse(req.TestID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Scan ID format (from testId field)"})
		return
	}

	if req.Result.Name == "" {
		now := time.Now()

		err := h.db.Model(&models.Scan{ID: scanUUID}).
			Updates(map[string]interface{}{
				"status":       "COMPLETED",
				"completed_at": &now,
			}).Error

		if err != nil {
			log.Printf("Failed to complete scan %s: %v", scanUUID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update scan status"})
			return
		}

		log.Printf("Scan %s completed successfully", scanUUID)
		c.JSON(http.StatusOK, gin.H{"message": "Scan completed"})
		return
	}

	metaJSON, _ := json.Marshal(req.Result.Metadata)

	passed := req.Result.ThreatLevel == "None" || req.Result.ThreatLevel == "Info"

	newResult := models.ScanResult{
		ScanID:   scanUUID,
		TestName: req.Result.Name,
		Severity: req.Result.ThreatLevel,
		Passed:   passed,
		Message:  req.Result.Description,
		Metadata: datatypes.JSON(metaJSON),
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&newResult).Error; err != nil {
			return err
		}

		now := time.Now()
		if err := tx.Model(&models.Scan{ID: scanUUID}).
			Where("status = ?", "PENDING").
			Updates(map[string]interface{}{
				"status":     "RUNNING",
				"started_at": &now,
			}).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		log.Printf("Transaction failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save result"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Result received"})
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
