package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/models"
)

func HandleScanSubmission(c *gin.Context) {
	var request models.ScanSubmissionRequest

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"status": "PENDING",
		"scanId": "placeholder-scan-id",
	})
}
