package config

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultAccessTokenTTL  = 15 * time.Minute
	DefaultRefreshTokenTTL = 720 * time.Hour
)

const minJWTSecretLen = 32

const totpEncryptionKeyLen = 32

const DefaultWebAuthnRPName = "AntiGinx"

type Config struct {
	DatabaseURL string
	RabbitMQURL string

	JWTSecret []byte

	PublicBaseURL string

	CookieSecure bool

	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	TOTPEncryptionKey []byte

	GoogleClientID     string
	GoogleClientSecret string

	WebAuthnRPID   string
	WebAuthnRPName string
}

func (c *Config) GoogleEnabled() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

func (c *Config) OAuthRedirectURI(provider string) string {
	return c.PublicBaseURL + "/api/auth/oauth/" + provider + "/callback"
}

func Load() (*Config, error) {
	var problems []string

	cfg := &Config{
		DatabaseURL: strings.TrimSpace(os.Getenv("DATABASE_URL")),
		RabbitMQURL: strings.TrimSpace(os.Getenv("RABBITMQ_URL")),
	}

	secret := os.Getenv("JWT_SECRET")
	cfg.JWTSecret = []byte(secret)

	if cfg.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL is required")
	}
	if cfg.RabbitMQURL == "" {
		problems = append(problems, "RABBITMQ_URL is required")
	}
	if secret == "" {
		problems = append(problems, "JWT_SECRET is required")
	} else if len(secret) < minJWTSecretLen {
		log.Printf("Ostrzeżenie: JWT_SECRET ma %d znaków, zalecane minimum to %d", len(secret), minJWTSecretLen)
	}

	if raw := strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")); raw == "" {
		problems = append(problems, "PUBLIC_BASE_URL is required (frontend origin, e.g. https://antiginx.pl)")
	} else if origin, err := normalizeOrigin(raw); err != nil {
		problems = append(problems, fmt.Sprintf("PUBLIC_BASE_URL is invalid: %v", err))
	} else {
		cfg.PublicBaseURL = origin
	}

	if raw := strings.TrimSpace(os.Getenv("TOTP_ENCRYPTION_KEY")); raw == "" {
		problems = append(problems, "TOTP_ENCRYPTION_KEY is required (32 random bytes, base64)")
	} else if key, err := decodeTOTPKey(raw); err != nil {
		problems = append(problems, fmt.Sprintf("TOTP_ENCRYPTION_KEY is invalid: %v", err))
	} else {
		cfg.TOTPEncryptionKey = key
	}

	cfg.GoogleClientID = strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
	cfg.GoogleClientSecret = strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET"))
	if (cfg.GoogleClientID == "") != (cfg.GoogleClientSecret == "") {
		problems = append(problems, "GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be set together")
	}

	cfg.WebAuthnRPName = envOrDefault("WEBAUTHN_RP_NAME", DefaultWebAuthnRPName)
	cfg.WebAuthnRPID = strings.TrimSpace(os.Getenv("WEBAUTHN_RPID"))

	secure, err := envBool("COOKIE_SECURE", true)
	if err != nil {
		problems = append(problems, err.Error())
	}
	cfg.CookieSecure = secure

	if cfg.AccessTokenTTL, err = envDuration("ACCESS_TOKEN_TTL", DefaultAccessTokenTTL); err != nil {
		problems = append(problems, err.Error())
	}
	if cfg.RefreshTokenTTL, err = envDuration("REFRESH_TOKEN_TTL", DefaultRefreshTokenTTL); err != nil {
		problems = append(problems, err.Error())
	}

	if cfg.WebAuthnRPID == "" && cfg.PublicBaseURL != "" {
		rpID, err := hostWithoutPort(cfg.PublicBaseURL)
		if err != nil {
			problems = append(problems, fmt.Sprintf("cannot derive WEBAUTHN_RPID from PUBLIC_BASE_URL: %v", err))
		} else {
			cfg.WebAuthnRPID = rpID
		}
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return cfg, nil
}

func normalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("missing host")
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("must be a bare origin, without path, query or fragment")
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

func hostWithoutPort(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("missing host")
	}
	return u.Hostname(), nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func decodeTOTPKey(raw string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("must be valid base64")
		}
	}
	if len(key) != totpEncryptionKeyLen {
		return nil, fmt.Errorf("must decode to %d bytes, got %d", totpEncryptionKeyLen, len(key))
	}
	return key, nil
}

func envBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s must be a boolean, got %q", key, raw)
	}
	return v, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s must be a duration such as 15m or 720h, got %q", key, raw)
	}
	if v <= 0 {
		return fallback, fmt.Errorf("%s must be positive, got %q", key, raw)
	}
	return v, nil
}
