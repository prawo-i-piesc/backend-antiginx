package middleware

import (
	"errors"
	"log"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

func RequireAuth(secret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			httpx.Fail(c, httpx.CodeSessionExpired)
			return
		}
		authenticate(c, secret, strings.TrimPrefix(authHeader, "Bearer "))
	}
}

// RequireAccessCookie to RequireAuth dla handshake'u WebSocketa: przeglądarka nie
// dołoży do niego nagłówka Authorization, a połączenie idzie z pominięciem proxy
// frontu, które robi to dla REST. Poza takimi trasami token w ciasteczku otwierałby
// drogę do CSRF, więc muszą też mieć RequireOrigin.
func RequireAccessCookie(secret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(auth.AccessCookieName)
		if err != nil {
			httpx.Fail(c, httpx.CodeSessionExpired)
			return
		}
		authenticate(c, secret, token)
	}
}

func authenticate(c *gin.Context, secret []byte, token string) {
	claims, err := auth.ParseToken(secret, token, auth.TokenTypeAccess)
	if err != nil {
		httpx.Fail(c, httpx.CodeSessionExpired)
		return
	}

	c.Set("userID", claims.Subject)
	c.Set("userRole", claims.Role)
	c.Set(auth.TokenExpiryKey, claims.ExpiresAt)

	c.Next()
}

func RequireAdmin(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("userID")
		if !exists {
			log.Printf("RequireAdmin: brak userID w kontekście — trasa poza RequireAuth")
			httpx.Fail(c, httpx.CodeInternal)
			return
		}

		userIDStr, ok := userID.(string)
		if !ok || strings.TrimSpace(userIDStr) == "" {
			log.Printf("RequireAdmin: userID w kontekście ma nieoczekiwany typ %T", userID)
			httpx.Fail(c, httpx.CodeInternal)
			return
		}

		var user models.User
		if err := db.Select("id", "role").Where("id = ?", userIDStr).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				httpx.Fail(c, httpx.CodeSessionExpired)
				return
			}
			log.Printf("RequireAdmin: błąd bazy danych przy pobieraniu roli: %v", err)
			httpx.Fail(c, httpx.CodeInternal)
			return
		}

		if strings.ToLower(strings.TrimSpace(user.Role)) != models.UserRoleAdmin {
			httpx.Fail(c, httpx.CodeForbidden)
			return
		}

		c.Set("userRole", models.UserRoleAdmin)
		c.Next()
	}
}
