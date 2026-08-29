package config

import (
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

type Config struct {
	DatabaseURL string
	RabbitMQURL string

	JWTSecret []byte

	PublicBaseURL string

	CookieSecure bool

	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
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
