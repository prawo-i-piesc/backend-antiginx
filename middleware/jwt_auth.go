package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

func RequireAuth(secret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Access not authorized",
			})
			return
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token format (Bearer required)"})
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		claims, err := auth.ParseToken(secret, tokenString, auth.TokenTypeAccess)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			return
		}

		c.Set("userID", claims.Subject)
		c.Set("userRole", claims.Role)

		c.Next()
	}
}

func RequireAdmin(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("userID")
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Access not authorized"})
			return
		}

		userIDStr, ok := userID.(string)
		if !ok || strings.TrimSpace(userIDStr) == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Access not authorized"})
			return
		}

		var user models.User
		result := db.Select("id", "role").Where("id = ?", userIDStr).First(&user)
		if result.Error != nil {
			if result.Error == gorm.ErrRecordNotFound {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
			return
		}

		if strings.ToLower(strings.TrimSpace(user.Role)) != models.UserRoleAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
			return
		}

		c.Set("userRole", models.UserRoleAdmin)
		c.Next()
	}
}
