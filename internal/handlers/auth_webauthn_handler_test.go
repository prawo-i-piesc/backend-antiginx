package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/config"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

const (
	testOrigin = "http://localhost:3000"
	testRPID   = "localhost"
)

func passkeyConfig() *config.Config {
	return &config.Config{
		JWTSecret:       []byte("test-secret-that-is-long-enough-for-hs256"),
		PublicBaseURL:   testOrigin,
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: time.Hour,
		WebAuthnRPID:    testRPID,
		WebAuthnRPName:  config.DefaultWebAuthnRPName,
	}
}

func passkeyHandler(t *testing.T, db *gorm.DB) *AuthHandler {
	t.Helper()

	h, err := NewAuthHandler(db, passkeyConfig())
	if err != nil {
		t.Fatalf("NewAuthHandler: %v", err)
	}
	return h
}

func passkeyRouter(h *AuthHandler, userID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	authenticated := func(c *gin.Context) {
		c.Set("userID", userID.String())
		c.Next()
	}

	r.POST("/register/options", authenticated, h.HandleWebAuthnRegisterOptions)
	r.POST("/register/verify", authenticated, h.HandleWebAuthnRegisterVerify)
	r.POST("/login/options", h.HandleWebAuthnLoginOptions)
	r.POST("/login/verify", h.HandleWebAuthnLoginVerify)
	r.GET("/credentials", authenticated, h.HandleWebAuthnCredentials)
	r.DELETE("/credentials/:id", authenticated, h.HandleWebAuthnDeleteCredential)
	return r
}

func call(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func seedPasskeyUser(t *testing.T, db *gorm.DB) *models.User {
	t.Helper()

	user := &models.User{
		ID:        newID(t),
		FullName:  "Jan Kowalski",
		Email:     "jan@example.com",
		Role:      models.UserRoleUser,
		CreatedAt: time.Now(),
		Password:  []byte("nieistotny-hasz"),
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("utworzenie użytkownika: %v", err)
	}
	return user
}

// registerPasskey przechodzi pełną ceremonię rejestracji na wirtualnym
// authenticatorze i zwraca dane potrzebne do późniejszego logowania.
func registerPasskey(t *testing.T, r *gin.Engine, user *models.User, name string) (virtualwebauthn.RelyingParty, virtualwebauthn.Authenticator, virtualwebauthn.Credential) {
	t.Helper()

	rp := virtualwebauthn.RelyingParty{Name: config.DefaultWebAuthnRPName, ID: testRPID, Origin: testOrigin}
	authenticator := virtualwebauthn.NewAuthenticatorWithOptions(virtualwebauthn.AuthenticatorOptions{
		UserHandle: user.ID[:],
	})
	credential := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)

	options := call(t, r, http.MethodPost, "/register/options", "")
	if options.Code != http.StatusOK {
		t.Fatalf("register/options: status = %d, body = %s", options.Code, options.Body.String())
	}

	parsed, err := virtualwebauthn.ParseAttestationOptions(options.Body.String())
	if err != nil {
		t.Fatalf("ParseAttestationOptions: %v", err)
	}

	attestation := virtualwebauthn.CreateAttestationResponse(rp, authenticator, credential, *parsed)

	verify := call(t, r, http.MethodPost, "/register/verify",
		fmt.Sprintf(`{"credential": %s, "name": %q}`, attestation, name))
	if verify.Code != http.StatusCreated {
		t.Fatalf("register/verify: status = %d, body = %s", verify.Code, verify.Body.String())
	}

	authenticator.AddCredential(credential)
	return rp, authenticator, credential
}

