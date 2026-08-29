package auth

import (
	"errors"
	"sync"
	"time"
)

const (
	MFATokenTTL      = 5 * time.Minute
	MFAMaxAttempts   = 5
	EnrollmentTTL    = 10 * time.Minute
	EnrollmentMaxTry = 5
)

var (
	ErrChallengeNotFound = errors.New("auth: mfa challenge not found or expired")
	ErrTooManyAttempts   = errors.New("auth: too many attempts")
)

type challenge struct {
	userID    string
	attempts  int
	expiresAt time.Time
}

type MFAStore struct {
	mu         sync.Mutex
	challenges map[string]*challenge
	counters   map[string]*challenge
}

func NewMFAStore() *MFAStore {
	return &MFAStore{
		challenges: make(map[string]*challenge),
		counters:   make(map[string]*challenge),
	}
}

func (s *MFAStore) Issue(id, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweepLocked()
	s.challenges[id] = &challenge{userID: userID, expiresAt: time.Now().Add(MFATokenTTL)}
}

func (s *MFAStore) Attempt(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.challenges[id]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(s.challenges, id)
		return "", ErrChallengeNotFound
	}

	entry.attempts++
	if entry.attempts > MFAMaxAttempts {
		delete(s.challenges, id)
		return "", ErrTooManyAttempts
	}
	return entry.userID, nil
}

func (s *MFAStore) Consume(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.challenges, id)
}

func (s *MFAStore) CountEnrollmentFailure(userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweepLocked()

	entry, ok := s.counters[userID]
	if !ok || time.Now().After(entry.expiresAt) {
		entry = &challenge{userID: userID, expiresAt: time.Now().Add(EnrollmentTTL)}
		s.counters[userID] = entry
	}

	entry.attempts++
	if entry.attempts >= EnrollmentMaxTry {
		delete(s.counters, userID)
		return true
	}
	return false
}

func (s *MFAStore) ResetEnrollmentFailures(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.counters, userID)
}

func (s *MFAStore) sweepLocked() {
	now := time.Now()
	for id, entry := range s.challenges {
		if now.After(entry.expiresAt) {
			delete(s.challenges, id)
		}
	}
	for id, entry := range s.counters {
		if now.After(entry.expiresAt) {
			delete(s.counters, id)
		}
	}
}
