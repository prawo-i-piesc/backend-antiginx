package api

import (
	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/handlers"
)

func NewRouter(scanHandler *handlers.ScanHandler) *gin.Engine {
	r := gin.Default()

	public := r.Group("/api")
	{
		public.POST("/scans", scanHandler.HandleScanSubmission)
		public.POST("/results", scanHandler.HandleResultSubmission)
	}

	return r
}
