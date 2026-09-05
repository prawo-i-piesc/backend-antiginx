package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/prawo-i-piesc/backend/internal/models"
)

func TestGitHubAuthCodeURL(t *testing.T) {
	github := NewGitHub("klient-id", "sekret", "https://antiginx.pl/api/auth/oauth/github/callback")

	if github.Name() != models.ProviderGitHub {
		t.Errorf("Name = %q", github.Name())
	}

	raw := github.AuthCodeURL("stan-testowy", "weryfikator-ignorowany")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("adres autoryzacji nie jest poprawnym URI: %v", err)
	}
	if u.Host != "github.com" {
		t.Errorf("host = %q", u.Host)
	}

	q := u.Query()
	for key, want := range map[string]string{
		"client_id":     "klient-id",
		"redirect_uri":  "https://antiginx.pl/api/auth/oauth/github/callback",
		"response_type": "code",
		"state":         "stan-testowy",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("parametr %s = %q, want %q", key, got, want)
		}
	}

	if scope := q.Get("scope"); scope == "" {
		t.Error("brak zakresów, a bez user:email nie odczytamy potwierdzonego adresu")
	}
	if q.Get("client_secret") != "" {
		t.Error("sekret klienta trafił do adresu autoryzacji")
	}

	// GitHub nie obsługuje PKCE dla aplikacji OAuth, więc wysyłanie wyzwania
	// byłoby wprowadzaniem w błąd co do poziomu zabezpieczenia.
	if q.Get("code_challenge") != "" || q.Get("code_challenge_method") != "" {
		t.Error("adres zawiera parametry PKCE, których GitHub nie obsługuje")
	}
}

func TestSelectGitHubEmail(t *testing.T) {
	tests := []struct {
		name         string
		emails       []GitHubEmail
		wantEmail    string
		wantVerified bool
	}{
		{
			name: "podstawowy i potwierdzony",
			emails: []GitHubEmail{
				{Email: "inny@example.com", Verified: true},
				{Email: "jan@example.com", Primary: true, Verified: true},
			},
			wantEmail:    "jan@example.com",
			wantVerified: true,
		},
		{
			name: "podstawowy bez potwierdzenia",
			emails: []GitHubEmail{
				{Email: "jan@example.com", Primary: true},
			},
			wantEmail:    "jan@example.com",
			wantVerified: false,
		},
		{
			name: "potwierdzony ale nie podstawowy",
			emails: []GitHubEmail{
				{Email: "inny@example.com", Verified: true},
			},
		},
		{
			name:   "pusta lista",
			emails: nil,
		},
		{
			name: "podstawowy z pustym adresem",
			emails: []GitHubEmail{
				{Email: "   ", Primary: true, Verified: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email, verified := SelectGitHubEmail(tt.emails)
			if email != tt.wantEmail {
				t.Errorf("adres = %q, want %q", email, tt.wantEmail)
			}
			if verified != tt.wantVerified {
				t.Errorf("potwierdzony = %v, want %v", verified, tt.wantVerified)
			}
		})
	}
}

// fakeGitHub udaje serwer tokenów i API GitHuba, żeby przejść całą wymianę
// kodu bez sieci.
func fakeGitHub(t *testing.T, account any, emails any) *GitHub {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token-testowy",
			"token_type":   "bearer",
		})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "brak User-Agent", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(account)
	})
	mux.HandleFunc("/user/emails", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(emails)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	github := NewGitHub("klient-id", "sekret", "https://antiginx.pl/callback")
	github.config.Endpoint.TokenURL = server.URL + "/login/oauth/access_token"
	github.apiBaseURL = server.URL
	return github
}

func TestGitHubExchange(t *testing.T) {
	github := fakeGitHub(t,
		map[string]any{"id": 4242, "login": "jankowalski", "name": "Jan Kowalski"},
		[]GitHubEmail{
			{Email: "prywatny@example.com", Verified: true},
			{Email: "jan@example.com", Primary: true, Verified: true},
		},
	)

	profile, err := github.Exchange(context.Background(), "kod", "")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if profile.Subject != "4242" {
		t.Errorf("Subject = %q, want %q — identyfikatorem musi być stabilne id, nie login", profile.Subject, "4242")
	}
	if profile.Email != "jan@example.com" {
		t.Errorf("Email = %q", profile.Email)
	}
	if !profile.EmailVerified {
		t.Error("EmailVerified = false")
	}
	if profile.FullName != "Jan Kowalski" {
		t.Errorf("FullName = %q", profile.FullName)
	}
}

func TestGitHubExchangeFallsBackToLogin(t *testing.T) {
	github := fakeGitHub(t,
		map[string]any{"id": 7, "login": "jankowalski", "name": ""},
		[]GitHubEmail{{Email: "jan@example.com", Primary: true, Verified: true}},
	)

	profile, err := github.Exchange(context.Background(), "kod", "")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if profile.FullName != "jankowalski" {
		t.Errorf("FullName = %q, want %q", profile.FullName, "jankowalski")
	}
}

func TestGitHubExchangeReportsUnverifiedEmail(t *testing.T) {
	github := fakeGitHub(t,
		map[string]any{"id": 7, "login": "jankowalski"},
		[]GitHubEmail{{Email: "jan@example.com", Primary: true, Verified: false}},
	)

	profile, err := github.Exchange(context.Background(), "kod", "")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if profile.EmailVerified {
		t.Error("EmailVerified = true dla niepotwierdzonego adresu")
	}
}

func TestGitHubExchangeWithoutUsableEmail(t *testing.T) {
	github := fakeGitHub(t,
		map[string]any{"id": 7, "login": "jankowalski"},
		[]GitHubEmail{{Email: "inny@example.com", Verified: true}},
	)

	if _, err := github.Exchange(context.Background(), "kod", ""); !errors.Is(err, ErrProfileUnavailable) {
		t.Errorf("err = %v, want ErrProfileUnavailable", err)
	}
}

func TestGitHubExchangeWithoutAccountID(t *testing.T) {
	github := fakeGitHub(t,
		map[string]any{"login": "jankowalski"},
		[]GitHubEmail{{Email: "jan@example.com", Primary: true, Verified: true}},
	)

	if _, err := github.Exchange(context.Background(), "kod", ""); !errors.Is(err, ErrProfileUnavailable) {
		t.Errorf("err = %v, want ErrProfileUnavailable", err)
	}
}

func TestRegistryHoldsBothProviders(t *testing.T) {
	registry := NewRegistry(
		NewGoogle("id", "sekret", "https://antiginx.pl/api/auth/oauth/google/callback"),
		NewGitHub("id", "sekret", "https://antiginx.pl/api/auth/oauth/github/callback"),
	)

	for _, name := range []string{models.ProviderGoogle, models.ProviderGitHub} {
		if _, ok := registry.Get(name); !ok {
			t.Errorf("dostawca %q nie został zarejestrowany", name)
		}
	}
	if len(registry.Names()) != 2 {
		t.Errorf("zarejestrowano %d dostawców, oczekiwano 2", len(registry.Names()))
	}
}
