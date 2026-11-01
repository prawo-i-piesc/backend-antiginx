package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/billing"
	"github.com/prawo-i-piesc/backend/internal/config"
	"github.com/prawo-i-piesc/backend/internal/handlers"
	"github.com/prawo-i-piesc/backend/internal/models"
	"github.com/prawo-i-piesc/backend/internal/monitoring"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const frontendOrigin = "https://antiginx.pl"

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

func testConfig() *config.Config {
	return &config.Config{
		JWTSecret:      []byte("test-secret-that-is-long-enough-for-hs256"),
		PublicBaseURL:  frontendOrigin,
		WebAuthnRPID:   "antiginx.pl",
		WebAuthnRPName: config.DefaultWebAuthnRPName,
	}
}

func testRouter(t *testing.T) *gin.Engine {
	t.Helper()
	return routerWithConfig(t, testConfig())
}

func routerWithConfig(t *testing.T, cfg *config.Config) *gin.Engine {
	t.Helper()
	return routerWithProbes(t, cfg, nil)
}

func routerWithProbes(t *testing.T, cfg *config.Config, probes []monitoring.Probe) *gin.Engine {
	t.Helper()

	billingService := billing.New(nil, nil, "", "")
	authHandler, err := handlers.NewAuthHandler(nil, cfg, billingService)
	if err != nil {
		t.Fatalf("NewAuthHandler: %v", err)
	}

	return NewRouter(
		handlers.NewScanHandler(nil, nil),
		authHandler,
		handlers.NewAdminHandler(nil, nil, billingService),
		handlers.NewBillingHandler(nil, billingService, ""),
		handlers.NewDashboardHandler(nil),
		handlers.NewSystemHandler(nil, probes, nil, ""),
		cfg,
		probes,
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

func TestPreferenceWritesRejectForeignOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/api/users/preferences/dashboard.widgets", nil)
	req.Header.Set("Origin", "https://attacker.invalid")

	w := httptest.NewRecorder()
	testRouter(t).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
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

func TestOAuthRoutesServeBothProviders(t *testing.T) {
	r := testRouter(t)

	for _, provider := range []string{"google", "github"} {
		t.Run(provider, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/oauth/"+provider+"/start", nil))

			if w.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302", w.Code)
			}
			if location := w.Header().Get("Location"); !strings.HasPrefix(location, frontendOrigin) {
				t.Errorf("Location = %q", location)
			}
		})
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

func TestWebAuthnRoutesRequireAToken(t *testing.T) {
	r := testRouter(t)

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/auth/webauthn/register/options"},
		{http.MethodPost, "/api/auth/webauthn/register/verify"},
		{http.MethodGet, "/api/auth/webauthn/credentials"},
		{http.MethodDelete, "/api/auth/webauthn/credentials/" + uuid.NewString()},
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

func TestWebAuthnLoginRoutesArePublic(t *testing.T) {
	r := testRouter(t)

	for _, path := range []string{
		"/api/auth/webauthn/login/options",
		"/api/auth/webauthn/login/verify",
	} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))

			if w.Code == http.StatusUnauthorized {
				t.Error("logowanie passkeyem wymaga tokenu, a musi być publiczne")
			}
			if w.Code == http.StatusNotFound {
				t.Error("trasa nie jest zarejestrowana")
			}
		})
	}
}

func TestWebAuthnRoutesRejectForeignOrigin(t *testing.T) {
	r := testRouter(t)

	for _, path := range []string{
		"/api/auth/webauthn/login/options",
		"/api/auth/webauthn/register/options",
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

func TestMailRoutesArePublic(t *testing.T) {
	r := testRouter(t)

	for _, path := range []string{
		"/api/auth/email/verify",
		"/api/auth/password/forgot",
		"/api/auth/password/reset",
	} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))

			if w.Code == http.StatusUnauthorized {
				t.Error("trasa wymaga tokenu, a musi być publiczna")
			}
			if w.Code == http.StatusNotFound {
				t.Error("trasa nie jest zarejestrowana")
			}
		})
	}
}

