// Package api provides HTTP routing configuration for the backend-antiginx service.
//
// This package defines the API routes and their mappings to handler functions
// using the Gin web framework.
package api

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/handlers"
)

// NewRouter creates and configures a new Gin router with all API endpoints.
//
// The router exposes the following public endpoints under /api prefix:
//
//   - POST /api/scans    - Submit a new security scan request
//   - POST /api/results  - Submit scan results from a worker
//   - GET  /api/scans/:id - Retrieve scan details and results by ID
//
// Parameters:
//   - scanHandler: Handler instance containing business logic for scan operations
//
// Returns:
//   - *gin.Engine: Configured Gin router ready to serve HTTP requests
//
// Example:
//
//	handler := handlers.NewScanHandler(amqpChannel, db)
//	router := api.NewRouter(handler)
//	router.Run(":8080")
func NewRouter(scanHandler *handlers.ScanHandler) *gin.Engine {
	r := gin.Default()

	// Configure CORS for frontend communication
	// Note: AllowCredentials requires specific origins, not AllowAllOrigins
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000", "http://localhost:5173"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	public := r.Group("/api")
	{
		public.POST("/scans", scanHandler.HandleScanSubmission)
		public.POST("/results", scanHandler.HandleResultSubmission)
		public.GET("/scans/:id", scanHandler.HandleGetScan)
	}

	return r
}
