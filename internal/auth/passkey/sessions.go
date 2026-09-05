package passkey

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

const (
	CeremonyTTL = 5 * time.Minute
	idBytes     = 32
)

type Ceremony struct {
	Data         webauthn.SessionData
	UserID       uuid.UUID
	Discoverable bool

	expiresAt time.Time
}

type CeremonyStore struct {
	mu         sync.Mutex
	ceremonies map[string]*Ceremony
}

func NewCeremonyStore() *CeremonyStore {
	return &CeremonyStore{ceremonies: make(map[string]*Ceremony)}
}

func (s *CeremonyStore) Put(id string, ceremony *Ceremony) {
	ceremony.expiresAt = time.Now().Add(CeremonyTTL)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweepLocked()
	s.ceremonies[id] = ceremony
}

func (s *CeremonyStore) Take(id string) (*Ceremony, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ceremony, ok := s.ceremonies[id]
	if !ok {
		return nil, false
	}

	delete(s.ceremonies, id)

	if time.Now().After(ceremony.expiresAt) {
		return nil, false
	}
	return ceremony, true
}

func (s *CeremonyStore) sweepLocked() {
	now := time.Now()
	for id, ceremony := range s.ceremonies {
		if now.After(ceremony.expiresAt) {
			delete(s.ceremonies, id)
		}
	}
}

func RegistrationKey(userID uuid.UUID) string {
	return "registration:" + userID.String()
}

func NewCeremonyID() (string, error) {
	buf := make([]byte, idBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
