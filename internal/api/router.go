package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/handlers"
)

func NewRouter() *gin.Engine {
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "OK",
		})
	})

	api := r.Group("/api")
	{
		api.POST("/scans", handlers.HandleScanSubmission)
	}

	return r
}
