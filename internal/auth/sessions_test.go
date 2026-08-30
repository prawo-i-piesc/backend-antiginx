package auth

import (
	"encoding/hex"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Discard,
	})
	if err != nil {
		t.Fatalf("otwarcie bazy testowej: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("uchwyt do bazy: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if err := db.AutoMigrate(&models.User{}, &models.Session{}); err != nil {
		t.Fatalf("migracja bazy testowej: %v", err)
	}
	return db
}

func testUser(t *testing.T, db *gorm.DB) *models.User {
	t.Helper()

	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	user := &models.User{ID: id, Email: "jan@example.com", Role: models.UserRoleUser}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("utworzenie użytkownika: %v", err)
	}
	return user
}

func newService(t *testing.T) (*SessionService, *gorm.DB, *models.User) {
	t.Helper()

	db := testDB(t)
	return NewSessionService(db, time.Hour), db, testUser(t, db)
}

func loadSession(t *testing.T, db *gorm.DB, token string) models.Session {
	t.Helper()

	var session models.Session
	if err := db.Where("token_hash = ?", HashRefreshToken(token)).First(&session).Error; err != nil {
		t.Fatalf("sesja dla tokenu nie istnieje: %v", err)
	}
	return session
}

func TestIssueStoresOnlyTheHash(t *testing.T) {
	service, db, user := newService(t)

	token, session, err := service.Issue(user.ID, SessionContext{AMR: models.AMRPassword, IP: "10.0.0.1"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token == "" {
		t.Fatal("pusty token")
	}

	stored := loadSession(t, db, token)
	if string(stored.TokenHash) == token {
		t.Error("token zapisany w bazie w postaci jawnej")
	}
	if stored.ID != session.ID || stored.UserID != user.ID {
		t.Error("sesja zapisana z innymi danymi niż zwrócone")
	}
	if stored.RevokedAt != nil {
		t.Error("nowa sesja jest od razu unieważniona")
	}
	if stored.AMR != models.AMRPassword {
		t.Errorf("AMR = %q, want %q", stored.AMR, models.AMRPassword)
	}
}

func TestIssueGeneratesDistinctTokens(t *testing.T) {
	service, _, user := newService(t)

	first, _, err := service.Issue(user.ID, SessionContext{})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	second, _, err := service.Issue(user.ID, SessionContext{})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if first == second {
		t.Error("dwie sesje dostały ten sam token")
	}
}

func TestRotateReplacesTokenAndKeepsFamily(t *testing.T) {
	service, db, user := newService(t)

	token, original, err := service.Issue(user.ID, SessionContext{AMR: models.AMRPassword})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	newToken, rotated, err := service.Rotate(token, SessionContext{})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if newToken == token {
		t.Fatal("rotacja zwróciła ten sam token")
	}
	if rotated.FamilyID != original.FamilyID {
		t.Error("rotacja zmieniła rodzinę sesji")
	}
	if rotated.AMR != models.AMRPassword {
		t.Errorf("AMR = %q, rotacja powinna przenieść AMR", rotated.AMR)
	}

	old := loadSession(t, db, token)
	if old.RevokedAt == nil {
		t.Error("stara sesja nie została unieważniona")
	}
}

func TestRotateRejectsUnknownToken(t *testing.T) {
	service, _, _ := newService(t)

	if _, _, err := service.Rotate("nie-ma-takiego-tokenu", SessionContext{}); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestRotateRejectsExpiredSession(t *testing.T) {
	db := testDB(t)
	user := testUser(t, db)
	service := NewSessionService(db, -time.Minute)

	token, _, err := service.Issue(user.ID, SessionContext{})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, _, err := service.Rotate(token, SessionContext{}); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestRotateWithinGraceWindowReturnsTheSameToken(t *testing.T) {
	service, _, user := newService(t)

	token, _, err := service.Issue(user.ID, SessionContext{})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	first, _, err := service.Rotate(token, SessionContext{})
	if err != nil {
		t.Fatalf("pierwsza rotacja: %v", err)
	}

	second, session, err := service.Rotate(token, SessionContext{})
	if err != nil {
		t.Fatalf("druga rotacja w oknie tolerancji zwróciła błąd: %v", err)
	}
	if second != first {
		t.Errorf("druga rotacja zwróciła inny token: %q vs %q", second, first)
	}
	if session == nil {
		t.Fatal("brak sesji w odpowiedzi")
	}
}

func TestRotateConcurrentlyDoesNotLogTheUserOut(t *testing.T) {
	service, _, user := newService(t)

	token, _, err := service.Issue(user.ID, SessionContext{})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	const callers = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		tokens  []string
		failure error
	)

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _, err := service.Rotate(token, SessionContext{})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failure = err
				return
			}
			tokens = append(tokens, got)
		}()
	}
	wg.Wait()

	if failure != nil {
		t.Fatalf("równoległa rotacja zwróciła błąd: %v", failure)
	}
	if len(tokens) != callers {
		t.Fatalf("odpowiedzi: %d, oczekiwano %d", len(tokens), callers)
	}
	for _, got := range tokens {
		if got != tokens[0] {
			t.Fatal("równoległe rotacje zwróciły różne tokeny")
		}
	}
}

