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
)

type ScanHandler struct {
	amqpChannel *amqp.Channel
}

func NewScanHandler(ch *amqp.Channel) *ScanHandler {
	return &ScanHandler{
		amqpChannel: ch,
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
		newScanID = uuid.New()
	}

	newScan := models.Scan{
		ID:        newScanID,
		TargetURL: req.TargetURL,
		Status:    "PENDING",
		CreatedAt: time.Now(),
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

	//TODO: DATABASE UPDATE LOGIC HERE
	log.Printf("Received results for Scan ID: %s, Status: %s, Results Count: %d\n", req.ScanID, req.Status, len(req.Results))

	c.JSON(http.StatusOK, gin.H{"message": "Results received"})
}
