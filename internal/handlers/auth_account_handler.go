package handlers

import (
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type DeleteAccountRequest struct {
	Password string `json:"password"`
	Method   string `json:"method"`
	Code     string `json:"code"`
}

func (h *AuthHandler) HandleDeleteAccount(c *gin.Context) {
	var req DeleteAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		httpx.FailValidation(c, err)
		return
	}

	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	if user.HasPassword() {
		if err := bcrypt.CompareHashAndPassword(user.Password, []byte(req.Password)); err != nil {
			httpx.Fail(c, httpx.CodeInvalidCredentials)
			return
		}
	}

	if user.TOTPEnabled() {
		if req.Code == "" {
			c.JSON(http.StatusOK, gin.H{
				"mfa_required": true,
				"methods":      h.availableMFAMethods(&user),
			})
			return
		}
		if !h.verifySecondFactor(c, &user, req.Method, req.Code) {
			return
		}
	}

	if err := h.deleteAccount(user.ID); err != nil {
		log.Printf("DeleteAccount: nie udało się usunąć konta %s: %v", user.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	auth.ClearSessionCookie(c, h.cfg.CookieSecure)
	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) deleteAccount(userID uuid.UUID) error {
	return h.db.Transaction(func(tx *gorm.DB) error {
		var scanIDs []uuid.UUID
		if err := tx.Model(&models.PremiumScan{}).
			Where("user_id = ?", userID).
			Pluck("id", &scanIDs).Error; err != nil {
			return err
		}

		if len(scanIDs) > 0 {
			if err := tx.Where("scan_id IN ?", scanIDs).Delete(&models.ScanResult{}).Error; err != nil {
				return err
			}
			if err := tx.Where("user_id = ?", userID).Delete(&models.PremiumScan{}).Error; err != nil {
				return err
			}
		}

		if err := tx.Where("user_id = ?", userID).Delete(&models.Session{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&models.RecoveryCode{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&models.OAuthAccount{}).Error; err != nil {
			return err
		}

		return tx.Where("id = ?", userID).Delete(&models.User{}).Error
	})
}
