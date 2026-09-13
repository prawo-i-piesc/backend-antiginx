package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/prawo-i-piesc/backend/internal/models"
	"golang.org/x/oauth2"
)

const (
	githubAPIBaseURL = "https://api.github.com"
	githubAPIVersion = "2022-11-28"
	githubUserAgent  = "backend-antiginx"
)

var githubEndpoint = oauth2.Endpoint{
	AuthURL:   "https://github.com/login/oauth/authorize",
	TokenURL:  "https://github.com/login/oauth/access_token",
	AuthStyle: oauth2.AuthStyleInParams,
}

type GitHub struct {
	config     *oauth2.Config
	apiBaseURL string
}

func NewGitHub(clientID, clientSecret, redirectURI string) *GitHub {
	return &GitHub{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURI,
			Endpoint:     githubEndpoint,
			Scopes:       []string{"read:user", "user:email"},
		},
		apiBaseURL: githubAPIBaseURL,
	}
}

func (g *GitHub) Name() string {
	return models.ProviderGitHub
}

func (g *GitHub) AuthCodeURL(state, _ string) string {
	return g.config.AuthCodeURL(state)
}

func (g *GitHub) Exchange(ctx context.Context, code, _ string) (*Profile, error) {
	token, err := g.config.Exchange(ctx, code)
	if err != nil {
		return nil, ErrExchangeFailed
	}

	client := g.config.Client(ctx, token)

	var account struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := g.get(ctx, client, "/user", &account); err != nil {
		return nil, err
	}
	if account.ID == 0 {
		return nil, ErrProfileUnavailable
	}

	var emails []GitHubEmail
	if err := g.get(ctx, client, "/user/emails", &emails); err != nil {
		return nil, err
	}

	email, verified := SelectGitHubEmail(emails)
	if email == "" {
		return nil, ErrProfileUnavailable
	}

	fullName := strings.TrimSpace(account.Name)
	if fullName == "" {
		fullName = account.Login
	}

	return &Profile{
		Subject:       strconv.FormatInt(account.ID, 10),
		Email:         email,
		EmailVerified: verified,
		FullName:      fullName,
	}, nil
}

func (g *GitHub) get(ctx context.Context, client *http.Client, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.apiBaseURL+path, nil)
	if err != nil {
		return ErrProfileUnavailable
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	req.Header.Set("User-Agent", githubUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return ErrProviderUnavailable
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s returned %d", ErrProfileUnavailable, path, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return ErrProfileUnavailable
	}
	return nil
}

type GitHubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

func SelectGitHubEmail(emails []GitHubEmail) (string, bool) {
	for _, entry := range emails {
		if entry.Primary && entry.Verified && strings.TrimSpace(entry.Email) != "" {
			return entry.Email, true
		}
	}

	for _, entry := range emails {
		if entry.Primary && strings.TrimSpace(entry.Email) != "" {
			return entry.Email, false
		}
	}
	return "", false
}
