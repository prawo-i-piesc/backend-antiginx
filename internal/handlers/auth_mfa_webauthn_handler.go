package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/auth/passkey"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

// Passkey jako drugi składnik po haśle.
//
// Różni się od /auth/webauthn/login/*, które loguje passkeyem zamiast hasła.
// Tutaj hasło zostało już przyjęte, a wyzwanie z mfa_token domyka podpis —
// dlatego ceremonia jest przypięta do użytkownika z wyzwania, nie odkrywana
// z uchwytu, i dlatego sesja dostaje amr "pwd+webauthn".

type MFAWebAuthnOptionsRequest struct {
	MFAToken string `json:"mfa_token" binding:"required"`
}

type MFAWebAuthnVerifyRequest struct {
	MFAToken   string          `json:"mfa_token" binding:"required"`
	Session    string          `json:"webauthn_session" binding:"required"`
	Credential json.RawMessage `json:"credential" binding:"required"`
}

// userFromMFAToken sprawdza token wyzwania, nie licząc próby.
func (h *AuthHandler) userFromMFAToken(c *gin.Context, token string) (*models.User, string, bool) {
	claims, err := auth.ParseToken(h.cfg.JWTSecret, token, auth.TokenTypeMFA)
	if err != nil {
		httpx.Fail(c, httpx.CodeMFATokenExpired)
		return nil, "", false
	}

	userID, ok := h.mfa.Peek(claims.ID)
	if !ok || userID != claims.Subject {
		httpx.Fail(c, httpx.CodeMFATokenExpired)
		return nil, "", false
	}

	var user models.User
	if err := h.db.Where("id = ?", userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpx.Fail(c, httpx.CodeMFATokenExpired)
			return nil, "", false
		}
		log.Printf("MFAWebAuthn: błąd bazy danych: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return nil, "", false
	}

	return &user, claims.ID, true
}

func (h *AuthHandler) HandleMFAWebAuthnOptions(c *gin.Context) {
	var req MFAWebAuthnOptionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	user, _, ok := h.userFromMFAToken(c, req.MFAToken)
	if !ok {
		return
	}

	stored, err := h.userCredentials(user.ID)
	if err != nil {
		log.Printf("MFAWebAuthnOptions: błąd bazy danych: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	if len(stored) == 0 {
		httpx.Fail(c, httpx.CodeCredentialNotFound)
		return
	}

	assertion, session, err := h.passkeys.BeginLogin(passkey.NewUser(user, stored))
	if err != nil {
		log.Printf("MFAWebAuthnOptions: nie udało się rozpocząć: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	id, err := passkey.NewCeremonyID()
	if err != nil {
		log.Printf("MFAWebAuthnOptions: nie udało się wygenerować identyfikatora: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	h.passkeyCeremonies.Put(id, &passkey.Ceremony{Data: *session, UserID: user.ID})

	c.JSON(http.StatusOK, gin.H{
		"publicKey":        assertion.Response,
		"webauthn_session": id,
	})
}

func (h *AuthHandler) HandleMFAWebAuthnVerify(c *gin.Context) {
	var req MFAWebAuthnVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	claims, err := auth.ParseToken(h.cfg.JWTSecret, req.MFAToken, auth.TokenTypeMFA)
	if err != nil {
		httpx.Fail(c, httpx.CodeMFATokenExpired)
		return
	}

	// Tu liczymy próbę: to jest już domykanie wyzwania, nie pobranie opcji.
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
		log.Printf("MFAWebAuthnVerify: błąd bazy danych: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	ceremony, ok := h.passkeyCeremonies.Take(req.Session)
	// Ceremonia musi należeć do tego samego konta, inaczej podpis z cudzego
	// wyzwania domykałby to.
	if !ok || ceremony.Discoverable || ceremony.UserID != user.ID {
		httpx.Fail(c, httpx.CodeWebAuthnChallengeInvalid)
		return
	}

	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(req.Credential))
	if err != nil {
		httpx.Fail(c, httpx.CodeWebAuthnVerificationFailed)
		return
	}

	stored, err := h.userCredentials(user.ID)
	if err != nil {
		log.Printf("MFAWebAuthnVerify: błąd bazy danych: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	credential, err := h.passkeys.ValidateLogin(passkey.NewUser(&user, stored), ceremony.Data, parsed)
	if err != nil {
		httpx.Fail(c, httpx.CodeWebAuthnVerificationFailed)
		return
	}

	if credential.Authenticator.CloneWarning {
		log.Printf("Ostrzeżenie: licznik podpisów klucza użytkownika %s sugeruje sklonowany authenticator", user.ID)
	}

	if err := h.markCredentialUsed(credential); err != nil {
		log.Printf("MFAWebAuthnVerify: nie udało się zaktualizować klucza: %v", err)
	}

	h.mfa.Consume(claims.ID)
	h.issueSession(c, &user, models.AMRPasswordKey, http.StatusOK)
}
