package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/mail"
	"github.com/prawo-i-piesc/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	EmailVerificationTTL = 24 * time.Hour
	PasswordResetTTL     = 30 * time.Minute

	verifyEmailPath   = "/verify-email"
	resetPasswordPath = "/reset-password"

	forgotPerAddress    = 3
	forgotPerIP         = 10
	verificationPerUser = 3
	mailWindow          = time.Hour

	mailSendTimeout = 20 * time.Second
)

type EmailVerifyRequest struct {
	Token string `json:"token" binding:"required"`
}

type PasswordForgotRequest struct {
	Email string `json:"email" binding:"required,email"`
}

type PasswordResetRequest struct {
	Token       string `json:"token" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

func (h *AuthHandler) sendAsync(msg mail.Message) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), mailSendTimeout)
		defer cancel()

		if err := h.mailer.Send(ctx, msg); err != nil {
			log.Printf("Nie udało się wysłać wiadomości %q: %v", msg.Subject, err)
		}
	}()
}

func (h *AuthHandler) SendEmailVerification(user *models.User) {
	token, hash, err := auth.GenerateSecretToken()
	if err != nil {
		log.Printf("EmailVerification: nie udało się wygenerować tokenu: %v", err)
		return
	}

	id, err := uuid.NewV7()
	if err != nil {
		log.Printf("EmailVerification: nie udało się wygenerować UUIDv7: %v", err)
		return
	}

	now := time.Now()
	record := models.EmailVerificationToken{
		ID:        id,
		UserID:    user.ID,
		Email:     user.Email,
		TokenHash: hash,
		CreatedAt: now,
		ExpiresAt: now.Add(EmailVerificationTTL),
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ? AND used_at IS NULL", user.ID).
			Delete(&models.EmailVerificationToken{}).Error; err != nil {
			return err
		}
		return tx.Create(&record).Error
	})
	if err != nil {
		log.Printf("EmailVerification: nie udało się zapisać tokenu: %v", err)
		return
	}

	h.sendAsync(mail.VerificationMessage(user.Email, h.frontendLink(verifyEmailPath, token)))
}

func (h *AuthHandler) frontendLink(path, token string) string {
	return h.cfg.PublicBaseURL + path + "?token=" + url.QueryEscape(token)
}

func (h *AuthHandler) HandleEmailVerificationRequest(c *gin.Context) {
	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	if user.EmailVerified {
		c.Status(http.StatusNoContent)
		return
	}

	if allowed, retryAfter := h.limiter.Allow("verify:"+user.ID.String(), verificationPerUser, mailWindow); !allowed {
		httpx.FailRetryAfter(c, httpx.CodeRateLimited, retryAfter)
		return
	}

	h.SendEmailVerification(&user)
	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) HandleEmailVerify(c *gin.Context) {
	var req EmailVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	var record models.EmailVerificationToken
	err := h.db.Where("token_hash = ?", auth.HashSecretToken(req.Token)).First(&record).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpx.Fail(c, httpx.CodeTokenInvalid)
			return
		}
		log.Printf("EmailVerify: błąd bazy danych: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	if record.UsedAt != nil {
		httpx.Fail(c, httpx.CodeTokenInvalid)
		return
	}
	if time.Now().After(record.ExpiresAt) {
		httpx.Fail(c, httpx.CodeTokenExpired)
		return
	}

	var user models.User
	if err := h.db.Where("id = ?", record.UserID).First(&user).Error; err != nil {
		httpx.Fail(c, httpx.CodeTokenInvalid)
		return
	}

	if !sameEmail(user.Email, record.Email) {
		httpx.Fail(c, httpx.CodeTokenInvalid)
		return
	}

	now := time.Now()
	err = h.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.EmailVerificationToken{}).
			Where("id = ? AND used_at IS NULL", record.ID).
			Update("used_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return tx.Model(&user).Update("email_verified", true).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpx.Fail(c, httpx.CodeTokenInvalid)
			return
		}
		log.Printf("EmailVerify: nie udało się potwierdzić adresu: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) HandlePasswordForgot(c *gin.Context) {
	var req PasswordForgotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	email := auth.NormalizeEmail(req.Email)

	if allowed, retryAfter := h.limiter.Allow("forgot-ip:"+c.ClientIP(), forgotPerIP, mailWindow); !allowed {
		httpx.FailRetryAfter(c, httpx.CodeRateLimited, retryAfter)
		return
	}
	if allowed, _ := h.limiter.Allow("forgot-address:"+email, forgotPerAddress, mailWindow); allowed {
		h.issuePasswordReset(email)
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message": "If the account exists, a reset link has been sent",
	})
}

func (h *AuthHandler) issuePasswordReset(email string) {
	var user models.User
	if err := byEmail(h.db, email).First(&user).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("PasswordForgot: błąd bazy danych: %v", err)
		}
		return
	}

	token, hash, err := auth.GenerateSecretToken()
	if err != nil {
		log.Printf("PasswordForgot: nie udało się wygenerować tokenu: %v", err)
		return
	}

	id, err := uuid.NewV7()
	if err != nil {
		log.Printf("PasswordForgot: nie udało się wygenerować UUIDv7: %v", err)
		return
	}

	now := time.Now()
	record := models.PasswordResetToken{
		ID:        id,
		UserID:    user.ID,
		TokenHash: hash,
		CreatedAt: now,
		ExpiresAt: now.Add(PasswordResetTTL),
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ? AND used_at IS NULL", user.ID).
			Delete(&models.PasswordResetToken{}).Error; err != nil {
			return err
		}
		return tx.Create(&record).Error
	})
	if err != nil {
		log.Printf("PasswordForgot: nie udało się zapisać tokenu: %v", err)
		return
	}

	h.sendAsync(mail.PasswordResetMessage(user.Email, h.frontendLink(resetPasswordPath, token)))
}

func (h *AuthHandler) HandlePasswordReset(c *gin.Context) {
	var req PasswordResetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	var record models.PasswordResetToken
	err := h.db.Where("token_hash = ?", auth.HashSecretToken(req.Token)).First(&record).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpx.Fail(c, httpx.CodeTokenInvalid)
			return
		}
		log.Printf("PasswordReset: błąd bazy danych: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	if record.UsedAt != nil {
		httpx.Fail(c, httpx.CodeTokenInvalid)
		return
	}
	if time.Now().After(record.ExpiresAt) {
		httpx.Fail(c, httpx.CodeTokenExpired)
		return
	}

	if err := auth.ValidatePassword(req.NewPassword); err != nil {
		httpx.Fail(c, httpx.CodePasswordTooWeak)
		return
	}

	var user models.User
	if err := h.db.Where("id = ?", record.UserID).First(&user).Error; err != nil {
		httpx.Fail(c, httpx.CodeTokenInvalid)
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcryptCost)
	if err != nil {
		log.Printf("PasswordReset: nie udało się zahaszować hasła: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.PasswordResetToken{}).
			Where("id = ? AND used_at IS NULL", record.ID).
			Update("used_at", time.Now())
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return tx.Model(&user).Update("password", hashed).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpx.Fail(c, httpx.CodeTokenInvalid)
			return
		}
		log.Printf("PasswordReset: nie udało się ustawić hasła: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	if err := h.sessions.RevokeAll(user.ID); err != nil {
		log.Printf("PasswordReset: nie udało się unieważnić sesji użytkownika %s: %v", user.ID, err)
	}

	h.limiter.Reset("forgot-address:" + user.Email)
	auth.ClearSessionCookie(c, h.cfg.CookieSecure)

	response := gin.H{"message": "Password has been reset"}

	if user.TOTPEnabled() {
		codes, err := h.replaceRecoveryCodes(user.ID)
		if err != nil {
			log.Printf("PasswordReset: nie udało się odnowić kodów odzyskiwania: %v", err)
		} else {
			response["recovery_codes"] = codes
			response["generated_at"] = time.Now().UTC()
		}
	}

	c.JSON(http.StatusOK, response)
}

func sameEmail(a, b string) bool {
	return auth.NormalizeEmail(a) == auth.NormalizeEmail(b)
}
