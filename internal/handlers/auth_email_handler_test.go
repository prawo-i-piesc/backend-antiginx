package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/mail"
	"github.com/prawo-i-piesc/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type recordingMailer struct {
	sent chan mail.Message
}

func newRecordingMailer() *recordingMailer {
	return &recordingMailer{sent: make(chan mail.Message, 16)}
}

func (m *recordingMailer) Send(_ context.Context, msg mail.Message) error {
	m.sent <- msg
	return nil
}

func (m *recordingMailer) await(t *testing.T) mail.Message {
	t.Helper()

	select {
	case msg := <-m.sent:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("nie wysłano żadnej wiadomości")
		return mail.Message{}
	}
}

func (m *recordingMailer) expectSilence(t *testing.T) {
	t.Helper()

	select {
	case msg := <-m.sent:
		t.Fatalf("wysłano wiadomość, której nie powinno być: %q do %s", msg.Subject, msg.To)
	case <-time.After(200 * time.Millisecond):
	}
}

func mailHandler(t *testing.T, db *gorm.DB) (*AuthHandler, *recordingMailer) {
	t.Helper()

	h := passkeyHandler(t, db)
	recorder := newRecordingMailer()
	h.mailer = recorder
	return h, recorder
}

func mailRouter(h *AuthHandler, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.POST("/register", h.Register)
	r.POST("/email/verify", h.HandleEmailVerify)
	r.POST("/password/forgot", h.HandlePasswordForgot)
	r.POST("/password/reset", h.HandlePasswordReset)
	r.POST("/email/verify/request", func(c *gin.Context) {
		c.Set("userID", userID)
		c.Next()
	}, h.HandleEmailVerificationRequest)
	return r
}

func tokenFromLink(t *testing.T, msg mail.Message) string {
	t.Helper()

	start := strings.Index(msg.Text, "http")
	if start < 0 {
		t.Fatalf("wiadomość nie zawiera linku: %s", msg.Text)
	}

	link := strings.Fields(msg.Text[start:])[0]
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("link nie jest poprawnym URI: %v", err)
	}

	token := parsed.Query().Get("token")
	if token == "" {
		t.Fatalf("link bez tokenu: %s", link)
	}
	return token
}

