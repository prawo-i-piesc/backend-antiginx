package passkey

import (
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

func TestCeremonyStoreTakeIsSingleUse(t *testing.T) {
	store := NewCeremonyStore()
	store.Put("ceremonia", &Ceremony{Data: webauthn.SessionData{Challenge: "wyzwanie"}})

	ceremony, ok := store.Take("ceremonia")
	if !ok {
		t.Fatal("pierwsze pobranie nie powiodło się")
	}
	if ceremony.Data.Challenge != "wyzwanie" {
		t.Errorf("Challenge = %q", ceremony.Data.Challenge)
	}

	if _, ok := store.Take("ceremonia"); ok {
		t.Error("ta sama ceremonia dała się pobrać drugi raz")
	}
}

func TestCeremonyStoreUnknownID(t *testing.T) {
	store := NewCeremonyStore()

	if _, ok := store.Take("nie-ma-takiej"); ok {
		t.Error("nieznana ceremonia została zwrócona")
	}
	if _, ok := store.Take(""); ok {
		t.Error("pusty identyfikator został przyjęty")
	}
}

func TestCeremonyStoreExpires(t *testing.T) {
	store := NewCeremonyStore()
	ceremony := &Ceremony{}
	store.Put("ceremonia", ceremony)

	ceremony.expiresAt = time.Now().Add(-time.Second)

	if _, ok := store.Take("ceremonia"); ok {
		t.Error("przeterminowana ceremonia została zwrócona")
	}
}

// Ponowne rozpoczęcie rejestracji zastępuje poprzednie wyzwanie, więc
// porzucona ceremonia nie zostaje w pamięci.
func TestCeremonyStorePutReplacesPreviousEntry(t *testing.T) {
	store := NewCeremonyStore()

	store.Put("ceremonia", &Ceremony{Data: webauthn.SessionData{Challenge: "pierwsze"}})
	store.Put("ceremonia", &Ceremony{Data: webauthn.SessionData{Challenge: "drugie"}})

	ceremony, ok := store.Take("ceremonia")
	if !ok {
		t.Fatal("ceremonia zniknęła")
	}
	if ceremony.Data.Challenge != "drugie" {
		t.Errorf("Challenge = %q, want %q", ceremony.Data.Challenge, "drugie")
	}
}

func TestRegistrationKeyIsPerUser(t *testing.T) {
	first, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	second, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}

	if RegistrationKey(first) == RegistrationKey(second) {
		t.Error("dwaj użytkownicy dostali ten sam klucz rejestracji")
	}
	if RegistrationKey(first) != RegistrationKey(first) {
		t.Error("klucz rejestracji nie jest stabilny")
	}
}

func TestNewCeremonyIDIsUnpredictable(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		id, err := NewCeremonyID()
		if err != nil {
			t.Fatalf("NewCeremonyID: %v", err)
		}
		if len(id) < 32 {
			t.Fatalf("identyfikator ma %d znaków, to za mało", len(id))
		}
		if seen[id] {
			t.Fatal("identyfikator ceremonii się powtórzył")
		}
		seen[id] = true
	}
}

func TestCeremonyStoreIsSafeForConcurrentUse(t *testing.T) {
	store := NewCeremonyStore()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := NewCeremonyID()
			if err != nil {
				return
			}
			store.Put(id, &Ceremony{})
			store.Take(id)
		}()
	}
	wg.Wait()
}
