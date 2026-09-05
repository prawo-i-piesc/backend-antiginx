package passkey

import (
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
)

func testUser(t *testing.T) *models.User {
	t.Helper()

	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return &models.User{ID: id, Email: "jan@example.com", FullName: "Jan Kowalski"}
}

func TestNewRequiresUserVerification(t *testing.T) {
	instance, err := New("antiginx.pl", "AntiGinx", "https://antiginx.pl")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	selection := instance.Config.AuthenticatorSelection
	if selection.UserVerification != protocol.VerificationRequired {
		t.Errorf("UserVerification = %q, a bez wymogu weryfikacji passkey nie zastępuje drugiego składnika", selection.UserVerification)
	}
	if instance.Config.RPID != "antiginx.pl" {
		t.Errorf("RPID = %q", instance.Config.RPID)
	}
}

// Biblioteka nie sprawdza tych wartości sama, a puste RPID albo origin
// dałyby opcje, które przeglądarka odrzuca bez czytelnego powodu.
func TestNewRejectsIncompleteConfiguration(t *testing.T) {
	if _, err := New("", "AntiGinx", "https://antiginx.pl"); err == nil {
		t.Error("New przyjął pusty RPID")
	}
	if _, err := New("antiginx.pl", "AntiGinx", ""); err == nil {
		t.Error("New przyjął pusty origin")
	}
}

// Uchwyt użytkownika jest zapisany w kluczu na stałe, więc musi być stabilny
// i wyliczalny w obie strony.
func TestUserHandleRoundTrip(t *testing.T) {
	user := testUser(t)
	adapter := NewUser(user, nil)

	handle := adapter.WebAuthnID()
	if len(handle) != 16 {
		t.Fatalf("długość uchwytu = %d, want 16", len(handle))
	}

	recovered, err := UserIDFromHandle(handle)
	if err != nil {
		t.Fatalf("UserIDFromHandle: %v", err)
	}
	if recovered != user.ID {
		t.Errorf("odzyskano %s, oczekiwano %s", recovered, user.ID)
	}
}

func TestUserIDFromHandleRejectsGarbage(t *testing.T) {
	for _, handle := range [][]byte{nil, {1, 2, 3}, make([]byte, 32)} {
		if _, err := UserIDFromHandle(handle); err == nil {
			t.Errorf("przyjęto uchwyt o długości %d", len(handle))
		}
	}
}

func TestUserDisplayNameFallsBackToEmail(t *testing.T) {
	user := testUser(t)
	if got := NewUser(user, nil).WebAuthnDisplayName(); got != "Jan Kowalski" {
		t.Errorf("WebAuthnDisplayName = %q", got)
	}

	user.FullName = ""
	if got := NewUser(user, nil).WebAuthnDisplayName(); got != user.Email {
		t.Errorf("WebAuthnDisplayName = %q, want %q", got, user.Email)
	}
}

func TestCredentialRoundTrip(t *testing.T) {
	original := webauthn.Credential{
		ID:              []byte("identyfikator"),
		PublicKey:       []byte("klucz-publiczny"),
		AttestationType: "none",
		Transport:       []protocol.AuthenticatorTransport{protocol.Internal, protocol.Hybrid},
		Flags:           webauthn.CredentialFlags{BackupEligible: true, BackupState: true},
		Authenticator:   webauthn.Authenticator{AAGUID: []byte("aaguid"), SignCount: 7},
	}

	stored := FromCredential(&original)
	restored := ToCredential(&stored)

	if string(restored.ID) != string(original.ID) {
		t.Errorf("ID = %q", restored.ID)
	}
	if string(restored.PublicKey) != string(original.PublicKey) {
		t.Errorf("PublicKey = %q", restored.PublicKey)
	}
	if restored.AttestationType != original.AttestationType {
		t.Errorf("AttestationType = %q", restored.AttestationType)
	}
	if restored.Authenticator.SignCount != 7 {
		t.Errorf("SignCount = %d", restored.Authenticator.SignCount)
	}
	if !restored.Flags.BackupEligible || !restored.Flags.BackupState {
		t.Error("flagi kopii zapasowej nie przetrwały zapisu")
	}
	if len(restored.Transport) != 2 {
		t.Fatalf("transporty = %v", restored.Transport)
	}
	if restored.Transport[0] != protocol.Internal || restored.Transport[1] != protocol.Hybrid {
		t.Errorf("transporty = %v", restored.Transport)
	}
}

func TestCredentialRoundTripWithoutTransports(t *testing.T) {
	stored := FromCredential(&webauthn.Credential{ID: []byte("x"), PublicKey: []byte("y")})

	if stored.Transports != "" {
		t.Errorf("Transports = %q, want pusty", stored.Transports)
	}
	if got := ToCredential(&stored); len(got.Transport) != 0 {
		t.Errorf("transporty = %v, want brak", got.Transport)
	}
}

func TestNewUserExposesStoredCredentials(t *testing.T) {
	user := testUser(t)
	stored := []models.WebAuthnCredential{
		{CredentialID: []byte("pierwszy"), PublicKey: []byte("a")},
		{CredentialID: []byte("drugi"), PublicKey: []byte("b")},
	}

	credentials := NewUser(user, stored).WebAuthnCredentials()
	if len(credentials) != 2 {
		t.Fatalf("zwrócono %d kluczy, oczekiwano 2", len(credentials))
	}
	if string(credentials[0].ID) != "pierwszy" || string(credentials[1].ID) != "drugi" {
		t.Error("klucze zwrócone w innej kolejności niż zapisane")
	}
}
