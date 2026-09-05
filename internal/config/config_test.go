package config

import (
	"strings"
	"testing"
	"time"
)

var allKeys = []string{
	"DATABASE_URL", "RABBITMQ_URL", "JWT_SECRET", "PUBLIC_BASE_URL",
	"COOKIE_SECURE", "ACCESS_TOKEN_TTL", "REFRESH_TOKEN_TTL", "TOTP_ENCRYPTION_KEY",
	"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET",
	"GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET",
	"WEBAUTHN_RPID", "WEBAUTHN_RP_NAME",
}

const testTOTPKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":        "postgres://user:pass@localhost:5432/antiginx",
		"RABBITMQ_URL":        "amqp://user:pass@localhost:5672/",
		"JWT_SECRET":          "a-secret-long-enough-to-avoid-the-warning",
		"PUBLIC_BASE_URL":     "https://antiginx.pl",
		"TOTP_ENCRYPTION_KEY": testTOTPKey,
	}
}

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, key := range allKeys {
		t.Setenv(key, "")
	}
	for key, value := range env {
		t.Setenv(key, value)
	}
}

func TestLoadValidEnvironment(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.AccessTokenTTL != DefaultAccessTokenTTL {
		t.Errorf("AccessTokenTTL = %v, want %v", cfg.AccessTokenTTL, DefaultAccessTokenTTL)
	}
	if cfg.RefreshTokenTTL != DefaultRefreshTokenTTL {
		t.Errorf("RefreshTokenTTL = %v, want %v", cfg.RefreshTokenTTL, DefaultRefreshTokenTTL)
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure = false, want true by default")
	}
	if string(cfg.JWTSecret) != validEnv()["JWT_SECRET"] {
		t.Error("JWTSecret was not read from the environment")
	}
	if len(cfg.TOTPEncryptionKey) != 32 {
		t.Errorf("TOTPEncryptionKey ma %d bajtów, oczekiwano 32", len(cfg.TOTPEncryptionKey))
	}
}

func TestLoadOverrides(t *testing.T) {
	env := validEnv()
	env["COOKIE_SECURE"] = "false"
	env["ACCESS_TOKEN_TTL"] = "30m"
	env["REFRESH_TOKEN_TTL"] = "168h"
	setEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.CookieSecure {
		t.Error("CookieSecure = true, want false")
	}
	if cfg.AccessTokenTTL != 30*time.Minute {
		t.Errorf("AccessTokenTTL = %v, want 30m", cfg.AccessTokenTTL)
	}
	if cfg.RefreshTokenTTL != 168*time.Hour {
		t.Errorf("RefreshTokenTTL = %v, want 168h", cfg.RefreshTokenTTL)
	}
}

func TestLoadRequiresVariables(t *testing.T) {
	for _, key := range []string{"DATABASE_URL", "RABBITMQ_URL", "JWT_SECRET", "PUBLIC_BASE_URL", "TOTP_ENCRYPTION_KEY"} {
		t.Run("missing "+key, func(t *testing.T) {
			env := validEnv()
			delete(env, key)
			setEnv(t, env)

			_, err := Load()
			if err == nil {
				t.Fatalf("Load succeeded without %s", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error does not mention %s: %v", key, err)
			}
		})
	}
}

func TestLoadReportsAllProblemsAtOnce(t *testing.T) {
	setEnv(t, nil)

	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded with an empty environment")
	}
	for _, key := range []string{"DATABASE_URL", "RABBITMQ_URL", "JWT_SECRET", "PUBLIC_BASE_URL", "TOTP_ENCRYPTION_KEY"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s: %v", key, err)
		}
	}
}

func TestLoadRejectsMalformedValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"public base url with a path", "PUBLIC_BASE_URL", "https://antiginx.pl/app"},
		{"public base url with a query", "PUBLIC_BASE_URL", "https://antiginx.pl?a=1"},
		{"public base url without a scheme", "PUBLIC_BASE_URL", "antiginx.pl"},
		{"public base url with a foreign scheme", "PUBLIC_BASE_URL", "ftp://antiginx.pl"},
		{"cookie secure not a boolean", "COOKIE_SECURE", "yes please"},
		{"access ttl not a duration", "ACCESS_TOKEN_TTL", "15 minutes"},
		{"access ttl not positive", "ACCESS_TOKEN_TTL", "-15m"},
		{"refresh ttl not a duration", "REFRESH_TOKEN_TTL", "forever"},
		{"totp key not base64", "TOTP_ENCRYPTION_KEY", "nie-base64!!"},
		{"totp key too short", "TOTP_ENCRYPTION_KEY", "c2hvcnQ="},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
			env[tt.key] = tt.value
			setEnv(t, env)

			if _, err := Load(); err == nil {
				t.Fatalf("Load accepted %s=%q", tt.key, tt.value)
			}
		})
	}
}

func TestLoadNormalizesPublicBaseURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"https://antiginx.pl/", "https://antiginx.pl"},
		{"https://ANTIGINX.pl", "https://antiginx.pl"},
		{"HTTPS://AntiGinx.PL/", "https://antiginx.pl"},
		{"http://localhost:3000", "http://localhost:3000"},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			env := validEnv()
			env["PUBLIC_BASE_URL"] = tt.raw
			setEnv(t, env)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.PublicBaseURL != tt.want {
				t.Errorf("PublicBaseURL = %q, want %q", cfg.PublicBaseURL, tt.want)
			}
		})
	}
}

func TestGoogleCredentialsMustBeSetTogether(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		secret    string
		wantError bool
		enabled   bool
	}{
		{"oba puste", "", "", false, false},
		{"oba ustawione", "klient-id", "sekret", false, true},
		{"samo id", "klient-id", "", true, false},
		{"sam sekret", "", "sekret", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
			env["GOOGLE_CLIENT_ID"] = tt.id
			env["GOOGLE_CLIENT_SECRET"] = tt.secret
			setEnv(t, env)

			cfg, err := Load()
			if tt.wantError {
				if err == nil {
					t.Fatal("Load przyjął połowiczną konfigurację Google")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.GoogleEnabled() != tt.enabled {
				t.Errorf("GoogleEnabled = %v, want %v", cfg.GoogleEnabled(), tt.enabled)
			}
		})
	}
}

// RPID jest wpisywany w passkey na stałe, więc musi wynikać z originu frontu
// i nie może zawierać portu.
func TestWebAuthnRPIDDerivedFromPublicBaseURL(t *testing.T) {
	tests := []struct {
		origin string
		want   string
	}{
		{"https://antiginx.pl", "antiginx.pl"},
		{"https://antiginx.pl/", "antiginx.pl"},
		{"http://localhost:3000", "localhost"},
		{"https://app.antiginx.pl:8443", "app.antiginx.pl"},
	}

	for _, tt := range tests {
		t.Run(tt.origin, func(t *testing.T) {
			env := validEnv()
			env["PUBLIC_BASE_URL"] = tt.origin
			setEnv(t, env)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.WebAuthnRPID != tt.want {
				t.Errorf("WebAuthnRPID = %q, want %q", cfg.WebAuthnRPID, tt.want)
			}
		})
	}
}

func TestWebAuthnOverrides(t *testing.T) {
	env := validEnv()
	env["WEBAUTHN_RPID"] = "antiginx.pl"
	env["WEBAUTHN_RP_NAME"] = "AntiGinx Test"
	env["PUBLIC_BASE_URL"] = "http://localhost:3000"
	setEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WebAuthnRPID != "antiginx.pl" {
		t.Errorf("WebAuthnRPID = %q, jawna wartość powinna wygrać z wyliczoną", cfg.WebAuthnRPID)
	}
	if cfg.WebAuthnRPName != "AntiGinx Test" {
		t.Errorf("WebAuthnRPName = %q", cfg.WebAuthnRPName)
	}
}

func TestWebAuthnRPNameDefault(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WebAuthnRPName != DefaultWebAuthnRPName {
		t.Errorf("WebAuthnRPName = %q, want %q", cfg.WebAuthnRPName, DefaultWebAuthnRPName)
	}
}

func TestGitHubCredentialsMustBeSetTogether(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		secret    string
		wantError bool
		enabled   bool
	}{
		{"oba puste", "", "", false, false},
		{"oba ustawione", "klient-id", "sekret", false, true},
		{"samo id", "klient-id", "", true, false},
		{"sam sekret", "", "sekret", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
			env["GITHUB_CLIENT_ID"] = tt.id
			env["GITHUB_CLIENT_SECRET"] = tt.secret
			setEnv(t, env)

			cfg, err := Load()
			if tt.wantError {
				if err == nil {
					t.Fatal("Load przyjął połowiczną konfigurację GitHuba")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.GitHubEnabled() != tt.enabled {
				t.Errorf("GitHubEnabled = %v, want %v", cfg.GitHubEnabled(), tt.enabled)
			}
		})
	}
}

func TestProvidersAreIndependent(t *testing.T) {
	env := validEnv()
	env["GOOGLE_CLIENT_ID"] = "klient-id"
	env["GOOGLE_CLIENT_SECRET"] = "sekret"
	setEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.GoogleEnabled() {
		t.Error("Google powinien być włączony")
	}
	if cfg.GitHubEnabled() {
		t.Error("GitHub włączył się bez własnych kluczy")
	}
}

func TestOAuthRedirectURI(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for provider, want := range map[string]string{
		"google": "https://antiginx.pl/api/auth/oauth/google/callback",
		"github": "https://antiginx.pl/api/auth/oauth/github/callback",
	} {
		if got := cfg.OAuthRedirectURI(provider); got != want {
			t.Errorf("OAuthRedirectURI(%q) = %q, want %q", provider, got, want)
		}
	}
}

func TestLoadAcceptsShortJWTSecret(t *testing.T) {
	env := validEnv()
	env["JWT_SECRET"] = "short"
	setEnv(t, env)

	if _, err := Load(); err != nil {
		t.Fatalf("Load rejected a short JWT_SECRET: %v", err)
	}
}