func TestRotateAfterGraceWindowDetectsReuse(t *testing.T) {
	service, db, user := newService(t)

	token, original, err := service.Issue(user.ID, SessionContext{})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	newToken, _, err := service.Rotate(token, SessionContext{})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	expireGrace(t, service, db, token)

	if _, _, err := service.Rotate(token, SessionContext{}); !errors.Is(err, ErrSessionReuse) {
		t.Fatalf("err = %v, want ErrSessionReuse", err)
	}

	successor := loadSession(t, db, newToken)
	if successor.RevokedAt == nil {
		t.Error("następca nie został unieważniony razem z rodziną")
	}

	var active int64
	if err := db.Model(&models.Session{}).
		Where("family_id = ? AND revoked_at IS NULL", original.FamilyID).
		Count(&active).Error; err != nil {
		t.Fatalf("zliczenie sesji: %v", err)
	}
	if active != 0 {
		t.Errorf("po wykryciu ponownego użycia zostało %d aktywnych sesji rodziny", active)
	}
}

func expireGrace(t *testing.T, service *SessionService, db *gorm.DB, token string) {
	t.Helper()

	hash := HashRefreshToken(token)
	past := time.Now().Add(-RotationGracePeriod - time.Minute)
	if err := db.Model(&models.Session{}).
		Where("token_hash = ?", hash).
		Update("revoked_at", past).Error; err != nil {
		t.Fatalf("cofnięcie revoked_at: %v", err)
	}

	service.grace.mu.Lock()
	delete(service.grace.entries, hex.EncodeToString(hash))
	service.grace.mu.Unlock()
}

func TestRevokeMakesTokenUnusable(t *testing.T) {
	service, _, user := newService(t)

	token, _, err := service.Issue(user.ID, SessionContext{})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := service.Revoke(token); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if _, _, err := service.Rotate(token, SessionContext{}); err == nil {
		t.Error("unieważniony token nadal daje się zrotować")
	}
}

func TestRevokeAllEndsEveryDevice(t *testing.T) {
	service, db, user := newService(t)

	var tokens []string
	for i := 0; i < 3; i++ {
		token, _, err := service.Issue(user.ID, SessionContext{})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		tokens = append(tokens, token)
	}

	if err := service.RevokeAll(user.ID); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}

	var active int64
	if err := db.Model(&models.Session{}).
		Where("user_id = ? AND revoked_at IS NULL", user.ID).
		Count(&active).Error; err != nil {
		t.Fatalf("zliczenie sesji: %v", err)
	}
	if active != 0 {
		t.Errorf("zostało %d aktywnych sesji", active)
	}

	for _, token := range tokens {
		if _, _, err := service.Rotate(token, SessionContext{}); err == nil {
			t.Error("token po RevokeAll nadal działa")
		}
	}
}

func TestRevokeAllLeavesOtherUsersAlone(t *testing.T) {
	service, db, user := newService(t)

	otherID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	other := &models.User{ID: otherID, Email: "ktos@example.com", Role: models.UserRoleUser}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("utworzenie użytkownika: %v", err)
	}

	if _, _, err := service.Issue(user.ID, SessionContext{}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	otherToken, _, err := service.Issue(other.ID, SessionContext{})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := service.RevokeAll(user.ID); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}

	if _, _, err := service.Rotate(otherToken, SessionContext{}); err != nil {
		t.Errorf("sesja innego użytkownika została unieważniona: %v", err)
	}
}

func TestGenerateRefreshTokenMatchesItsHash(t *testing.T) {
	token, hash, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken: %v", err)
	}

	if string(hash) != string(HashRefreshToken(token)) {
		t.Error("zwrócony hasz nie odpowiada tokenowi")
	}
	if len(hash) != 32 {
		t.Errorf("długość hasza = %d, want 32", len(hash))
	}
}
