package config

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/netip"
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

const (
	DefaultResultsQueue    = "results_queue"
	DefaultResultConsumers = 2
	DefaultResultBatchSize = 50
)

const DefaultStripeSyncInterval = time.Hour

const (
	MailTransportResend = "resend"
	MailTransportLog    = "log"
	MailTransportNone   = "none"
)

type Config struct {
	DatabaseURL string
	RabbitMQURL string

	// ResultsQueue musi być tą samą nazwą, co RABBITMQ_RESULTS_QUEUE daemona.
	ResultsQueue    string
	ResultConsumers int
	ResultBatchSize int

	JWTSecret []byte

	PublicBaseURL string

	CookieSecure bool

	TrustedProxies []string

	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	TOTPEncryptionKey []byte

	GoogleClientID     string
	GoogleClientSecret string

	GitHubClientID     string
	GitHubClientSecret string

	WebAuthnRPID   string
	WebAuthnRPName string

	MailTransport string
	MailFrom      string
	ResendAPIKey  string

	StripeSecretKey     string
	StripeProPriceID    string
	StripeWebhookSecret string
	StripeSyncInterval  time.Duration

	// Opcjonalne: bez PROMETHEUS_URL widok systemu w panelu admina pokazuje
	// tylko sondy i kolejkę skanów; GRAFANA_URL dokłada link do dashboardów.
	PrometheusURL string
	GrafanaURL    string
}

// BillingEnabled mówi, czy można sprzedawać Pro. Bez kluczy Stripe wszyscy są
// na planie Free, a endpointy płatności odpowiadają BILLING_DISABLED.
func (c *Config) BillingEnabled() bool {
	return c.StripeSecretKey != "" && c.StripeProPriceID != ""
}

func (c *Config) GoogleEnabled() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

