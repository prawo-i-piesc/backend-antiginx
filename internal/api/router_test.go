package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/config"
	"github.com/prawo-i-piesc/backend/internal/handlers"
)

const frontendOrigin = "https://antiginx.pl"

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

func testRouter(t *testing.T) *gin.Engine {
	t.Helper()

	cfg := &config.Config{
		JWTSecret:     []byte("test-secret-that-is-long-enough-for-hs256"),
		PublicBaseURL: frontendOrigin,
	}
	return NewRouter(
		handlers.NewScanHandler(nil, nil),
		handlers.NewAuthHandler(nil, cfg),
		handlers.NewAdminHandler(nil),
		cfg,
	)
}

func preflight(t *testing.T, r *gin.Engine, origin string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCORSAllowsTheFrontendOrigin(t *testing.T) {
	w := preflight(t, testRouter(t), frontendOrigin)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != frontendOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, frontendOrigin)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want %q", got, "true")
	}
}

func TestCORSRejectsForeignOrigins(t *testing.T) {
	r := testRouter(t)

	for _, origin := range []string{
		"https://evil.pl",
		"https://antiginx.pl.evil.pl",
		"http://antiginx.pl",
		"null",
	} {
		t.Run(origin, func(t *testing.T) {
			w := preflight(t, r, origin)
			if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("Access-Control-Allow-Origin = %q, want no header", got)
			}
		})
	}
}

func TestMFARoutesRequireAToken(t *testing.T) {
	r := testRouter(t)

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/auth/mfa/totp/enroll"},
		{http.MethodPost, "/api/auth/mfa/totp/activate"},
		{http.MethodDelete, "/api/auth/mfa/totp"},
		{http.MethodPost, "/api/auth/mfa/recovery-codes/regenerate"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))

			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
		})
	}
}

func TestMFARoutesRejectForeignOrigin(t *testing.T) {
	r := testRouter(t)

	for _, path := range []string{
		"/api/auth/mfa/verify",
		"/api/auth/mfa/totp/enroll",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			req.Header.Set("Origin", "https://evil.pl")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
		})
	}
}

func TestProtectedRoutesRequireAToken(t *testing.T) {
	r := testRouter(t)

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/auth/me"},
		{http.MethodGet, "/api/users/scans"},
		{http.MethodGet, "/api/admin/widgets"},
		{http.MethodPatch, "/api/utils/profile/email"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))

			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
		})
	}
}
