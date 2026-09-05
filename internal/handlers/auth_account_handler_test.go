package handlers

import (
	"path/filepath"
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

	if err := db.AutoMigrate(
		&models.User{}, &models.Session{}, &models.RecoveryCode{},
		&models.OAuthAccount{}, &models.PremiumScan{}, &models.ScanResult{},
	); err != nil {
		t.Fatalf("migracja bazy testowej: %v", err)
	}
	return db
}

func newID(t *testing.T) uuid.UUID {
	t.Helper()

	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return id
}

func seedAccount(t *testing.T, db *gorm.DB, email string) uuid.UUID {
	t.Helper()

	userID := newID(t)
	scanID := newID(t)

	records := []any{
		&models.User{ID: userID, Email: email, Role: models.UserRoleUser, CreatedAt: time.Now()},
		&models.Session{
			ID: newID(t), UserID: userID, FamilyID: newID(t),
			TokenHash: []byte(email + "-session"), ExpiresAt: time.Now().Add(time.Hour),
		},
		&models.RecoveryCode{ID: newID(t), UserID: userID, CodeHash: []byte(email + "-code")},
		&models.OAuthAccount{
			ID: newID(t), UserID: userID,
			Provider: models.ProviderGoogle, ProviderUserID: email + "-sub",
		},
		&models.PremiumScan{ID: scanID, UserID: userID, TargetURL: "https://example.com"},
		&models.ScanResult{ScanID: scanID, TestName: "test", Severity: "low"},
	}

	for _, record := range records {
		if err := db.Create(record).Error; err != nil {
			t.Fatalf("zasianie danych dla %s: %v", email, err)
		}
	}
	return userID
}

func countFor(t *testing.T, db *gorm.DB, model any, userID uuid.UUID) int64 {
	t.Helper()

	var count int64
	if err := db.Model(model).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		t.Fatalf("zliczenie wierszy: %v", err)
	}
	return count
}

func TestDeleteAccountRemovesEverythingOwnedByTheUser(t *testing.T) {
	db := testDB(t)
	userID := seedAccount(t, db, "jan@example.com")

	h := &AuthHandler{db: db}
	if err := h.deleteAccount(userID); err != nil {
		t.Fatalf("deleteAccount: %v", err)
	}

	var users int64
	if err := db.Model(&models.User{}).Where("id = ?", userID).Count(&users).Error; err != nil {
		t.Fatalf("zliczenie użytkowników: %v", err)
	}
	if users != 0 {
		t.Error("konto użytkownika nie zostało usunięte")
	}

	for name, model := range map[string]any{
		"sesje":             &models.Session{},
		"kody odzyskiwania": &models.RecoveryCode{},
		"konta OAuth":       &models.OAuthAccount{},
		"skany premium":     &models.PremiumScan{},
	} {
		if got := countFor(t, db, model, userID); got != 0 {
			t.Errorf("po usunięciu konta zostało %d wierszy w tabeli: %s", got, name)
		}
	}

	var results int64
	if err := db.Model(&models.ScanResult{}).Count(&results).Error; err != nil {
		t.Fatalf("zliczenie wyników: %v", err)
	}
	if results != 0 {
		t.Errorf("zostało %d osieroconych wyników skanów", results)
	}
}

func TestDeleteAccountLeavesOtherUsersAlone(t *testing.T) {
	db := testDB(t)
	victim := seedAccount(t, db, "jan@example.com")
	bystander := seedAccount(t, db, "anna@example.com")

	h := &AuthHandler{db: db}
	if err := h.deleteAccount(victim); err != nil {
		t.Fatalf("deleteAccount: %v", err)
	}

	var users int64
	if err := db.Model(&models.User{}).Where("id = ?", bystander).Count(&users).Error; err != nil {
		t.Fatalf("zliczenie użytkowników: %v", err)
	}
	if users != 1 {
		t.Fatal("usunięto konto innego użytkownika")
	}

	for name, model := range map[string]any{
		"sesje":             &models.Session{},
		"kody odzyskiwania": &models.RecoveryCode{},
		"konta OAuth":       &models.OAuthAccount{},
		"skany premium":     &models.PremiumScan{},
	} {
		if got := countFor(t, db, model, bystander); got != 1 {
			t.Errorf("dane innego użytkownika ucierpiały, tabela %s ma %d wierszy zamiast 1", name, got)
		}
	}

	var results int64
	if err := db.Model(&models.ScanResult{}).Count(&results).Error; err != nil {
		t.Fatalf("zliczenie wyników: %v", err)
	}
	if results != 1 {
		t.Errorf("wyniki skanów innego użytkownika: %d, oczekiwano 1", results)
	}
}

func TestDeleteAccountOnUnknownUser(t *testing.T) {
	db := testDB(t)

	h := &AuthHandler{db: db}
	if err := h.deleteAccount(newID(t)); err != nil {
		t.Errorf("usunięcie nieistniejącego konta zwróciło błąd: %v", err)
	}
}