func TestVerificationRequestRequiresAToken(t *testing.T) {
	w := httptest.NewRecorder()
	testRouter(t).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/auth/email/verify/request", nil))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestMailRoutesRejectForeignOrigin(t *testing.T) {
	r := testRouter(t)

	for _, path := range []string{
		"/api/auth/email/verify",
		"/api/auth/password/forgot",
		"/api/auth/password/reset",
		"/api/auth/email/verify/request",
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

func TestProtectedRoutesRequireAToken(t *testing.T) {
	r := testRouter(t)

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/auth/me"},
		{http.MethodDelete, "/api/auth/account"},
		{http.MethodGet, "/api/users/scans"},
		{http.MethodGet, "/api/users/stats/summary"},
		{http.MethodGet, "/api/users/stats/risk"},
		{http.MethodGet, "/api/users/stats/severity"},
		{http.MethodGet, "/api/users/stats/tests"},
		{http.MethodGet, "/api/users/stats/domains"},
		{http.MethodGet, "/api/users/preferences/dashboard.widgets"},
		{http.MethodPut, "/api/users/preferences/dashboard.widgets"},
		{http.MethodGet, "/api/scans/" + uuid.NewString() + "/report"},
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

func TestContentSecurityPolicyHasNoWildcards(t *testing.T) {
	w := httptest.NewRecorder()
	testRouter(t).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	csp := w.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "*") || strings.Contains(csp, "unsafe-inline") {
		t.Errorf("Content-Security-Policy = %q, want no wildcards and no unsafe-inline", csp)
	}
	if !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("Content-Security-Policy = %q, want default-src 'none'", csp)
	}
}

func TestStrictTransportSecurityFollowsCookieSecure(t *testing.T) {
	for _, tc := range []struct {
		secure bool
		want   bool
	}{{secure: true, want: true}, {secure: false, want: false}} {
		cfg := testConfig()
		cfg.CookieSecure = tc.secure

		w := httptest.NewRecorder()
		routerWithConfig(t, cfg).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/health", nil))

		if got := w.Header().Get("Strict-Transport-Security") != ""; got != tc.want {
			t.Errorf("CookieSecure=%v: HSTS present = %v, want %v", tc.secure, got, tc.want)
		}
	}
}

func TestResponsesExposeRetryAfterAndContentDisposition(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Origin", frontendOrigin)

	w := httptest.NewRecorder()
	testRouter(t).ServeHTTP(w, req)

	// Content-Disposition niesie nazwę pliku pobieranego raportu.
	for _, header := range []string{"Retry-After", "Content-Disposition"} {
		if got := w.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, header) {
			t.Errorf("Access-Control-Expose-Headers = %q, want it to contain %s", got, header)
		}
	}
}

func TestUnknownRouteAndMethodReturnTheErrorContract(t *testing.T) {
	r := testRouter(t)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		status int
		code   string
	}{
		{"unknown path", http.MethodGet, "/api/nie-ma-takiej-trasy", http.StatusNotFound, "NOT_FOUND"},
		{"wrong method", http.MethodPatch, "/api/health", http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))

			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
			if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Errorf("Content-Type = %q, want JSON", got)
			}
			if body := w.Body.String(); !strings.Contains(body, tc.code) {
				t.Errorf("body = %q, want code %s", body, tc.code)
			}
		})
	}
}

func TestOversizedBodyIsRejected(t *testing.T) {
	body := `{"email":"a@b.pl","password":"` + strings.Repeat("x", 2<<20) + `"}`

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", frontendOrigin)

	w := httptest.NewRecorder()
	testRouter(t).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestClientIPIgnoresForwardedHeaders(t *testing.T) {
	r := testRouter(t)
	r.GET("/test-client-ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })

	req := httptest.NewRequest(http.MethodGet, "/test-client-ip", nil)
	req.RemoteAddr = "192.0.2.1:4242"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Real-IP", "203.0.113.9")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Body.String(); got != "192.0.2.1" {
		t.Errorf("ClientIP = %q, want the connection address 192.0.2.1", got)
	}
}

func TestHealthzHasNoDependencies(t *testing.T) {
	failing := []monitoring.Probe{{Name: "db", Check: func(context.Context) error { return errors.New("down") }}}

	w := httptest.NewRecorder()
	routerWithProbes(t, testConfig(), failing).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — liveness nie może zależeć od bazy", w.Code)
	}
}

