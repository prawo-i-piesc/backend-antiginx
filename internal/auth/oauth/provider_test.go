package oauth

import (
	"context"
	"net/url"
	"testing"

	"github.com/prawo-i-piesc/backend/internal/models"
)

type stubProvider struct{ name string }

func (s *stubProvider) Name() string                   { return s.name }
func (s *stubProvider) AuthCodeURL(_, _ string) string { return "" }
func (s *stubProvider) Exchange(context.Context, string, string) (*Profile, error) {
	return nil, nil
}

func TestRegistryLookup(t *testing.T) {
	registry := NewRegistry(&stubProvider{name: "google"})

	if _, ok := registry.Get("google"); !ok {
		t.Error("zarejestrowany dostawca nie został znaleziony")
	}
	if _, ok := registry.Get("github"); ok {
		t.Error("niezarejestrowany dostawca został znaleziony")
	}
}

func TestRegistryIgnoresNilProviders(t *testing.T) {
	registry := NewRegistry(nil, &stubProvider{name: "google"}, nil)

	if len(registry.Names()) != 1 {
		t.Errorf("zarejestrowano %d dostawców, oczekiwano 1", len(registry.Names()))
	}
}

func TestEmptyRegistry(t *testing.T) {
	registry := NewRegistry()

	if _, ok := registry.Get(models.ProviderGoogle); ok {
		t.Error("pusty rejestr zwrócił dostawcę")
	}
	if len(registry.Names()) != 0 {
		t.Error("pusty rejestr zwrócił nazwy")
	}
}

func TestGoogleAuthCodeURL(t *testing.T) {
	google := NewGoogle("klient-id", "sekret", "https://antiginx.pl/api/auth/oauth/google/callback")

	if google.Name() != models.ProviderGoogle {
		t.Errorf("Name = %q", google.Name())
	}

	raw := google.AuthCodeURL("stan-testowy", "weryfikator-testowy-o-odpowiedniej-dlugosci")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("adres autoryzacji nie jest poprawnym URI: %v", err)
	}
	if u.Host != "accounts.google.com" {
		t.Errorf("host = %q", u.Host)
	}

	q := u.Query()
	for key, want := range map[string]string{
		"client_id":             "klient-id",
		"redirect_uri":          "https://antiginx.pl/api/auth/oauth/google/callback",
		"response_type":         "code",
		"state":                 "stan-testowy",
		"code_challenge_method": "S256",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("parametr %s = %q, want %q", key, got, want)
		}
	}

	if q.Get("code_challenge") == "" {
		t.Error("brak code_challenge, czyli PKCE nie działa")
	}
	if q.Get("code_challenge") == "weryfikator-testowy-o-odpowiedniej-dlugosci" {
		t.Error("code_challenge jest równy weryfikatorowi, czyli nie został zahaszowany")
	}
	if scope := q.Get("scope"); scope == "" {
		t.Error("brak zakresów")
	}
	if q.Get("client_secret") != "" {
		t.Error("sekret klienta trafił do adresu autoryzacji")
	}
}
