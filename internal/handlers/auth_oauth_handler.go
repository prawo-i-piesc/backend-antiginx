package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/auth/oauth"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	oauthExchangeTimeout = 15 * time.Second
	loginPath            = "/login"
	mfaPath              = "/login/mfa"
	linkPath             = "/login/link"
)

type OAuthLinkConfirmRequest struct {
	Password string `json:"password" binding:"required"`
	Method   string `json:"method"`
	Code     string `json:"code"`
}

func (h *AuthHandler) HandleOAuthStart(c *gin.Context) {
	provider, ok := h.oauth.Get(c.Param("provider"))
	if !ok {
		h.oauthRedirectError(c, loginPath, httpx.CodeOAuthProviderError)
		return
	}

	id, flow, err := h.oauthState.Issue(provider.Name(), c.Query("next"), "")
	if err != nil {
		log.Printf("OAuthStart: nie udało się utworzyć stanu: %v", err)
		h.oauthRedirectError(c, loginPath, httpx.CodeInternal)
		return
	}

	auth.SetOAuthStateCookie(c, id, h.cfg.CookieSecure)
	c.Redirect(http.StatusFound, provider.AuthCodeURL(flow.State, flow.Verifier))
}

func (h *AuthHandler) HandleOAuthLink(c *gin.Context) {
	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	provider, ok := h.oauth.Get(c.Param("provider"))
	if !ok {
		httpx.Fail(c, httpx.CodeOAuthProviderError)
		return
	}

	id, flow, err := h.oauthState.Issue(provider.Name(), c.Query("next"), user.ID.String())
	if err != nil {
		log.Printf("OAuthLink: nie udało się utworzyć stanu: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	auth.SetOAuthStateCookie(c, id, h.cfg.CookieSecure)
	c.JSON(http.StatusOK, gin.H{"redirect_url": provider.AuthCodeURL(flow.State, flow.Verifier)})
}

func (h *AuthHandler) HandleOAuthCallback(c *gin.Context) {
	flow, ok := h.oauthState.Consume(auth.OAuthStateCookie(c))
	if !ok {
		h.oauthRedirectError(c, loginPath, httpx.CodeOAuthStateInvalid)
		return
	}

	failurePath := loginPath
	if flow.IsLink() {
		failurePath = flow.Next
	}

	if flow.Provider != c.Param("provider") || !flow.MatchesState(c.Query("state")) {
		h.oauthRedirectError(c, failurePath, httpx.CodeOAuthStateInvalid)
		return
	}

	if c.Query("error") != "" || c.Query("code") == "" {
		h.oauthRedirectError(c, failurePath, httpx.CodeOAuthProviderError)
		return
	}

	provider, ok := h.oauth.Get(flow.Provider)
	if !ok {
		h.oauthRedirectError(c, failurePath, httpx.CodeOAuthProviderError)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), oauthExchangeTimeout)
	defer cancel()

	profile, err := provider.Exchange(ctx, c.Query("code"), flow.Verifier)
	if err != nil {
		log.Printf("OAuthCallback: wymiana kodu u %s nie powiodła się: %v", flow.Provider, err)
		h.oauthRedirectError(c, failurePath, httpx.CodeOAuthProviderError)
		return
	}

	if !profile.EmailVerified {
		h.oauthRedirectError(c, failurePath, httpx.CodeOAuthEmailUnverified)
		return
	}

	email := auth.NormalizeEmail(profile.Email)

	if flow.IsLink() {
		h.completeOAuthLink(c, flow, profile, email)
		return
	}

	user, needsConfirmation, failure := h.resolveOAuthUser(flow.Provider, profile, email)
	if failure != "" {
		h.oauthRedirectError(c, failurePath, failure)
		return
	}

	if needsConfirmation {
		h.beginPendingLink(c, flow, profile, email, user)
		return
	}

	if user.TOTPEnabled() {
		h.redirectToMFA(c, user, flow.Next)
		return
	}

	if err := h.startSession(c, user, models.AMROAuth); err != nil {
		log.Printf("OAuthCallback: nie udało się utworzyć sesji: %v", err)
		h.oauthRedirectError(c, failurePath, httpx.CodeInternal)
		return
	}

	c.Redirect(http.StatusFound, h.cfg.PublicBaseURL+flow.Next)
}

func (h *AuthHandler) resolveOAuthUser(provider string, profile *oauth.Profile, email string) (*models.User, bool, httpx.Code) {
	var account models.OAuthAccount
	err := h.db.Where("provider = ? AND provider_user_id = ?", provider, profile.Subject).First(&account).Error
	switch {
	case err == nil:
		var user models.User
		if err := h.db.Where("id = ?", account.UserID).First(&user).Error; err != nil {
			log.Printf("resolveOAuthUser: powiązane konto wskazuje na nieistniejącego użytkownika: %v", err)
			return nil, false, httpx.CodeInternal
		}
		return &user, false, ""
	case !errors.Is(err, gorm.ErrRecordNotFound):
		log.Printf("resolveOAuthUser: błąd bazy danych: %v", err)
		return nil, false, httpx.CodeInternal
	}

	var existing models.User
	err = byEmail(h.db, email).First(&existing).Error
	switch {
	case err == nil:
		return &existing, true, ""
	case !errors.Is(err, gorm.ErrRecordNotFound):
		log.Printf("resolveOAuthUser: błąd bazy danych: %v", err)
		return nil, false, httpx.CodeInternal
	}

	user, err := h.createOAuthUser(provider, profile, email)
	if err != nil {
		log.Printf("resolveOAuthUser: nie udało się utworzyć konta: %v", err)
		return nil, false, httpx.CodeInternal
	}
	return user, false, ""
}

func (h *AuthHandler) beginPendingLink(c *gin.Context, flow *oauth.Flow, profile *oauth.Profile, email string, user *models.User) {
	id, err := h.oauthPending.Issue(&oauth.PendingLink{
		Provider: flow.Provider,
		Subject:  profile.Subject,
		Email:    email,
		UserID:   user.ID.String(),
		Next:     flow.Next,
	})
	if err != nil {
		log.Printf("beginPendingLink: nie udało się zapisać oczekującego powiązania: %v", err)
		h.oauthRedirectError(c, loginPath, httpx.CodeInternal)
		return
	}

	auth.SetOAuthStateCookie(c, id, h.cfg.CookieSecure)
	c.Redirect(http.StatusFound, h.cfg.PublicBaseURL+linkPath)
}

func (h *AuthHandler) HandleOAuthLinkPending(c *gin.Context) {
	link, ok := h.oauthPending.Get(auth.OAuthStateCookie(c))
	if !ok {
		httpx.Fail(c, httpx.CodeOAuthStateInvalid)
		return
	}

	var user models.User
	if err := h.db.Where("id = ?", link.UserID).First(&user).Error; err != nil {
		httpx.Fail(c, httpx.CodeOAuthStateInvalid)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"provider":      link.Provider,
		"email":         link.Email,
		"password_set":  user.HasPassword(),
		"totp_required": user.TOTPEnabled(),
		"expires_in":    int(time.Until(link.ExpiresAt).Seconds()),
	})
}