func TestPasskeyRegistrationStoresCredential(t *testing.T) {
	db := testDB(t)
	user := seedPasskeyUser(t, db)
	r := passkeyRouter(passkeyHandler(t, db), user.ID)

	registerPasskey(t, r, user, "MacBook")

	var stored []models.WebAuthnCredential
	if err := db.Where("user_id = ?", user.ID).Find(&stored).Error; err != nil {
		t.Fatalf("odczyt kluczy: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("zapisano %d kluczy, oczekiwano 1", len(stored))
	}
	if stored[0].Name != "MacBook" {
		t.Errorf("Name = %q, want %q", stored[0].Name, "MacBook")
	}
	if len(stored[0].PublicKey) == 0 || len(stored[0].CredentialID) == 0 {
		t.Error("klucz zapisany bez identyfikatora albo klucza publicznego")
	}
	if stored[0].LastUsedAt != nil {
		t.Error("świeżo zarejestrowany klucz ma ustawioną datę użycia")
	}
}

func TestPasskeyLoginWithEmail(t *testing.T) {
	db := testDB(t)
	user := seedPasskeyUser(t, db)
	r := passkeyRouter(passkeyHandler(t, db), user.ID)

	rp, authenticator, credential := registerPasskey(t, r, user, "MacBook")

	options := call(t, r, http.MethodPost, "/login/options", `{"email":"jan@example.com"}`)
	if options.Code != http.StatusOK {
		t.Fatalf("login/options: status = %d, body = %s", options.Code, options.Body.String())
	}

	var optionsBody struct {
		Session string `json:"webauthn_session"`
	}
	if err := json.Unmarshal(options.Body.Bytes(), &optionsBody); err != nil {
		t.Fatalf("odpowiedź nie jest poprawnym JSON-em: %v", err)
	}
	if optionsBody.Session == "" {
		t.Fatal("brak webauthn_session w odpowiedzi")
	}

	parsed, err := virtualwebauthn.ParseAssertionOptions(options.Body.String())
	if err != nil {
		t.Fatalf("ParseAssertionOptions: %v", err)
	}

	assertion := virtualwebauthn.CreateAssertionResponse(rp, authenticator, credential, *parsed)

	verify := call(t, r, http.MethodPost, "/login/verify",
		fmt.Sprintf(`{"webauthn_session": %q, "credential": %s}`, optionsBody.Session, assertion))
	if verify.Code != http.StatusOK {
		t.Fatalf("login/verify: status = %d, body = %s", verify.Code, verify.Body.String())
	}

	var sessionBody struct {
		AccessToken string `json:"access_token"`
		User        struct {
			Email string `json:"email"`
		} `json:"user"`
	}
	if err := json.Unmarshal(verify.Body.Bytes(), &sessionBody); err != nil {
		t.Fatalf("odpowiedź nie jest poprawnym JSON-em: %v", err)
	}
	if sessionBody.AccessToken == "" {
		t.Error("logowanie passkeyem nie zwróciło tokenu dostępu")
	}
	if sessionBody.User.Email != user.Email {
		t.Errorf("zalogowano jako %q, oczekiwano %q", sessionBody.User.Email, user.Email)
	}

	if cookies := verify.Result().Cookies(); len(cookies) != 1 {
		t.Errorf("ustawiono %d ciasteczek, oczekiwano dokładnie jednego", len(cookies))
	}

	var session models.Session
	if err := db.Where("user_id = ?", user.ID).First(&session).Error; err != nil {
		t.Fatalf("sesja nie powstała: %v", err)
	}
	if session.AMR != models.AMRWebAuthn {
		t.Errorf("AMR = %q, want %q", session.AMR, models.AMRWebAuthn)
	}

	var stored models.WebAuthnCredential
	if err := db.Where("user_id = ?", user.ID).First(&stored).Error; err != nil {
		t.Fatalf("odczyt klucza: %v", err)
	}
	if stored.LastUsedAt == nil {
		t.Error("po zalogowaniu nie zapisano daty użycia klucza")
	}
}

// Logowanie bez podanego adresu korzysta z discoverable credentials, czyli
// authenticator sam wskazuje, o które konto chodzi.
func TestPasskeyDiscoverableLogin(t *testing.T) {
	db := testDB(t)
	user := seedPasskeyUser(t, db)
	r := passkeyRouter(passkeyHandler(t, db), user.ID)

	rp, authenticator, credential := registerPasskey(t, r, user, "MacBook")

	options := call(t, r, http.MethodPost, "/login/options", "{}")
	if options.Code != http.StatusOK {
		t.Fatalf("login/options: status = %d, body = %s", options.Code, options.Body.String())
	}

	var optionsBody struct {
		Session string `json:"webauthn_session"`
	}
	if err := json.Unmarshal(options.Body.Bytes(), &optionsBody); err != nil {
		t.Fatalf("odpowiedź nie jest poprawnym JSON-em: %v", err)
	}

	parsed, err := virtualwebauthn.ParseAssertionOptions(options.Body.String())
	if err != nil {
		t.Fatalf("ParseAssertionOptions: %v", err)
	}

	assertion := virtualwebauthn.CreateAssertionResponse(rp, authenticator, credential, *parsed)

	verify := call(t, r, http.MethodPost, "/login/verify",
		fmt.Sprintf(`{"webauthn_session": %q, "credential": %s}`, optionsBody.Session, assertion))
	if verify.Code != http.StatusOK {
		t.Fatalf("login/verify: status = %d, body = %s", verify.Code, verify.Body.String())
	}
}

// Nieznany adres nie może dawać innej odpowiedzi niż znany, bo endpoint jest
// publiczny i zdradzałby, które konta istnieją.
func TestPasskeyLoginOptionsHidesUnknownAccounts(t *testing.T) {
	db := testDB(t)
	user := seedPasskeyUser(t, db)
	r := passkeyRouter(passkeyHandler(t, db), user.ID)

	known := call(t, r, http.MethodPost, "/login/options", `{"email":"jan@example.com"}`)
	unknown := call(t, r, http.MethodPost, "/login/options", `{"email":"nieznany@example.com"}`)

	if known.Code != http.StatusOK || unknown.Code != http.StatusOK {
		t.Fatalf("statusy: znany = %d, nieznany = %d", known.Code, unknown.Code)
	}
}

func TestPasskeyLoginVerifyRejectsUnknownSession(t *testing.T) {
	db := testDB(t)
	user := seedPasskeyUser(t, db)
	r := passkeyRouter(passkeyHandler(t, db), user.ID)

	verify := call(t, r, http.MethodPost, "/login/verify",
		`{"webauthn_session": "nie-ma-takiej", "credential": {}}`)

	if verify.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", verify.Code)
	}
	if !strings.Contains(verify.Body.String(), "WEBAUTHN_CHALLENGE_INVALID") {
		t.Errorf("body = %s", verify.Body.String())
	}
}

