package handlers

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

type LogoutRequest struct {
	AllDevices bool `json:"all_devices"`
}

func (h *AuthHandler) sessionContext(c *gin.Context, amr string) auth.SessionContext {
	return auth.SessionContext{
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
		AMR:       amr,
	}
}

func (h *AuthHandler) startSession(c *gin.Context, user *models.User, amr string) error {
	token, _, err := h.sessions.Issue(user.ID, h.sessionContext(c, amr))
	if err != nil {
		return err
	}

	auth.SetSessionCookie(c, token, h.cfg.RefreshTokenTTL, h.cfg.CookieSecure)
	return nil
}

func (h *AuthHandler) issueSession(c *gin.Context, user *models.User, amr string, status int) {
	if err := h.startSession(c, user, amr); err != nil {
		log.Printf("Nie udało się utworzyć sesji dla %s: %v", user.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	accessToken, err := h.GenerateToken(user.ID.String(), user.Role)
	if err != nil {
		log.Printf("Nie udało się wygenerować tokenu dostępu: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.JSON(status, h.accessTokenBody(accessToken, user))
}

func (h *AuthHandler) accessTokenBody(accessToken string, user *models.User) gin.H {
	return gin.H{
		"token":        accessToken,
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   int(h.cfg.AccessTokenTTL.Seconds()),
		"user":         publicUser(user),
	}
}

func (h *AuthHandler) HandleRefresh(c *gin.Context) {
	token := auth.SessionCookie(c)
	if token == "" {
		httpx.Fail(c, httpx.CodeSessionExpired)
		return
	}

	newToken, session, err := h.sessions.Rotate(token, h.sessionContext(c, ""))
	switch {
	case errors.Is(err, auth.ErrSessionReuse):
		auth.ClearSessionCookie(c, h.cfg.CookieSecure)
		httpx.Fail(c, httpx.CodeSessionReuseDetected)
		return
	case errors.Is(err, auth.ErrSessionNotFound):
		httpx.Fail(c, httpx.CodeSessionExpired)
		return
	case err != nil:
		log.Printf("Refresh: nie udało się zrotować sesji: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	var user models.User
	if err := h.db.Where("id = ?", session.UserID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpx.Fail(c, httpx.CodeSessionExpired)
			return
		}
		log.Printf("Refresh: błąd bazy danych: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	accessToken, err := h.GenerateToken(user.ID.String(), user.Role)
	if err != nil {
		log.Printf("Refresh: nie udało się wygenerować tokenu dostępu: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	auth.SetSessionCookie(c, newToken, h.cfg.RefreshTokenTTL, h.cfg.CookieSecure)
	c.JSON(http.StatusOK, h.accessTokenBody(accessToken, &user))
}

func (h *AuthHandler) HandleLogout(c *gin.Context) {
	var req LogoutRequest
	_ = c.ShouldBindJSON(&req)

	if req.AllDevices {
		var user models.User
		if h.loadCurrentUser(c, &user) {
			if err := h.sessions.RevokeAll(user.ID); err != nil {
				log.Printf("Logout: nie udało się unieważnić sesji użytkownika %s: %v", user.ID, err)
			}
		} else {
			return
		}
	} else if token := auth.SessionCookie(c); token != "" {
		if err := h.sessions.Revoke(token); err != nil {
			log.Printf("Logout: nie udało się unieważnić sesji: %v", err)
		}
	}

	auth.ClearSessionCookie(c, h.cfg.CookieSecure)
	c.Status(http.StatusNoContent)
}
