// Package api provides HTTP routing configuration for the backend-antiginx service.
//
// This package defines the API routes and their mappings to handler functions
// using the Gin web framework.
package api

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/config"
	"github.com/prawo-i-piesc/backend/internal/handlers"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/monitoring"
	"github.com/prawo-i-piesc/backend/middleware"
)

var quietPaths = []string{"/api/auth/refresh", "/healthz", "/readyz"}

const maxRequestBody = 1 << 20

func NewRouter(scanHandler *handlers.ScanHandler, authHandler *handlers.AuthHandler, adminHandler *handlers.AdminHandler, billingHandler *handlers.BillingHandler, dashboardHandler *handlers.DashboardHandler, systemHandler *handlers.SystemHandler, cfg *config.Config, probes []monitoring.Probe) *gin.Engine {
	httpx.RegisterValidationFieldNames()

	r := gin.New()
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		log.Printf("Router: nieprawidłowe TRUSTED_PROXIES, zostaję przy adresie połączenia: %v", err)
	}
	r.HandleMethodNotAllowed = true

	r.Use(
		gin.LoggerWithConfig(gin.LoggerConfig{SkipPaths: quietPaths}),
		gin.Recovery(),
	)

	// Sondy rejestrujemy przed CORS i resztą middleware'u: gin składa łańcuch
	// handlerów w chwili rejestracji trasy, więc to, co dochodzi przez Use
	// niżej, ich nie dotyczy. Kubelet nie wysyła Origin i nie potrzebuje CSP.
	r.GET("/healthz", healthz)
	r.GET("/readyz", readyz(probes))

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{cfg.PublicBaseURL},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length", "Content-Disposition", "Retry-After"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	r.Use(securityHeaders(cfg.CookieSecure), limitRequestBody(maxRequestBody))

	r.NoRoute(func(c *gin.Context) { httpx.Fail(c, httpx.CodeNotFound) })
	r.NoMethod(func(c *gin.Context) { httpx.Fail(c, httpx.CodeMethodNotAllowed) })

	public := r.Group("/api")
	{
		public.POST("/freescans", scanHandler.HandleScanSubmission)
		public.GET("/freescans/:id", scanHandler.HandleGetScan)
		public.GET("/freescans/:id/report", scanHandler.HandleFreeScanReport)
		public.GET("/health", scanHandler.HandleHealthCheck)
	}

	authPublic := r.Group("/api/auth")
	authPublic.Use(middleware.RequireOrigin(cfg.PublicBaseURL))
	{
		authPublic.POST("/register", authHandler.Register)
		authPublic.POST("/login", authHandler.Login)
		authPublic.POST("/refresh", authHandler.HandleRefresh)
	}

	sessionProtected := r.Group("/api/auth")
	sessionProtected.Use(middleware.RequireOrigin(cfg.PublicBaseURL), middleware.RequireAuth(cfg.JWTSecret))
	{
		sessionProtected.POST("/logout", authHandler.HandleLogout)
		sessionProtected.DELETE("/account", authHandler.HandleDeleteAccount)
	}

	protected := r.Group("/api")
	protected.Use(middleware.RequireAuth(cfg.JWTSecret))
	{
		protected.GET("/auth/me", authHandler.Me)
		protected.POST("/scans", scanHandler.HandlePremiumScanSubmission)
		protected.GET("/scans/:id", scanHandler.HandlePremiumGetScan)
		protected.GET("/scans/:id/report", scanHandler.HandlePremiumScanReport)
		protected.GET("/users/scans", scanHandler.HandleUserScans)
		protected.GET("/users/widgets", scanHandler.HandleUserDashboardWidgets)
		protected.GET("/users/stats/summary", dashboardHandler.HandleSummary)
		protected.GET("/users/stats/risk", dashboardHandler.HandleRiskTrend)
		protected.GET("/users/stats/severity", dashboardHandler.HandleSeverity)
		protected.GET("/users/stats/tests", dashboardHandler.HandleTests)
		protected.GET("/users/stats/domains", dashboardHandler.HandleDomains)
		protected.GET("/users/preferences/:key", dashboardHandler.HandleGetPreference)
		protected.GET("/utils/tests", scanHandler.HandleAvailableScans)
		protected.PATCH("/utils/profile/name", authHandler.HandleUpdateFullName)
		protected.PATCH("/utils/profile/email", authHandler.HandleUpdateEmail)
		protected.PATCH("/utils/profile/password", authHandler.HandleUpdatePassword)
	}

	mfaPublic := r.Group("/api/auth/mfa")
	mfaPublic.Use(middleware.RequireOrigin(cfg.PublicBaseURL))
	{
		mfaPublic.POST("/verify", authHandler.HandleMFAVerify)
		mfaPublic.POST("/webauthn/options", authHandler.HandleMFAWebAuthnOptions)
		mfaPublic.POST("/webauthn/verify", authHandler.HandleMFAWebAuthnVerify)
	}

	mfa := r.Group("/api/auth/mfa")
	mfa.Use(middleware.RequireOrigin(cfg.PublicBaseURL), middleware.RequireAuth(cfg.JWTSecret))
	{
		mfa.POST("/totp/enroll", authHandler.HandleTOTPEnroll)
		mfa.POST("/totp/activate", authHandler.HandleTOTPActivate)
		mfa.DELETE("/totp", authHandler.HandleTOTPDisable)
		mfa.POST("/recovery-codes/regenerate", authHandler.HandleRegenerateRecoveryCodes)
	}

	oauthPublic := r.Group("/api/auth/oauth")
	{
		oauthPublic.GET("/:provider/start", authHandler.HandleOAuthStart)
		oauthPublic.GET("/:provider/callback", authHandler.HandleOAuthCallback)
	}

	oauthProtected := r.Group("/api/auth/oauth")
	oauthProtected.Use(middleware.RequireOrigin(cfg.PublicBaseURL), middleware.RequireAuth(cfg.JWTSecret))
	{
		oauthProtected.POST("/:provider/link", authHandler.HandleOAuthLink)
		oauthProtected.DELETE("/:provider", authHandler.HandleOAuthUnlink)
	}

	oauthLink := r.Group("/api/auth/oauth-link")
	oauthLink.Use(middleware.RequireOrigin(cfg.PublicBaseURL))
	{
		oauthLink.GET("/pending", authHandler.HandleOAuthLinkPending)
		oauthLink.POST("/confirm", authHandler.HandleOAuthLinkConfirm)
	}

	webauthnPublic := r.Group("/api/auth/webauthn")
	webauthnPublic.Use(middleware.RequireOrigin(cfg.PublicBaseURL))
	{
		webauthnPublic.POST("/login/options", authHandler.HandleWebAuthnLoginOptions)
		webauthnPublic.POST("/login/verify", authHandler.HandleWebAuthnLoginVerify)
	}

	webauthn := r.Group("/api/auth/webauthn")
	webauthn.Use(middleware.RequireOrigin(cfg.PublicBaseURL), middleware.RequireAuth(cfg.JWTSecret))
	{
		webauthn.POST("/register/options", authHandler.HandleWebAuthnRegisterOptions)
		webauthn.POST("/register/verify", authHandler.HandleWebAuthnRegisterVerify)
		webauthn.GET("/credentials", authHandler.HandleWebAuthnCredentials)
		webauthn.DELETE("/credentials/:id", authHandler.HandleWebAuthnDeleteCredential)
		webauthn.PUT("/mode", authHandler.HandleSetPasskeyMode)
	}

	mailPublic := r.Group("/api/auth")
	mailPublic.Use(middleware.RequireOrigin(cfg.PublicBaseURL))
	{
		mailPublic.POST("/email/verify", authHandler.HandleEmailVerify)
		mailPublic.POST("/password/forgot", authHandler.HandlePasswordForgot)
		mailPublic.POST("/password/reset", authHandler.HandlePasswordReset)
	}

	mailProtected := r.Group("/api/auth")
	mailProtected.Use(middleware.RequireOrigin(cfg.PublicBaseURL), middleware.RequireAuth(cfg.JWTSecret))
	{
		mailProtected.POST("/email/verify/request", authHandler.HandleEmailVerificationRequest)
	}

	preferences := r.Group("/api/users/preferences")
	preferences.Use(middleware.RequireOrigin(cfg.PublicBaseURL), middleware.RequireAuth(cfg.JWTSecret))
	{
		preferences.PUT("/:key", dashboardHandler.HandlePutPreference)
	}

	billing := r.Group("/api/billing")
	billing.Use(middleware.RequireOrigin(cfg.PublicBaseURL), middleware.RequireAuth(cfg.JWTSecret))
	{
		billing.GET("", billingHandler.HandleStatus)
		billing.POST("/checkout", billingHandler.HandleCheckout)
		billing.POST("/portal", billingHandler.HandlePortal)
		billing.POST("/sync", billingHandler.HandleSync)
	}

	// Stripe nie ma sesji ani Origin; tożsamość potwierdza podpis zdarzenia.
	r.POST("/api/billing/webhook", billingHandler.HandleWebhook)

	admin := r.Group("/api/admin")
	admin.Use(middleware.RequireAuth(cfg.JWTSecret), middleware.RequireAdmin(authHandler.DB()))
	{
		admin.GET("/widgets", adminHandler.HandleGetDashboardWidgets)
		admin.GET("/stats/tests", adminHandler.HandleTestStats)
		admin.GET("/system", systemHandler.HandleSystem)

		admin.GET("/users", adminHandler.HandleListUsers)
		admin.GET("/users/:id", adminHandler.HandleGetUser)
		admin.PATCH("/users/:id/role", adminHandler.HandleSetUserRole)
		admin.POST("/users/:id/logout", adminHandler.HandleLogoutUser)
		admin.DELETE("/users/:id", adminHandler.HandleDeleteUser)

		admin.GET("/scans", adminHandler.HandleListScans)
		admin.GET("/scans/:id", adminHandler.HandleGetScan)
		admin.DELETE("/scans/:id", adminHandler.HandleDeleteScan)
	}

	return r
}

func securityHeaders(httpsOnly bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Frame-Options", "DENY")
		c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Permissions-Policy", "geolocation=(),midi=(),sync-xhr=(),microphone=(),camera=(),magnetometer=(),gyroscope=(),fullscreen=(self),payment=()")
		if httpsOnly {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}

func limitRequestBody(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}
