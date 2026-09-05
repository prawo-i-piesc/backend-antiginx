package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
		"https://attacker.invalid",
		"https://antiginx.pl.attacker.invalid",
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
			req.Header.Set("Origin", "https://attacker.invalid")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
		})
	}
}

func TestRefreshWithoutCookieReportsExpiredSession(t *testing.T) {
	w := httptest.NewRecorder()
	testRouter(t).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "SESSION_EXPIRED") {
		t.Errorf("body = %s, want kod SESSION_EXPIRED", w.Body.String())
	}
}

func TestSessionRoutesRejectForeignOrigin(t *testing.T) {
	r := testRouter(t)

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/auth/refresh"},
		{http.MethodPost, "/api/auth/logout"},
		{http.MethodDelete, "/api/auth/account"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, nil)
			req.Header.Set("Origin", "https://attacker.invalid")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
		})
	}
}

func TestLogoutRequiresAToken(t *testing.T) {
	w := httptest.NewRecorder()
	testRouter(t).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestOAuthStartWithoutConfiguredProvider(t *testing.T) {
	w := httptest.NewRecorder()
	testRouter(t).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/oauth/google/start", nil))

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	location := w.Header().Get("Location")
	if !strings.HasPrefix(location, frontendOrigin+"/login") {
		t.Errorf("Location = %q, powinno wracać na ekran logowania frontu", location)
	}
	if !strings.Contains(location, "OAUTH_PROVIDER_ERROR") {
		t.Errorf("Location = %q, brak kodu błędu", location)
	}
}

func TestOAuthCallbackWithoutStateRedirects(t *testing.T) {
	w := httptest.NewRecorder()
	testRouter(t).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/oauth/google/callback?code=x&state=y", nil))

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if location := w.Header().Get("Location"); !strings.Contains(location, "OAUTH_STATE_INVALID") {
		t.Errorf("Location = %q, want kod OAUTH_STATE_INVALID", location)
	}
}

func TestOAuthCallbackNeverReturnsJSON(t *testing.T) {
	w := httptest.NewRecorder()
	testRouter(t).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/oauth/google/callback", nil))

	if w.Body.Len() != 0 && strings.Contains(w.Header().Get("Content-Type"), "json") {
		t.Errorf("callback zwrócił JSON, a ma zawsze przekierowywać: %s", w.Body.String())
	}
}

func TestOAuthManagementRoutesRequireAToken(t *testing.T) {
	r := testRouter(t)

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/auth/oauth/google/link"},
		{http.MethodDelete, "/api/auth/oauth/google"},
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

func TestOAuthManagementRoutesRejectForeignOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/google/link", nil)
	req.Header.Set("Origin", "https://attacker.invalid")

	w := httptest.NewRecorder()
	testRouter(t).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestOAuthLinkRoutesWithoutPendingLink(t *testing.T) {
	r := testRouter(t)

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/auth/oauth-link/pending"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
			if !strings.Contains(w.Body.String(), "OAUTH_STATE_INVALID") {
				t.Errorf("body = %s, want kod OAUTH_STATE_INVALID", w.Body.String())
			}
		})
	}
}

func TestOAuthLinkRoutesRejectForeignOrigin(t *testing.T) {
	r := testRouter(t)

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/auth/oauth-link/pending"},
		{http.MethodPost, "/api/auth/oauth-link/confirm"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, nil)
			req.Header.Set("Origin", "https://attacker.invalid")

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
		{http.MethodDelete, "/api/auth/account"},
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
