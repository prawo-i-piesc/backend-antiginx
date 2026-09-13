package oauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const (
	StateCookieName = "ag_oauth"
	StateTTL        = 10 * time.Minute
	stateBytes      = 32
)

type Flow struct {
	Provider  string
	State     string
	Verifier  string
	Next      string
	UserID    string
	ExpiresAt time.Time
}

func (f *Flow) MatchesState(state string) bool {
	return subtle.ConstantTimeCompare([]byte(f.State), []byte(state)) == 1
}

func (f *Flow) IsLink() bool {
	return f.UserID != ""
}

type StateStore struct {
	mu    sync.Mutex
	flows map[string]*Flow
}

func NewStateStore() *StateStore {
	return &StateStore{flows: make(map[string]*Flow)}
}

func (s *StateStore) Issue(provider, next, userID string) (string, *Flow, error) {
	id, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	state, err := randomToken()
	if err != nil {
		return "", nil, err
	}

	flow := &Flow{
		Provider:  provider,
		State:     state,
		Verifier:  oauth2.GenerateVerifier(),
		Next:      SanitizeNext(next),
		UserID:    userID,
		ExpiresAt: time.Now().Add(StateTTL),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweepLocked()
	s.flows[id] = flow

	return id, flow, nil
}

func (s *StateStore) Consume(id string) (*Flow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	flow, ok := s.flows[id]
	if !ok {
		return nil, false
	}

	delete(s.flows, id)

	if time.Now().After(flow.ExpiresAt) {
		return nil, false
	}
	return flow, true
}

func (s *StateStore) sweepLocked() {
	now := time.Now()
	for id, flow := range s.flows {
		if now.After(flow.ExpiresAt) {
			delete(s.flows, id)
		}
	}
}

func randomToken() (string, error) {
	buf := make([]byte, stateBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
