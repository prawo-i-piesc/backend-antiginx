package oauth

import (
	"context"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/prawo-i-piesc/backend/internal/models"
	"golang.org/x/oauth2"
)

const googleIssuer = "https://accounts.google.com"

var googleEndpoint = oauth2.Endpoint{
	AuthURL:   "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL:  "https://oauth2.googleapis.com/token",
	AuthStyle: oauth2.AuthStyleInParams,
}

type Google struct {
	config *oauth2.Config

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
}

func NewGoogle(clientID, clientSecret, redirectURI string) *Google {
	return &Google{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURI,
			Endpoint:     googleEndpoint,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
	}
}

func (g *Google) Name() string {
	return models.ProviderGoogle
}

func (g *Google) AuthCodeURL(state, verifier string) string {
	return g.config.AuthCodeURL(
		state,
		oauth2.AccessTypeOnline,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "select_account"),
	)
}

func (g *Google) Exchange(ctx context.Context, code, verifier string) (*Profile, error) {
	token, err := g.config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, ErrExchangeFailed
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, ErrProfileUnavailable
	}

	idTokenVerifier, err := g.idTokenVerifier(ctx)
	if err != nil {
		return nil, err
	}

	idToken, err := idTokenVerifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, ErrProfileUnavailable
	}

	var claims struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, ErrProfileUnavailable
	}
	if strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.Email) == "" {
		return nil, ErrProfileUnavailable
	}

	return &Profile{
		Subject:       claims.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		FullName:      strings.TrimSpace(claims.Name),
	}, nil
}

func (g *Google) idTokenVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.verifier != nil {
		return g.verifier, nil
	}

	provider, err := oidc.NewProvider(ctx, googleIssuer)
	if err != nil {
		return nil, ErrProviderUnavailable
	}

	g.verifier = provider.Verifier(&oidc.Config{ClientID: g.config.ClientID})
	return g.verifier, nil
}
