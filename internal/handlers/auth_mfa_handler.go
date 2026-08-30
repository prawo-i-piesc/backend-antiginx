package handlers

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	MFAMethodTOTP         = "totp"
	MFAMethodRecoveryCode = "recovery_code"
)

type StepUpRequest struct {
	Password string `json:"password"`
}

type TOTPActivateRequest struct {
	Code string `json:"code" binding:"required"`
}

type MFAVerifyRequest struct {
	MFAToken string `json:"mfa_token" binding:"required"`
	Method   string `json:"method" binding:"required,oneof=totp recovery_code"`
	Code     string `json:"code" binding:"required"`
}

func (h *AuthHandler) stepUp(c *gin.Context, user *models.User) bool {
	var req StepUpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return false
	}

	if !user.HasPassword() {
		httpx.Fail(c, httpx.CodeStepUpRequired)
		return false
	}

	if err := bcrypt.CompareHashAndPassword(user.Password, []byte(req.Password)); err != nil {
		httpx.Fail(c, httpx.CodeInvalidCredentials)
		return false
	}
	return true
}

func (h *AuthHandler) HandleTOTPEnroll(c *gin.Context) {
	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	if user.TOTPEnabled() {
		httpx.Fail(c, httpx.CodeMFAAlreadyEnabled)
		return
	}

	if !h.stepUp(c, &user) {
		return
	}

	enrollment, err := auth.GenerateTOTPEnrollment(user.Email)
	if err != nil {
		log.Printf("TOTPEnroll: nie udało się wygenerować sekretu: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	encrypted, err := auth.Encrypt(h.cfg.TOTPEncryptionKey, []byte(enrollment.Secret))
	if err != nil {
		log.Printf("TOTPEnroll: nie udało się zaszyfrować sekretu: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	if err := h.db.Model(&user).Update("totp_secret", encrypted).Error; err != nil {
		log.Printf("TOTPEnroll: nie udało się zapisać sekretu: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	h.mfa.ResetEnrollmentFailures(user.ID.String())

	c.JSON(http.StatusOK, gin.H{
		"secret":      enrollment.Secret,
		"otpauth_uri": enrollment.OTPAuthURI,
	})
}

func (h *AuthHandler) HandleTOTPActivate(c *gin.Context) {
	var req TOTPActivateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	if user.TOTPEnabled() {
		httpx.Fail(c, httpx.CodeMFAAlreadyEnabled)
		return
	}
	if len(user.TOTPSecret) == 0 {
		httpx.Fail(c, httpx.CodeMFANotEnabled)
		return
	}

	secret, err := auth.Decrypt(h.cfg.TOTPEncryptionKey, user.TOTPSecret)
	if err != nil {
		log.Printf("TOTPActivate: nie udało się odszyfrować sekretu użytkownika %s: %v", user.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	if !auth.ValidateTOTPCode(string(secret), req.Code) {
		if h.mfa.CountEnrollmentFailure(user.ID.String()) {
			if err := h.db.Model(&user).Updates(map[string]any{"totp_secret": nil}).Error; err != nil {
				log.Printf("TOTPActivate: nie udało się skasować enrollmentu: %v", err)
			}
		}
		httpx.Fail(c, httpx.CodeMFAInvalidCode)
		return
	}

	now := time.Now()
	codes, err := h.replaceRecoveryCodes(user.ID)
	if err != nil {
		log.Printf("TOTPActivate: nie udało się wygenerować kodów odzyskiwania: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	if err := h.db.Model(&user).Update("totp_enabled_at", now).Error; err != nil {
		log.Printf("TOTPActivate: nie udało się włączyć 2FA: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	h.mfa.ResetEnrollmentFailures(user.ID.String())

	c.JSON(http.StatusOK, gin.H{
		"recovery_codes": codes,
		"generated_at":   now.UTC(),
	})
}

func (h *AuthHandler) HandleTOTPDisable(c *gin.Context) {
	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	if !user.TOTPEnabled() {
		httpx.Fail(c, httpx.CodeMFANotEnabled)
		return
	}

	if !h.stepUp(c, &user) {
		return
	}

	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&user).Updates(map[string]any{
			"totp_secret":     nil,
			"totp_enabled_at": nil,
		}).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", user.ID).Delete(&models.RecoveryCode{}).Error
	})
	if err != nil {
		log.Printf("TOTPDisable: nie udało się wyłączyć 2FA: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) HandleRegenerateRecoveryCodes(c *gin.Context) {
	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	if !user.TOTPEnabled() {
		httpx.Fail(c, httpx.CodeMFANotEnabled)
		return
	}

	if !h.stepUp(c, &user) {
		return
	}

	codes, err := h.replaceRecoveryCodes(user.ID)
	if err != nil {
		log.Printf("RegenerateRecoveryCodes: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"recovery_codes": codes,
		"generated_at":   time.Now().UTC(),
	})
}

func (h *AuthHandler) HandleMFAVerify(c *gin.Context) {
	var req MFAVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	claims, err := auth.ParseToken(h.cfg.JWTSecret, req.MFAToken, auth.TokenTypeMFA)
	if err != nil {
		httpx.Fail(c, httpx.CodeMFATokenExpired)
		return
	}

	userID, err := h.mfa.Attempt(claims.ID)
	switch {
	case errors.Is(err, auth.ErrTooManyAttempts):
		httpx.Fail(c, httpx.CodeRateLimited)
		return
	case err != nil:
		httpx.Fail(c, httpx.CodeMFATokenExpired)
		return
	case userID != claims.Subject:
		httpx.Fail(c, httpx.CodeMFATokenExpired)
		return
	}

	var user models.User
	if err := h.db.Where("id = ?", userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpx.Fail(c, httpx.CodeMFATokenExpired)
			return
		}
		log.Printf("MFAVerify: błąd bazy danych: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	var verified bool
	switch req.Method {
	case MFAMethodTOTP:
		verified = h.verifyTOTP(&user, req.Code)
		if !verified {
			httpx.Fail(c, httpx.CodeMFAInvalidCode)
			return
		}
	case MFAMethodRecoveryCode:
		verified, err = h.consumeRecoveryCode(user.ID, req.Code)
		if err != nil {
			log.Printf("MFAVerify: błąd bazy danych przy kodzie odzyskiwania: %v", err)
			httpx.Fail(c, httpx.CodeInternal)
			return
		}
		if !verified {
			httpx.Fail(c, httpx.CodeRecoveryCodeInvalid)
			return
		}
	}

	h.mfa.Consume(claims.ID)
	h.issueSession(c, &user, models.AMRPasswordOTP, http.StatusOK)
}

func (h *AuthHandler) verifyTOTP(user *models.User, code string) bool {
	if !user.TOTPEnabled() || len(user.TOTPSecret) == 0 {
		return false
	}

	secret, err := auth.Decrypt(h.cfg.TOTPEncryptionKey, user.TOTPSecret)
	if err != nil {
		log.Printf("Nie udało się odszyfrować sekretu TOTP użytkownika %s: %v", user.ID, err)
		return false
	}
	return auth.ValidateTOTPCode(string(secret), code)
}

func (h *AuthHandler) consumeRecoveryCode(userID uuid.UUID, code string) (bool, error) {
	candidate := auth.HashRecoveryCode(code)

	var stored []models.RecoveryCode
	if err := h.db.Where("user_id = ? AND used_at IS NULL", userID).Find(&stored).Error; err != nil {
		return false, err
	}

	for _, rc := range stored {
		if !auth.RecoveryCodeMatches(rc.CodeHash, candidate) {
			continue
		}

		now := time.Now()
		result := h.db.Model(&models.RecoveryCode{}).
			Where("id = ? AND used_at IS NULL", rc.ID).
			Update("used_at", now)
		if result.Error != nil {
			return false, result.Error
		}
		return result.RowsAffected == 1, nil
	}
	return false, nil
}

func (h *AuthHandler) replaceRecoveryCodes(userID uuid.UUID) ([]string, error) {
	codes, err := auth.GenerateRecoveryCodes()
	if err != nil {
		return nil, err
	}

	records := make([]models.RecoveryCode, 0, len(codes))
	for _, code := range codes {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		records = append(records, models.RecoveryCode{
			ID:        id,
			UserID:    userID,
			CodeHash:  auth.HashRecoveryCode(code),
			CreatedAt: time.Now(),
		})
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&models.RecoveryCode{}).Error; err != nil {
			return err
		}
		return tx.Create(&records).Error
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

func (h *AuthHandler) countUnusedRecoveryCodes(userID uuid.UUID) int64 {
	var count int64
	if err := h.db.Model(&models.RecoveryCode{}).
		Where("user_id = ? AND used_at IS NULL", userID).
		Count(&count).Error; err != nil {
		log.Printf("Nie udało się policzyć kodów odzyskiwania użytkownika %s: %v", userID, err)
		return 0
	}
	return count
}