func post(t *testing.T, r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRegisterSendsVerificationMail(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	r := mailRouter(h, "")

	w := post(t, r, "/register", `{"full_name":"Jan Kowalski","email":"Jan@Example.com","password":"prawidloweHaslo123"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	msg := recorder.await(t)
	if msg.To != "jan@example.com" {
		t.Errorf("wiadomość poszła na %q, a adres jest normalizowany", msg.To)
	}

	token := tokenFromLink(t, msg)

	var stored models.EmailVerificationToken
	if err := db.Where("token_hash = ?", auth.HashSecretToken(token)).First(&stored).Error; err != nil {
		t.Fatalf("token z linku nie istnieje w bazie: %v", err)
	}
	if string(stored.TokenHash) == token {
		t.Error("token zapisany w bazie w postaci jawnej")
	}
}

func TestEmailVerificationMarksAccount(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	r := mailRouter(h, "")

	post(t, r, "/register", `{"full_name":"Jan","email":"jan@example.com","password":"prawidloweHaslo123"}`)
	token := tokenFromLink(t, recorder.await(t))

	if w := post(t, r, "/email/verify", `{"token":"`+token+`"}`); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var user models.User
	if err := db.Where("email = ?", "jan@example.com").First(&user).Error; err != nil {
		t.Fatalf("odczyt użytkownika: %v", err)
	}
	if !user.EmailVerified {
		t.Error("adres nie został oznaczony jako potwierdzony")
	}
}

func TestEmailVerificationTokenIsSingleUse(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	r := mailRouter(h, "")

	post(t, r, "/register", `{"full_name":"Jan","email":"jan@example.com","password":"prawidloweHaslo123"}`)
	token := tokenFromLink(t, recorder.await(t))

	post(t, r, "/email/verify", `{"token":"`+token+`"}`)

	w := post(t, r, "/email/verify", `{"token":"`+token+`"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "TOKEN_INVALID") {
		t.Errorf("zużyty token przeszedł drugi raz: %d %s", w.Code, w.Body.String())
	}
}

func TestEmailVerificationRejectsUnknownAndExpiredTokens(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	r := mailRouter(h, "")

	if w := post(t, r, "/email/verify", `{"token":"nie-ma-takiego"}`); w.Code != http.StatusBadRequest {
		t.Errorf("nieznany token: status = %d", w.Code)
	}

	post(t, r, "/register", `{"full_name":"Jan","email":"jan@example.com","password":"prawidloweHaslo123"}`)
	token := tokenFromLink(t, recorder.await(t))

	if err := db.Model(&models.EmailVerificationToken{}).
		Where("token_hash = ?", auth.HashSecretToken(token)).
		Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("postarzenie tokenu: %v", err)
	}

	w := post(t, r, "/email/verify", `{"token":"`+token+`"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "TOKEN_EXPIRED") {
		t.Errorf("przeterminowany token: %d %s", w.Code, w.Body.String())
	}
}

// Token jest przypięty do adresu z chwili wystawienia, więc po zmianie adresu
// nie może potwierdzić nowego.
func TestEmailVerificationRejectsTokenIssuedForAnotherAddress(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	r := mailRouter(h, "")

	post(t, r, "/register", `{"full_name":"Jan","email":"jan@example.com","password":"prawidloweHaslo123"}`)
	token := tokenFromLink(t, recorder.await(t))

	if err := db.Model(&models.User{}).
		Where("email = ?", "jan@example.com").
		Update("email", "nowy@example.com").Error; err != nil {
		t.Fatalf("zmiana adresu: %v", err)
	}

	w := post(t, r, "/email/verify", `{"token":"`+token+`"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("token wystawiony na stary adres potwierdził nowy: %d %s", w.Code, w.Body.String())
	}
}

func seedPasswordUser(t *testing.T, db *gorm.DB, email, password string) *models.User {
	t.Helper()

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}

	user := &models.User{
		ID: newID(t), FullName: "Jan Kowalski", Email: email,
		Role: models.UserRoleUser, CreatedAt: time.Now(), Password: hashed,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("utworzenie użytkownika: %v", err)
	}
	return user
}

func TestPasswordForgotSendsLinkForExistingAccount(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	r := mailRouter(h, "")
	seedPasswordUser(t, db, "jan@example.com", "prawidloweHaslo123")

	w := post(t, r, "/password/forgot", `{"email":"JAN@example.com"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}

	msg := recorder.await(t)
	if msg.To != "jan@example.com" {
		t.Errorf("wiadomość poszła na %q", msg.To)
	}
	if !strings.Contains(msg.Text, "/reset-password?token=") {
		t.Errorf("link nie prowadzi na ekran resetu: %s", msg.Text)
	}
}

// Odpowiedź dla nieznanego adresu musi wyglądać identycznie, inaczej endpoint
// zdradza, które konta istnieją.
func TestPasswordForgotHidesUnknownAccounts(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	r := mailRouter(h, "")
	seedPasswordUser(t, db, "jan@example.com", "prawidloweHaslo123")

	known := post(t, r, "/password/forgot", `{"email":"jan@example.com"}`)
	recorder.await(t)

	unknown := post(t, r, "/password/forgot", `{"email":"nieznany@example.com"}`)
	recorder.expectSilence(t)

	if known.Code != unknown.Code {
		t.Errorf("statusy różne: %d i %d", known.Code, unknown.Code)
	}
	if known.Body.String() != unknown.Body.String() {
		t.Errorf("treści różne:\n%s\n%s", known.Body.String(), unknown.Body.String())
	}
}

func TestPasswordForgotIsRateLimitedPerAddress(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	r := mailRouter(h, "")
	seedPasswordUser(t, db, "jan@example.com", "prawidloweHaslo123")

	for i := 0; i < forgotPerAddress; i++ {
		post(t, r, "/password/forgot", `{"email":"jan@example.com"}`)
		recorder.await(t)
	}

	w := post(t, r, "/password/forgot", `{"email":"jan@example.com"}`)
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, a limit adresu nie może być widoczny dla klienta", w.Code)
	}
	recorder.expectSilence(t)
}

func TestPasswordResetChangesPasswordAndEndsSessions(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	r := mailRouter(h, "")
	user := seedPasswordUser(t, db, "jan@example.com", "prawidloweHaslo123")

	if _, _, err := h.sessions.Issue(user.ID, auth.SessionContext{AMR: models.AMRPassword}); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	post(t, r, "/password/forgot", `{"email":"jan@example.com"}`)
	token := tokenFromLink(t, recorder.await(t))

	w := post(t, r, "/password/reset", `{"token":"`+token+`","new_password":"zupelnieNoweHaslo1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var stored models.User
	if err := db.Where("id = ?", user.ID).First(&stored).Error; err != nil {
		t.Fatalf("odczyt użytkownika: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword(stored.Password, []byte("zupelnieNoweHaslo1")); err != nil {
		t.Error("nowe hasło nie działa")
	}
	if err := bcrypt.CompareHashAndPassword(stored.Password, []byte("prawidloweHaslo123")); err == nil {
		t.Error("stare hasło nadal działa")
	}

	var active int64
	if err := db.Model(&models.Session{}).
		Where("user_id = ? AND revoked_at IS NULL", user.ID).
		Count(&active).Error; err != nil {
		t.Fatalf("zliczenie sesji: %v", err)
	}
	if active != 0 {
		t.Errorf("po resecie hasła zostało %d aktywnych sesji", active)
	}
}

func TestPasswordResetTokenIsSingleUse(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	r := mailRouter(h, "")
	seedPasswordUser(t, db, "jan@example.com", "prawidloweHaslo123")

	post(t, r, "/password/forgot", `{"email":"jan@example.com"}`)
	token := tokenFromLink(t, recorder.await(t))

	post(t, r, "/password/reset", `{"token":"`+token+`","new_password":"zupelnieNoweHaslo1"}`)

	w := post(t, r, "/password/reset", `{"token":"`+token+`","new_password":"jeszczeInneHaslo22"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "TOKEN_INVALID") {
		t.Errorf("zużyty token przeszedł drugi raz: %d %s", w.Code, w.Body.String())
	}
}

func TestPasswordResetRejectsWeakPassword(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	r := mailRouter(h, "")
	seedPasswordUser(t, db, "jan@example.com", "prawidloweHaslo123")

	post(t, r, "/password/forgot", `{"email":"jan@example.com"}`)
	token := tokenFromLink(t, recorder.await(t))

	w := post(t, r, "/password/reset", `{"token":"`+token+`","new_password":"krotkie"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "PASSWORD_TOO_WEAK") {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	if w := post(t, r, "/password/reset", `{"token":"`+token+`","new_password":"zupelnieNoweHaslo1"}`); w.Code != http.StatusOK {
		t.Errorf("token przepadł po odrzuconym haśle: %d %s", w.Code, w.Body.String())
	}
}

// Nowe żądanie unieważnia poprzednie, żeby stary link z poczty przestał działać.
func TestPasswordForgotInvalidatesPreviousToken(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	r := mailRouter(h, "")
	seedPasswordUser(t, db, "jan@example.com", "prawidloweHaslo123")

	post(t, r, "/password/forgot", `{"email":"jan@example.com"}`)
	first := tokenFromLink(t, recorder.await(t))

	post(t, r, "/password/forgot", `{"email":"jan@example.com"}`)
	second := tokenFromLink(t, recorder.await(t))

	if w := post(t, r, "/password/reset", `{"token":"`+first+`","new_password":"zupelnieNoweHaslo1"}`); w.Code == http.StatusOK {
		t.Error("stary token nadal działa")
	}
	if w := post(t, r, "/password/reset", `{"token":"`+second+`","new_password":"zupelnieNoweHaslo1"}`); w.Code != http.StatusOK {
		t.Errorf("najnowszy token nie zadziałał: %d", w.Code)
	}
}

func TestVerificationRequestIsRateLimited(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	user := seedPasswordUser(t, db, "jan@example.com", "prawidloweHaslo123")
	r := mailRouter(h, user.ID.String())

	for i := 0; i < verificationPerUser; i++ {
		if w := post(t, r, "/email/verify/request", ""); w.Code != http.StatusNoContent {
			t.Fatalf("próba %d: status = %d", i+1, w.Code)
		}
		recorder.await(t)
	}

	w := post(t, r, "/email/verify/request", "")
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("brak nagłówka Retry-After")
	}
}

func TestVerificationRequestOnVerifiedAccountSendsNothing(t *testing.T) {
	db := testDB(t)
	h, recorder := mailHandler(t, db)
	user := seedPasswordUser(t, db, "jan@example.com", "prawidloweHaslo123")

	if err := db.Model(user).Update("email_verified", true).Error; err != nil {
		t.Fatalf("oznaczenie adresu: %v", err)
	}

	r := mailRouter(h, user.ID.String())

	if w := post(t, r, "/email/verify/request", ""); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
	recorder.expectSilence(t)
}
