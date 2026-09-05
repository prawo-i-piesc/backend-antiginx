package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/auth/passkey"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

const (
	defaultPasskeyName   = "Passkey"
	maxPasskeyNameLength = 128
)

type WebAuthnRegisterRequest struct {
	Credential json.RawMessage `json:"credential" binding:"required"`
	Name       string          `json:"name"`
}

type WebAuthnLoginOptionsRequest struct {
	Email string `json:"email"`
}

type WebAuthnLoginVerifyRequest struct {
	Session    string          `json:"webauthn_session" binding:"required"`
	Credential json.RawMessage `json:"credential" binding:"required"`
}

func (h *AuthHandler) HandleWebAuthnRegisterOptions(c *gin.Context) {
	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	stored, err := h.userCredentials(user.ID)
	if err != nil {
		log.Printf("WebAuthnRegisterOptions: błąd bazy danych: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	creation, session, err := h.passkeys.BeginRegistration(passkey.NewUser(&user, stored))
	if err != nil {
		log.Printf("WebAuthnRegisterOptions: nie udało się rozpocząć rejestracji: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	h.passkeyCeremonies.Put(passkey.RegistrationKey(user.ID), &passkey.Ceremony{
		Data:   *session,
		UserID: user.ID,
	})

	c.JSON(http.StatusOK, creation)
}

func (h *AuthHandler) HandleWebAuthnRegisterVerify(c *gin.Context) {
	var req WebAuthnRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	ceremony, ok := h.passkeyCeremonies.Take(passkey.RegistrationKey(user.ID))
	if !ok {
		httpx.Fail(c, httpx.CodeWebAuthnChallengeInvalid)
		return
	}

	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(req.Credential))
	if err != nil {
		httpx.Fail(c, httpx.CodeWebAuthnVerificationFailed)
		return
	}

	stored, err := h.userCredentials(user.ID)
	if err != nil {
		log.Printf("WebAuthnRegisterVerify: błąd bazy danych: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	credential, err := h.passkeys.CreateCredential(passkey.NewUser(&user, stored), ceremony.Data, parsed)
	if err != nil {
		httpx.Fail(c, httpx.CodeWebAuthnVerificationFailed)
		return
	}

	record := passkey.FromCredential(credential)
	record.UserID = user.ID
	record.CreatedAt = time.Now()
	record.Name = passkeyName(req.Name)

	if record.ID, err = uuid.NewV7(); err != nil {
		log.Printf("WebAuthnRegisterVerify: nie udało się wygenerować UUIDv7: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	if err := h.db.Create(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			httpx.Fail(c, httpx.CodeProviderAlreadyLinked)
			return
		}
		log.Printf("WebAuthnRegisterVerify: nie udało się zapisać klucza: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.JSON(http.StatusCreated, credentialView(&record))
}

func (h *AuthHandler) HandleWebAuthnLoginOptions(c *gin.Context) {
	var req WebAuthnLoginOptionsRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		httpx.FailValidation(c, err)
		return
	}

	ceremony := &passkey.Ceremony{}
	var (
		assertion *protocol.CredentialAssertion
		session   *webauthn.SessionData
		err       error
	)

	if email := auth.NormalizeEmail(req.Email); email != "" {
		var user models.User
		if lookupErr := byEmail(h.db, email).First(&user).Error; lookupErr == nil {
			stored, storedErr := h.userCredentials(user.ID)
			if storedErr != nil {
				log.Printf("WebAuthnLoginOptions: błąd bazy danych: %v", storedErr)
				httpx.Fail(c, httpx.CodeInternal)
				return
			}
			if len(stored) > 0 {
				assertion, session, err = h.passkeys.BeginLogin(passkey.NewUser(&user, stored))
				ceremony.UserID = user.ID
			}
		}
	}

	if assertion == nil {
		assertion, session, err = h.passkeys.BeginDiscoverableLogin()
		ceremony.Discoverable = true
	}
	if err != nil {
		log.Printf("WebAuthnLoginOptions: nie udało się rozpocząć logowania: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	id, err := passkey.NewCeremonyID()
	if err != nil {
		log.Printf("WebAuthnLoginOptions: nie udało się wygenerować identyfikatora: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	ceremony.Data = *session
	h.passkeyCeremonies.Put(id, ceremony)

	c.JSON(http.StatusOK, gin.H{
		"publicKey":        assertion.Response,
		"webauthn_session": id,
	})
}

func (h *AuthHandler) HandleWebAuthnLoginVerify(c *gin.Context) {
	var req WebAuthnLoginVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	ceremony, ok := h.passkeyCeremonies.Take(req.Session)
	if !ok {
		httpx.Fail(c, httpx.CodeWebAuthnChallengeInvalid)
		return
	}

	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(req.Credential))
	if err != nil {
		httpx.Fail(c, httpx.CodeWebAuthnVerificationFailed)
		return
	}

	var user models.User
	var credential *webauthn.Credential

	if ceremony.Discoverable {
		credential, err = h.passkeys.ValidateDiscoverableLogin(func(_, userHandle []byte) (webauthn.User, error) {
			id, handleErr := passkey.UserIDFromHandle(userHandle)
			if handleErr != nil {
				return nil, handleErr
			}
			if lookupErr := h.db.Where("id = ?", id).First(&user).Error; lookupErr != nil {
				return nil, lookupErr
			}
			stored, storedErr := h.userCredentials(id)
			if storedErr != nil {
				return nil, storedErr
			}
			return passkey.NewUser(&user, stored), nil
		}, ceremony.Data, parsed)
	} else {
		if lookupErr := h.db.Where("id = ?", ceremony.UserID).First(&user).Error; lookupErr != nil {
			httpx.Fail(c, httpx.CodeWebAuthnVerificationFailed)
			return
		}
		stored, storedErr := h.userCredentials(user.ID)
		if storedErr != nil {
			log.Printf("WebAuthnLoginVerify: błąd bazy danych: %v", storedErr)
			httpx.Fail(c, httpx.CodeInternal)
			return
		}
		credential, err = h.passkeys.ValidateLogin(passkey.NewUser(&user, stored), ceremony.Data, parsed)
	}

	if err != nil {
		httpx.Fail(c, httpx.CodeWebAuthnVerificationFailed)
		return
	}

	if credential.Authenticator.CloneWarning {
		log.Printf("Ostrzeżenie: licznik podpisów klucza użytkownika %s sugeruje sklonowany authenticator", user.ID)
	}

	if err := h.markCredentialUsed(credential); err != nil {
		log.Printf("WebAuthnLoginVerify: nie udało się zaktualizować klucza: %v", err)
	}

	h.issueSession(c, &user, models.AMRWebAuthn, http.StatusOK)
}

func (h *AuthHandler) HandleWebAuthnCredentials(c *gin.Context) {
	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	stored, err := h.userCredentials(user.ID)
	if err != nil {
		log.Printf("WebAuthnCredentials: błąd bazy danych: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	views := make([]gin.H, 0, len(stored))
	for i := range stored {
		views = append(views, credentialView(&stored[i]))
	}

	c.JSON(http.StatusOK, views)
}

func (h *AuthHandler) HandleWebAuthnDeleteCredential(c *gin.Context) {
	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	credentialID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Fail(c, httpx.CodeCredentialNotFound)
		return
	}

	var record models.WebAuthnCredential
	if err := h.db.Where("id = ? AND user_id = ?", credentialID, user.ID).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpx.Fail(c, httpx.CodeCredentialNotFound)
			return
		}
		log.Printf("WebAuthnDeleteCredential: błąd bazy danych: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	methods, err := h.countLoginMethods(&user)
	if err != nil {
		log.Printf("WebAuthnDeleteCredential: nie udało się policzyć metod logowania: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	if methods <= 1 {
		httpx.Fail(c, httpx.CodeLastLoginMethod)
		return
	}

	if err := h.db.Delete(&record).Error; err != nil {
		log.Printf("WebAuthnDeleteCredential: nie udało się usunąć klucza: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) userCredentials(userID uuid.UUID) ([]models.WebAuthnCredential, error) {
	var stored []models.WebAuthnCredential
	err := h.db.Where("user_id = ?", userID).Order("created_at").Find(&stored).Error
	return stored, err
}

func (h *AuthHandler) countPasskeys(userID uuid.UUID) int64 {
	var count int64
	if err := h.db.Model(&models.WebAuthnCredential{}).
		Where("user_id = ?", userID).
		Count(&count).Error; err != nil {
		log.Printf("Nie udało się policzyć kluczy użytkownika %s: %v", userID, err)
		return 0
	}
	return count
}

func (h *AuthHandler) markCredentialUsed(credential *webauthn.Credential) error {
	now := time.Now()
	return h.db.Model(&models.WebAuthnCredential{}).
		Where("credential_id = ?", credential.ID).
		Updates(map[string]any{
			"sign_count":   credential.Authenticator.SignCount,
			"backup_state": credential.Flags.BackupState,
			"last_used_at": now,
		}).Error
}

func (h *AuthHandler) countLoginMethods(user *models.User) (int64, error) {
	var providers int64
	if err := h.db.Model(&models.OAuthAccount{}).
		Where("user_id = ?", user.ID).
		Count(&providers).Error; err != nil {
		return 0, err
	}

	total := providers + h.countPasskeys(user.ID)
	if user.HasPassword() {
		total++
	}
	return total, nil
}

func passkeyName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return defaultPasskeyName
	}

	runes := []rune(trimmed)
	if len(runes) > maxPasskeyNameLength {
		return string(runes[:maxPasskeyNameLength])
	}
	return trimmed
}

func credentialView(record *models.WebAuthnCredential) gin.H {
	return gin.H{
		"id":           record.ID,
		"name":         record.Name,
		"created_at":   record.CreatedAt,
		"last_used_at": record.LastUsedAt,
	}
}