func TestPasskeyCeremonyIsSingleUse(t *testing.T) {
	db := testDB(t)
	user := seedPasskeyUser(t, db)
	r := passkeyRouter(passkeyHandler(t, db), user.ID)

	rp, authenticator, credential := registerPasskey(t, r, user, "MacBook")

	options := call(t, r, http.MethodPost, "/login/options", `{"email":"jan@example.com"}`)
	var optionsBody struct {
		Session string `json:"webauthn_session"`
	}
	if err := json.Unmarshal(options.Body.Bytes(), &optionsBody); err != nil {
		t.Fatalf("odpowiedź nie jest poprawnym JSON-em: %v", err)
	}

	parsed, err := virtualwebauthn.ParseAssertionOptions(options.Body.String())
	if err != nil {
		t.Fatalf("ParseAssertionOptions: %v", err)
	}
	assertion := virtualwebauthn.CreateAssertionResponse(rp, authenticator, credential, *parsed)
	body := fmt.Sprintf(`{"webauthn_session": %q, "credential": %s}`, optionsBody.Session, assertion)

	if first := call(t, r, http.MethodPost, "/login/verify", body); first.Code != http.StatusOK {
		t.Fatalf("pierwsze logowanie: status = %d, body = %s", first.Code, first.Body.String())
	}

	second := call(t, r, http.MethodPost, "/login/verify", body)
	if second.Code == http.StatusOK {
		t.Error("ta sama ceremonia zadziałała drugi raz")
	}
}

func TestPasskeyListAndDelete(t *testing.T) {
	db := testDB(t)
	user := seedPasskeyUser(t, db)
	r := passkeyRouter(passkeyHandler(t, db), user.ID)

	registerPasskey(t, r, user, "MacBook")

	list := call(t, r, http.MethodGet, "/credentials", "")
	if list.Code != http.StatusOK {
		t.Fatalf("credentials: status = %d", list.Code)
	}

	var credentials []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &credentials); err != nil {
		t.Fatalf("odpowiedź nie jest poprawnym JSON-em: %v", err)
	}
	if len(credentials) != 1 || credentials[0].Name != "MacBook" {
		t.Fatalf("lista kluczy: %+v", credentials)
	}

	// Konto ma hasło, więc passkey nie jest ostatnią metodą logowania.
	deleted := call(t, r, http.MethodDelete, "/credentials/"+credentials[0].ID, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("usunięcie klucza: status = %d, body = %s", deleted.Code, deleted.Body.String())
	}

	var remaining int64
	if err := db.Model(&models.WebAuthnCredential{}).Where("user_id = ?", user.ID).Count(&remaining).Error; err != nil {
		t.Fatalf("zliczenie kluczy: %v", err)
	}
	if remaining != 0 {
		t.Errorf("zostało %d kluczy", remaining)
	}
}

func TestPasskeyDeleteRefusesLastLoginMethod(t *testing.T) {
	db := testDB(t)
	user := seedPasskeyUser(t, db)

	// Konto bez hasła, czyli passkey jest jedyną metodą wejścia.
	if err := db.Model(user).Updates(map[string]any{"password": nil}).Error; err != nil {
		t.Fatalf("wyczyszczenie hasła: %v", err)
	}

	r := passkeyRouter(passkeyHandler(t, db), user.ID)
	registerPasskey(t, r, user, "MacBook")

	list := call(t, r, http.MethodGet, "/credentials", "")
	var credentials []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &credentials); err != nil {
		t.Fatalf("odpowiedź nie jest poprawnym JSON-em: %v", err)
	}

	deleted := call(t, r, http.MethodDelete, "/credentials/"+credentials[0].ID, "")
	if deleted.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", deleted.Code)
	}
	if !strings.Contains(deleted.Body.String(), "LAST_LOGIN_METHOD") {
		t.Errorf("body = %s", deleted.Body.String())
	}
}

func TestPasskeyDeleteUnknownCredential(t *testing.T) {
	db := testDB(t)
	user := seedPasskeyUser(t, db)
	r := passkeyRouter(passkeyHandler(t, db), user.ID)

	for _, id := range []string{uuid.NewString(), "nie-uuid"} {
		w := call(t, r, http.MethodDelete, "/credentials/"+id, "")
		if w.Code != http.StatusNotFound {
			t.Errorf("dla %q status = %d, want 404", id, w.Code)
		}
	}
}
