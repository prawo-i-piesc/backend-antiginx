package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/httpx"
)

func RequireOrigin(expected string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := strings.ToLower(strings.TrimSpace(c.GetHeader("Origin")))
		if origin != "" && origin != expected {
			httpx.Fail(c, httpx.CodeForbidden)
			return
		}
		c.Next()
	}
}
