package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

const (
	RefreshTokenBytes   = 32
	RotationGracePeriod = 10 * time.Second
)

var (
	ErrSessionNotFound = errors.New("auth: session not found or expired")
	ErrSessionReuse    = errors.New("auth: rotated refresh token reused")
)

type SessionContext struct {
	IP        string
	UserAgent string
	AMR       string
}

type SessionService struct {
	db    *gorm.DB
	ttl   time.Duration
	grace *rotationCache
	locks *keyedMutex
}

func NewSessionService(db *gorm.DB, ttl time.Duration) *SessionService {
	return &SessionService{
		db:    db,
		ttl:   ttl,
		grace: newRotationCache(),
		locks: newKeyedMutex(),
	}
}

func (s *SessionService) Issue(userID uuid.UUID, ctx SessionContext) (string, *models.Session, error) {
	familyID, err := uuid.NewV7()
	if err != nil {
		return "", nil, err
	}
	return s.create(userID, familyID, ctx)
}

func (s *SessionService) Rotate(token string, ctx SessionContext) (string, *models.Session, error) {
	hash := HashRefreshToken(token)
	key := hex.EncodeToString(hash)

	unlock := s.locks.lock(key)
	defer unlock()

	now := time.Now()

	current, err := s.findByHash(hash)
	if err != nil {
		return "", nil, err
	}

	if current.RevokedAt != nil {
		return s.replay(key, current, now)
	}

	if now.After(current.ExpiresAt) {
		return "", nil, ErrSessionNotFound
	}

	result := s.db.Model(&models.Session{}).
		Where("id = ? AND revoked_at IS NULL", current.ID).
		Update("revoked_at", now)
	if result.Error != nil {
		return "", nil, result.Error
	}
	if result.RowsAffected == 0 {
		refreshed, err := s.findByHash(hash)
		if err != nil {
			return "", nil, err
		}
		return s.replay(key, refreshed, now)
	}

	next := ctx
	if next.AMR == "" {
		next.AMR = current.AMR
	}

	newToken, session, err := s.create(current.UserID, current.FamilyID, next)
	if err != nil {
		return "", nil, err
	}

	s.grace.put(key, newToken, now.Add(RotationGracePeriod))
	return newToken, session, nil
}

func (s *SessionService) findByHash(hash []byte) (*models.Session, error) {
	var session models.Session
	if err := s.db.Where("token_hash = ?", hash).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	return &session, nil
}

func (s *SessionService) replay(key string, current *models.Session, now time.Time) (string, *models.Session, error) {
	if token, ok := s.grace.get(key, now); ok {
		var session models.Session
		if err := s.db.Where("token_hash = ?", HashRefreshToken(token)).First(&session).Error; err != nil {
			return "", nil, ErrSessionNotFound
		}
		return token, &session, nil
	}

	if current.RevokedAt != nil && now.Sub(*current.RevokedAt) <= RotationGracePeriod {
		return "", nil, ErrSessionNotFound
	}

	if err := s.RevokeFamily(current.FamilyID); err != nil {
		return "", nil, err
	}
	return "", nil, ErrSessionReuse
}

func (s *SessionService) create(userID, familyID uuid.UUID, ctx SessionContext) (string, *models.Session, error) {
	token, hash, err := GenerateRefreshToken()
	if err != nil {
		return "", nil, err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return "", nil, err
	}

	now := time.Now()
	session := &models.Session{
		ID:         id,
		UserID:     userID,
		FamilyID:   familyID,
		TokenHash:  hash,
		UserAgent:  truncate(ctx.UserAgent, 512),
		IP:         truncate(ctx.IP, 64),
		AMR:        ctx.AMR,
		CreatedAt:  now,
		LastUsedAt: now,
		ExpiresAt:  now.Add(s.ttl),
	}

	if err := s.db.Create(session).Error; err != nil {
		return "", nil, err
	}
	return token, session, nil
}

func (s *SessionService) Revoke(token string) error {
	return s.db.Model(&models.Session{}).
		Where("token_hash = ? AND revoked_at IS NULL", HashRefreshToken(token)).
		Update("revoked_at", time.Now()).Error
}

func (s *SessionService) RevokeAll(userID uuid.UUID) error {
	return s.db.Model(&models.Session{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", time.Now()).Error
}

func (s *SessionService) RevokeFamily(familyID uuid.UUID) error {
	return s.db.Model(&models.Session{}).
		Where("family_id = ? AND revoked_at IS NULL", familyID).
		Update("revoked_at", time.Now()).Error
}

func GenerateRefreshToken() (string, []byte, error) {
	buf := make([]byte, RefreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	return token, HashRefreshToken(token), nil
}

func HashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

type graceEntry struct {
	token     string
	expiresAt time.Time
}

type rotationCache struct {
	mu      sync.Mutex
	entries map[string]graceEntry
}

func newRotationCache() *rotationCache {
	return &rotationCache{entries: make(map[string]graceEntry)}
}

func (c *rotationCache) put(key, token string, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sweepLocked(time.Now())
	c.entries[key] = graceEntry{token: token, expiresAt: expiresAt}
}

func (c *rotationCache) get(key string, now time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok || now.After(entry.expiresAt) {
		return "", false
	}
	return entry.token, true
}

type lockEntry struct {
	mu   sync.Mutex
	refs int
}

type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*lockEntry
}

func newKeyedMutex() *keyedMutex {
	return &keyedMutex{locks: make(map[string]*lockEntry)}
}

func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	entry, ok := k.locks[key]
	if !ok {
		entry = &lockEntry{}
		k.locks[key] = entry
	}
	entry.refs++
	k.mu.Unlock()

	entry.mu.Lock()

	return func() {
		entry.mu.Unlock()

		k.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}

func (c *rotationCache) sweepLocked(now time.Time) {
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}
