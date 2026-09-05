package oauth

import (
	"errors"
	"sync"
	"time"
)

const (
	PendingTTL         = 10 * time.Minute
	PendingMaxAttempts = 5
)

var (
	ErrPendingNotFound = errors.New("oauth: pending link not found or expired")
	ErrTooManyAttempts = errors.New("oauth: too many confirmation attempts")
)

type PendingLink struct {
	Provider  string
	Subject   string
	Email     string
	UserID    string
	Next      string
	ExpiresAt time.Time

	attempts int
}

type PendingStore struct {
	mu    sync.Mutex
	links map[string]*PendingLink
}

func NewPendingStore() *PendingStore {
	return &PendingStore{links: make(map[string]*PendingLink)}
}

func (s *PendingStore) Issue(link *PendingLink) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}

	link.Next = SanitizeNext(link.Next)
	link.ExpiresAt = time.Now().Add(PendingTTL)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweepLocked()
	s.links[id] = link

	return id, nil
}

func (s *PendingStore) Get(id string) (*PendingLink, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	link, ok := s.links[id]
	if !ok {
		return nil, false
	}
	if time.Now().After(link.ExpiresAt) {
		delete(s.links, id)
		return nil, false
	}
	return link, true
}

func (s *PendingStore) Attempt(id string) (*PendingLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	link, ok := s.links[id]
	if !ok || time.Now().After(link.ExpiresAt) {
		delete(s.links, id)
		return nil, ErrPendingNotFound
	}

	link.attempts++
	if link.attempts > PendingMaxAttempts {
		delete(s.links, id)
		return nil, ErrTooManyAttempts
	}
	return link, nil
}

func (s *PendingStore) Consume(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.links, id)
}

func (s *PendingStore) sweepLocked() {
	now := time.Now()
	for id, link := range s.links {
		if now.After(link.ExpiresAt) {
			delete(s.links, id)
		}
	}
}