func (h *AuthHandler) HandleOAuthLinkConfirm(c *gin.Context) {
	var req OAuthLinkConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	id := auth.OAuthStateCookie(c)

	link, err := h.oauthPending.Attempt(id)
	switch {
	case errors.Is(err, oauth.ErrTooManyAttempts):
		httpx.Fail(c, httpx.CodeRateLimited)
		return
	case err != nil:
		httpx.Fail(c, httpx.CodeOAuthStateInvalid)
		return
	}

	var user models.User
	if err := h.db.Where("id = ?", link.UserID).First(&user).Error; err != nil {
		httpx.Fail(c, httpx.CodeOAuthStateInvalid)
		return
	}

	if !user.HasPassword() {
		httpx.Fail(c, httpx.CodeOAuthAccountConflict)
		return
	}

	if err := bcrypt.CompareHashAndPassword(user.Password, []byte(req.Password)); err != nil {
		httpx.Fail(c, httpx.CodeInvalidCredentials)
		return
	}

	amr := models.AMRPassword

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
		amr = models.AMRPasswordOTP
	}

	if err := h.attachOAuthAccount(&user, link.Provider, &oauth.Profile{Subject: link.Subject}, link.Email, true); err != nil {
		log.Printf("OAuthLinkConfirm: nie udało się powiązać konta: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	h.oauthPending.Consume(id)
	h.issueSession(c, &user, amr, http.StatusOK)
}

func (h *AuthHandler) createOAuthUser(provider string, profile *oauth.Profile, email string) (*models.User, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}

	fullName := profile.FullName
	if fullName == "" {
		fullName = email
	}

	user := &models.User{
		ID:            id,
		FullName:      fullName,
		Email:         email,
		Role:          models.UserRoleUser,
		CreatedAt:     time.Now(),
		EmailVerified: true,
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		return createOAuthAccount(tx, user.ID, provider, profile.Subject, email)
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (h *AuthHandler) attachOAuthAccount(user *models.User, provider string, profile *oauth.Profile, email string, markVerified bool) error {
	return h.db.Transaction(func(tx *gorm.DB) error {
		if err := createOAuthAccount(tx, user.ID, provider, profile.Subject, email); err != nil {
			return err
		}
		if !markVerified || user.EmailVerified {
			return nil
		}
		if err := tx.Model(user).Update("email_verified", true).Error; err != nil {
			return err
		}
		user.EmailVerified = true
		return nil
	})
}

func createOAuthAccount(tx *gorm.DB, userID uuid.UUID, provider, subject, email string) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	return tx.Create(&models.OAuthAccount{
		ID:             id,
		UserID:         userID,
		Provider:       provider,
		ProviderUserID: subject,
		Email:          email,
		CreatedAt:      time.Now(),
	}).Error
}

func (h *AuthHandler) completeOAuthLink(c *gin.Context, flow *oauth.Flow, profile *oauth.Profile, email string) {
	userID, err := uuid.Parse(flow.UserID)
	if err != nil {
		h.oauthRedirectError(c, flow.Next, httpx.CodeInternal)
		return
	}

	var user models.User
	if err := h.db.Where("id = ?", userID).First(&user).Error; err != nil {
		h.oauthRedirectError(c, flow.Next, httpx.CodeSessionExpired)
		return
	}

	var account models.OAuthAccount
	err = h.db.Where("provider = ? AND provider_user_id = ?", flow.Provider, profile.Subject).First(&account).Error
	switch {
	case err == nil:
		if account.UserID != user.ID {
			h.oauthRedirectError(c, flow.Next, httpx.CodeProviderAlreadyLinked)
			return
		}
		c.Redirect(http.StatusFound, h.cfg.PublicBaseURL+flow.Next)
		return
	case !errors.Is(err, gorm.ErrRecordNotFound):
		log.Printf("completeOAuthLink: błąd bazy danych: %v", err)
		h.oauthRedirectError(c, flow.Next, httpx.CodeInternal)
		return
	}

	if err := h.attachOAuthAccount(&user, flow.Provider, profile, email, false); err != nil {
		log.Printf("completeOAuthLink: nie udało się powiązać konta: %v", err)
		h.oauthRedirectError(c, flow.Next, httpx.CodeInternal)
		return
	}

	c.Redirect(http.StatusFound, h.cfg.PublicBaseURL+flow.Next)
}

func (h *AuthHandler) HandleOAuthUnlink(c *gin.Context) {
	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	provider := c.Param("provider")

	var account models.OAuthAccount
	if err := h.db.Where("user_id = ? AND provider = ?", user.ID, provider).First(&account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpx.Fail(c, httpx.CodeCredentialNotFound)
			return
		}
		log.Printf("OAuthUnlink: błąd bazy danych: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	remaining, err := h.countLoginMethodsExcluding(&user, provider)
	if err != nil {
		log.Printf("OAuthUnlink: nie udało się policzyć metod logowania: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	if remaining == 0 {
		httpx.Fail(c, httpx.CodeLastLoginMethod)
		return
	}

	if err := h.db.Delete(&account).Error; err != nil {
		log.Printf("OAuthUnlink: nie udało się odpiąć konta: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) countLoginMethodsExcluding(user *models.User, provider string) (int64, error) {
	var others int64
	if err := h.db.Model(&models.OAuthAccount{}).
		Where("user_id = ? AND provider <> ?", user.ID, provider).
		Count(&others).Error; err != nil {
		return 0, err
	}

	if user.HasPassword() {
		others++
	}
	return others, nil
}

func (h *AuthHandler) userProviders(userID uuid.UUID) []string {
	providers := []string{}
	if err := h.db.Model(&models.OAuthAccount{}).
		Where("user_id = ?", userID).
		Order("provider").
		Pluck("provider", &providers).Error; err != nil {
		log.Printf("Nie udało się pobrać dostawców użytkownika %s: %v", userID, err)
		return []string{}
	}
	return providers
}

func (h *AuthHandler) redirectToMFA(c *gin.Context, user *models.User, next string) {
	token, id, err := auth.GenerateTokenWithID(h.cfg.JWTSecret, auth.TokenTypeMFA, user.ID.String(), user.Role, auth.MFATokenTTL)
	if err != nil {
		log.Printf("OAuthCallback: nie udało się wygenerować tokenu MFA: %v", err)
		h.oauthRedirectError(c, loginPath, httpx.CodeInternal)
		return
	}

	h.mfa.Issue(id, user.ID.String())

	target := h.cfg.PublicBaseURL + mfaPath + "?token=" + url.QueryEscape(token)
	if next != "" {
		target += "&next=" + url.QueryEscape(next)
	}
	c.Redirect(http.StatusFound, target)
}

func (h *AuthHandler) oauthRedirectError(c *gin.Context, path string, code httpx.Code) {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	c.Redirect(http.StatusFound, h.cfg.PublicBaseURL+path+separator+"error="+url.QueryEscape(string(code)))
}
