package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *ScanHandler) HandleAvailableScans(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"available_tests": AvailableTestsList,
	})
}