func TestReadyzReportsEachDependency(t *testing.T) {
	probes := []monitoring.Probe{
		{Name: "db", Check: func(context.Context) error { return errors.New("connection refused") }},
		{Name: "rabbitmq", Check: func(context.Context) error { return nil }},
	}

	w := httptest.NewRecorder()
	routerWithProbes(t, testConfig(), probes).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("odpowiedź nie jest JSON-em: %v", err)
	}
	if body["db"] != "down" || body["rabbitmq"] != "up" {
		t.Errorf("body = %v, want db=down rabbitmq=up", body)
	}
	if strings.Contains(w.Body.String(), "connection refused") {
		t.Errorf("odpowiedź ujawnia szczegóły błędu: %s", w.Body.String())
	}
}

func TestReadyzTimesOutAHangingProbe(t *testing.T) {
	probes := []monitoring.Probe{{Name: "db", Check: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}}

	start := time.Now()
	w := httptest.NewRecorder()
	routerWithProbes(t, testConfig(), probes).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	if elapsed := time.Since(start); elapsed > probeTimeout+time.Second {
		t.Errorf("sonda trwała %s, timeout to %s", elapsed, probeTimeout)
	}
}

func TestReadyzIsReadyWhenAllProbesPass(t *testing.T) {
	probes := []monitoring.Probe{{Name: "db", Check: func(context.Context) error { return nil }}}

	w := httptest.NewRecorder()
	routerWithProbes(t, testConfig(), probes).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestProbesIgnoreCORS(t *testing.T) {
	r := routerWithProbes(t, testConfig(), nil)

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Origin", "https://attacker.invalid")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200 — CORS nie powinien dotyczyć sond", w.Code)
			}
		})
	}
}

func TestRemovedAdminRoutesAreGone(t *testing.T) {
	r := testRouter(t)

	for _, path := range []string{"/api/admin/health", "/api/admin/database?table=users"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

			if w.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", w.Code)
			}
		})
	}
}

var adminRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/api/admin/users"},
	{http.MethodGet, "/api/admin/users/" + uuid.NewString()},
	{http.MethodPatch, "/api/admin/users/" + uuid.NewString() + "/role"},
	{http.MethodPost, "/api/admin/users/" + uuid.NewString() + "/logout"},
	{http.MethodDelete, "/api/admin/users/" + uuid.NewString()},
	{http.MethodGet, "/api/admin/scans"},
	{http.MethodGet, "/api/admin/scans/" + uuid.NewString()},
	{http.MethodDelete, "/api/admin/scans/" + uuid.NewString()},
	{http.MethodGet, "/api/admin/widgets"},
	{http.MethodGet, "/api/admin/stats/tests"},
	{http.MethodGet, "/api/admin/system"},
}

func TestAdminRoutesRequireAToken(t *testing.T) {
	r := testRouter(t)

	for _, route := range adminRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))

			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
		})
	}
}

func TestAdminRoutesRejectOrdinaryUsers(t *testing.T) {
	cfg := testConfig()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("baza testowa: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatalf("migracja: %v", err)
	}

	userID := uuid.New()
	if err := db.Create(&models.User{ID: userID, Email: "jan@example.com", Role: models.UserRoleUser}).Error; err != nil {
		t.Fatalf("zasianie konta: %v", err)
	}

	billingService := billing.New(db, nil, "", "")
	authHandler, err := handlers.NewAuthHandler(db, cfg, billingService)
	if err != nil {
		t.Fatalf("NewAuthHandler: %v", err)
	}
	r := NewRouter(handlers.NewScanHandler(nil, db), authHandler, handlers.NewAdminHandler(db, nil, billingService),
		handlers.NewBillingHandler(db, billingService, ""), handlers.NewDashboardHandler(db), handlers.NewSystemHandler(db, nil, nil, ""), cfg, nil)

	// Rola w tokenie kłamie: RequireAdmin ma patrzeć do bazy, nie w claim.
	token, err := auth.GenerateToken(cfg.JWTSecret, auth.TokenTypeAccess, userID.String(), models.UserRoleAdmin, time.Minute)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	for _, route := range adminRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
		})
	}
}

func TestFreeScanRejectsLoopbackTarget(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/freescans", strings.NewReader(`{"target_url":"http://127.0.0.1:15672"}`))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	testRouter(t).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"target_url":"not_public"`) {
		t.Errorf("body = %s, want fields.target_url = not_public", w.Body)
	}
}
