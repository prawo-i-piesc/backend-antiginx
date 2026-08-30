package oauth

import (
	"context"
	"errors"
)

var (
	ErrProviderUnavailable = errors.New("oauth: identity provider unavailable")
	ErrExchangeFailed      = errors.New("oauth: authorization code exchange failed")
	ErrProfileUnavailable  = errors.New("oauth: provider did not return a usable profile")
)

type Profile struct {
	Subject       string
	Email         string
	EmailVerified bool
	FullName      string
}

type Provider interface {
	Name() string
	AuthCodeURL(state, verifier string) string
	Exchange(ctx context.Context, code, verifier string) (*Profile, error)
}

type Registry struct {
	providers map[string]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	registry := &Registry{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		if provider != nil {
			registry.providers[provider.Name()] = provider
		}
	}
	return registry
}

func (r *Registry) Get(name string) (Provider, bool) {
	provider, ok := r.providers[name]
	return provider, ok
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}
