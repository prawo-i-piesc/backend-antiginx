package handlers

import (
	"testing"
	"time"

	"github.com/prawo-i-piesc/backend/internal/auth/oauth"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

func seedUserForOAuth(t *testing.T, db *gorm.DB, email string, password []byte, verified bool) *models.User {
	t.Helper()

	user := &models.User{
		ID:            newID(t),
		FullName:      "Jan Kowalski",
		Email:         email,
		Role:          models.UserRoleUser,
		CreatedAt:     time.Now(),
		Password:      password,
		EmailVerified: verified,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("utworzenie użytkownika: %v", err)
	}
	return user
}

// Konto założone hasłem ma niepotwierdzony adres — ktoś mógł zająć cudzy,
// czekając na jego pierwsze logowanie providerem. Takie wymaga hasła.
func TestResolveOAuthUserAsksForPasswordOnUnverifiedAccount(t *testing.T) {
	db := testDB(t)
	h := passkeyHandler(t, db)
	seedUserForOAuth(t, db, "jan@example.com", []byte("hasz"), false)

	profile := &oauth.Profile{Subject: "gh-1", Email: "jan@example.com", EmailVerified: true}
	user, needsConfirmation, failure := h.resolveOAuthUser(models.ProviderGitHub, profile, "jan@example.com")

	if failure != "" {
		t.Fatalf("resolveOAuthUser: nieoczekiwany błąd %q", failure)
	}
	if user == nil || !needsConfirmation {
		t.Fatalf("needsConfirmation = %v, want true", needsConfirmation)
	}
}

// Konto założone przez providera ma adres potwierdzony przez niego, a drugi
// provider potwierdza ten sam adres. Żądanie hasła zamykałoby je w ślepym
// zaułku, bo hasła nie ma i nie da się go podać.
func TestResolveOAuthUserLinksVerifiedAccountWithoutPassword(t *testing.T) {
	db := testDB(t)
	h := passkeyHandler(t, db)
	seedUserForOAuth(t, db, "jan@example.com", nil, true)

	profile := &oauth.Profile{Subject: "gh-1", Email: "jan@example.com", EmailVerified: true}
	user, needsConfirmation, failure := h.resolveOAuthUser(models.ProviderGitHub, profile, "jan@example.com")

	if failure != "" {
		t.Fatalf("resolveOAuthUser: nieoczekiwany błąd %q", failure)
	}
	if user == nil {
		t.Fatal("resolveOAuthUser nie zwrócił użytkownika")
	}
	if needsConfirmation {
		t.Fatal("konto bez hasła nie ma czym potwierdzić, więc nie może być o to proszone")
	}
}

func TestResolveOAuthUserCreatesAccountWhenEmailIsFree(t *testing.T) {
	db := testDB(t)
	h := passkeyHandler(t, db)

	profile := &oauth.Profile{
		Subject:       "gh-2",
		Email:         "nowy@example.com",
		EmailVerified: true,
		FullName:      "Nowy User",
	}
	user, needsConfirmation, failure := h.resolveOAuthUser(models.ProviderGitHub, profile, "nowy@example.com")

	if failure != "" {
		t.Fatalf("resolveOAuthUser: nieoczekiwany błąd %q", failure)
	}
	if needsConfirmation {
		t.Fatal("nowe konto nie ma czego potwierdzać")
	}
	if user == nil || user.Email != "nowy@example.com" {
		t.Fatalf("resolveOAuthUser zwrócił %+v", user)
	}
}
