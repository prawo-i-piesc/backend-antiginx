package auth

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const SessionCookieName = "ag_session"

func SetSessionCookie(c *gin.Context, token string, ttl time.Duration, secure bool) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookieName, token, int(ttl.Seconds()), "/", "", secure, true)
}

func ClearSessionCookie(c *gin.Context, secure bool) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookieName, "", -1, "/", "", secure, true)
}

func SessionCookie(c *gin.Context) string {
	token, err := c.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return token
}
