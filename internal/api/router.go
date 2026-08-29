// Package api provides HTTP routing configuration for the backend-antiginx service.
//
// This package defines the API routes and their mappings to handler functions
// using the Gin web framework.
package api

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/config"
	"github.com/prawo-i-piesc/backend/internal/handlers"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/middleware"
)

var quietPaths = []string{"/api/auth/refresh"}

func NewRouter(scanHandler *handlers.ScanHandler, authHandler *handlers.AuthHandler, adminHandler *handlers.AdminHandler, cfg *config.Config) *gin.Engine {
	httpx.RegisterValidationFieldNames()

	r := gin.New()
	r.Use(
		gin.LoggerWithConfig(gin.LoggerConfig{SkipPaths: quietPaths}),
		gin.Recovery(),
	)

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{cfg.PublicBaseURL},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	r.Use(func(c *gin.Context) {
		c.Header("X-Frame-Options", "DENY")
		c.Header("Content-Security-Policy", "default-src 'none'; connect-src *; font-src *; script-src-elem * 'unsafe-inline'; img-src * data:; style-src * 'unsafe-inline'; frame-ancestors 'none';")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Permissions-Policy", "geolocation=(),midi=(),sync-xhr=(),microphone=(),camera=(),magnetometer=(),gyroscope=(),fullscreen=(self),payment=()")
		c.Next()
	})

	public := r.Group("/api")
	{
		public.POST("/freescans", scanHandler.HandleScanSubmission)
		public.POST("/results", scanHandler.HandleResultSubmission)
		public.GET("/freescans/:id", scanHandler.HandleGetScan)
		public.GET("/health", scanHandler.HandleHealthCheck)
		public.POST("/auth/register", authHandler.Register)
		public.POST("/auth/login", authHandler.Login)
	}

	protected := r.Group("/api")
	protected.Use(middleware.RequireAuth(cfg.JWTSecret))
	{
		protected.GET("/auth/me", authHandler.Me)
		protected.POST("/scans", scanHandler.HandlePremiumScanSubmission)
		protected.GET("/scans/:id", scanHandler.HandlePremiumGetScan)
		protected.GET("/users/scans", scanHandler.HandleUserScans)
		protected.GET("/users/widgets", scanHandler.HandleUserDashboardWidgets)
		//Tutaj karol masz enpointa
		protected.GET("/utils/tests", scanHandler.HandleAvailableScans)
		protected.PATCH("/utils/profile/name", authHandler.HandleUpdateFullName)
		protected.PATCH("/utils/profile/email", authHandler.HandleUpdateEmail)
		protected.PATCH("/utils/profile/password", authHandler.HandleUpdatePassword)
	}

	mfaPublic := r.Group("/api/auth/mfa")
	mfaPublic.Use(middleware.RequireOrigin(cfg.PublicBaseURL))
	{
		mfaPublic.POST("/verify", authHandler.HandleMFAVerify)
	}

	mfa := r.Group("/api/auth/mfa")
	mfa.Use(middleware.RequireOrigin(cfg.PublicBaseURL), middleware.RequireAuth(cfg.JWTSecret))
	{
		mfa.POST("/totp/enroll", authHandler.HandleTOTPEnroll)
		mfa.POST("/totp/activate", authHandler.HandleTOTPActivate)
		mfa.DELETE("/totp", authHandler.HandleTOTPDisable)
		mfa.POST("/recovery-codes/regenerate", authHandler.HandleRegenerateRecoveryCodes)
	}

	admin := r.Group("/api/admin")
	admin.Use(middleware.RequireAuth(cfg.JWTSecret), middleware.RequireAdmin(authHandler.DB()))
	{
		admin.GET("/health", func(c *gin.Context) {
			c.JSON(200, gin.H{"status": "ok"})
		})
		admin.GET("/database", adminHandler.HandleGetDatabaseInfo)

		admin.GET("/widgets", adminHandler.HandleGetDashboardWidgets)
	}

	return r
}