func (c *Config) GitHubEnabled() bool {
	return c.GitHubClientID != "" && c.GitHubClientSecret != ""
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

	secret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
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

	cfg.GitHubClientID = strings.TrimSpace(os.Getenv("GITHUB_CLIENT_ID"))
	cfg.GitHubClientSecret = strings.TrimSpace(os.Getenv("GITHUB_CLIENT_SECRET"))
	if (cfg.GitHubClientID == "") != (cfg.GitHubClientSecret == "") {
		problems = append(problems, "GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET must be set together")
	}

	cfg.WebAuthnRPName = envOrDefault("WEBAUTHN_RP_NAME", DefaultWebAuthnRPName)
	cfg.WebAuthnRPID = strings.TrimSpace(os.Getenv("WEBAUTHN_RPID"))

	cfg.ResendAPIKey = strings.TrimSpace(os.Getenv("RESEND_API_KEY"))
	cfg.MailFrom = strings.TrimSpace(os.Getenv("MAIL_FROM"))
	cfg.MailTransport = strings.ToLower(envOrDefault("MAIL_TRANSPORT", defaultMailTransport(cfg.ResendAPIKey)))

	switch cfg.MailTransport {
	case MailTransportResend:
		if cfg.ResendAPIKey == "" {
			problems = append(problems, "RESEND_API_KEY is required when MAIL_TRANSPORT is resend")
		}
		if cfg.MailFrom == "" {
			problems = append(problems, "MAIL_FROM is required when MAIL_TRANSPORT is resend")
		}
	case MailTransportLog, MailTransportNone:
	default:
		problems = append(problems, fmt.Sprintf("MAIL_TRANSPORT must be resend, log or none, got %q", cfg.MailTransport))
	}

	cfg.StripeSecretKey = strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY"))
	cfg.StripeProPriceID = strings.TrimSpace(os.Getenv("STRIPE_PRO_PRICE_ID"))
	cfg.StripeWebhookSecret = strings.TrimSpace(os.Getenv("STRIPE_WEBHOOK_SECRET"))
	problems = append(problems, stripeProblems(cfg)...)

	for _, setting := range []struct {
		key    string
		target *string
	}{
		{"PROMETHEUS_URL", &cfg.PrometheusURL},
		{"GRAFANA_URL", &cfg.GrafanaURL},
	} {
		raw := strings.TrimSpace(os.Getenv(setting.key))
		if raw == "" {
			continue
		}
		if value, err := normalizeServiceURL(raw); err != nil {
			problems = append(problems, fmt.Sprintf("%s is invalid: %v", setting.key, err))
		} else {
			*setting.target = value
		}
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

	if cfg.TrustedProxies, err = envProxies("TRUSTED_PROXIES"); err != nil {
		problems = append(problems, err.Error())
	}

	if cfg.StripeSyncInterval, err = envDuration("STRIPE_SYNC_INTERVAL", DefaultStripeSyncInterval); err != nil {
		problems = append(problems, err.Error())
	}

	cfg.ResultsQueue = envOrDefault("RABBITMQ_RESULTS_QUEUE", DefaultResultsQueue)
	if cfg.ResultConsumers, err = envPositiveInt("RESULT_CONSUMERS", DefaultResultConsumers); err != nil {
		problems = append(problems, err.Error())
	}
	if cfg.ResultBatchSize, err = envPositiveInt("RESULT_BATCH_SIZE", DefaultResultBatchSize); err != nil {
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

// normalizeServiceURL przyjmuje adres usługi z opcjonalną ścieżką (Grafana bywa
// wystawiona pod /grafana), ale bez zapytania i fragmentu.
func normalizeServiceURL(raw string) (string, error) {
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
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("must not have a query or fragment")
	}
	// GRAFANA_URL trafia do przeglądarki admina, więc nie może nieść hasła.
	if u.User != nil {
		return "", fmt.Errorf("must not contain credentials")
	}
	return u.String(), nil
}

// stripeProblems sprawdza prefiksy, bo pomylenie klucza publicznego z
// sekretnym albo ID produktu z ID ceny wychodzi inaczej dopiero przy pierwszej
// płatności.
func stripeProblems(cfg *Config) []string {
	var problems []string
	if (cfg.StripeSecretKey == "") != (cfg.StripeProPriceID == "") {
		problems = append(problems, "STRIPE_SECRET_KEY and STRIPE_PRO_PRICE_ID must be set together")
	}
	if cfg.StripeSecretKey != "" && !strings.HasPrefix(cfg.StripeSecretKey, "sk_") && !strings.HasPrefix(cfg.StripeSecretKey, "rk_") {
		problems = append(problems, "STRIPE_SECRET_KEY must be a secret (sk_) or restricted (rk_) key")
	}
	if cfg.StripeProPriceID != "" && !strings.HasPrefix(cfg.StripeProPriceID, "price_") {
		problems = append(problems, "STRIPE_PRO_PRICE_ID must be a price ID (price_...), not a product ID")
	}
	if cfg.StripeWebhookSecret != "" {
		if cfg.StripeSecretKey == "" {
			problems = append(problems, "STRIPE_WEBHOOK_SECRET requires STRIPE_SECRET_KEY")
		}
		if !strings.HasPrefix(cfg.StripeWebhookSecret, "whsec_") {
			problems = append(problems, "STRIPE_WEBHOOK_SECRET must be a webhook signing secret (whsec_...)")
		}
	}
	return problems
}

func defaultMailTransport(apiKey string) string {
	if apiKey != "" {
		return MailTransportResend
	}
	return MailTransportNone
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

func envProxies(key string) ([]string, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil, nil
	}

	var proxies []string
	for _, part := range strings.Split(raw, ",") {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}
		_, prefixErr := netip.ParsePrefix(entry)
		_, addrErr := netip.ParseAddr(entry)
		if prefixErr != nil && addrErr != nil {
			return nil, fmt.Errorf("%s entries must be IP addresses or CIDR ranges, got %q", key, entry)
		}
		proxies = append(proxies, entry)
	}
	return proxies, nil
}

func envPositiveInt(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback, fmt.Errorf("%s must be a positive integer, got %q", key, raw)
	}
	return v, nil
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
