package oauth

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func newPending() *PendingLink {
	return &PendingLink{
		Provider: "google",
		Subject:  "google-sub-1",
		Email:    "jan@example.com",
		UserID:   "user-1",
		Next:     "/dashboard",
	}
}

func TestPendingIssueAndGet(t *testing.T) {
	store := NewPendingStore()

	id, err := store.Issue(newPending())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	link, ok := store.Get(id)
	if !ok {
		t.Fatal("świeżo utworzone powiązanie nie zostało znalezione")
	}
	if link.Email != "jan@example.com" || link.Subject != "google-sub-1" {
		t.Errorf("dane powiązania się nie zgadzają: %+v", link)
	}
	if link.ExpiresAt.IsZero() {
		t.Error("nie ustawiono czasu wygaśnięcia")
	}
}

func TestPendingGetDoesNotConsume(t *testing.T) {
	store := NewPendingStore()

	id, err := store.Issue(newPending())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, ok := store.Get(id); !ok {
		t.Fatal("pierwszy odczyt nie powiódł się")
	}
	if _, ok := store.Get(id); !ok {
		t.Error("odczyt skonsumował powiązanie, a ekran potwierdzenia może je czytać wielokrotnie")
	}
}

func TestPendingIssueSanitizesNext(t *testing.T) {
	store := NewPendingStore()

	pending := newPending()
	pending.Next = "https://attacker.invalid"
	id, err := store.Issue(pending)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	link, _ := store.Get(id)
	if link.Next != DefaultNext {
		t.Errorf("Next = %q, want %q", link.Next, DefaultNext)
	}
}

func TestPendingConsume(t *testing.T) {
	store := NewPendingStore()

	id, err := store.Issue(newPending())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	store.Consume(id)

	if _, ok := store.Get(id); ok {
		t.Error("skonsumowane powiązanie nadal istnieje")
	}
	if _, err := store.Attempt(id); !errors.Is(err, ErrPendingNotFound) {
		t.Errorf("err = %v, want ErrPendingNotFound", err)
	}
}

func TestPendingUnknownID(t *testing.T) {
	store := NewPendingStore()

	if _, ok := store.Get("nie-ma-takiego"); ok {
		t.Error("nieznany identyfikator został przyjęty")
	}
	if _, ok := store.Get(""); ok {
		t.Error("pusty identyfikator został przyjęty")
	}
	if _, err := store.Attempt(""); !errors.Is(err, ErrPendingNotFound) {
		t.Errorf("err = %v, want ErrPendingNotFound", err)
	}
}

func TestPendingExpires(t *testing.T) {
	store := NewPendingStore()

	id, err := store.Issue(newPending())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	link, _ := store.Get(id)
	link.ExpiresAt = time.Now().Add(-time.Second)

	if _, ok := store.Get(id); ok {
		t.Error("przeterminowane powiązanie zostało zwrócone")
	}
}

// Ekran potwierdzenia zna adres e-mail konta, więc bez limitu prób byłby
// wygodnym miejscem na zgadywanie hasła.
func TestPendingLimitsAttempts(t *testing.T) {
	store := NewPendingStore()

	id, err := store.Issue(newPending())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for i := 0; i < PendingMaxAttempts; i++ {
		if _, err := store.Attempt(id); err != nil {
			t.Fatalf("próba %d odrzucona przed limitem: %v", i+1, err)
		}
	}

	if _, err := store.Attempt(id); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("err = %v, want ErrTooManyAttempts", err)
	}
	if _, ok := store.Get(id); ok {
		t.Error("powiązanie przetrwało przekroczenie limitu prób")
	}
}

func TestPendingIssueProducesDistinctIDs(t *testing.T) {
	store := NewPendingStore()

	first, err := store.Issue(newPending())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	second, err := store.Issue(newPending())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if first == second {
		t.Error("dwa powiązania dostały ten sam identyfikator")
	}
}

func TestPendingStoreIsSafeForConcurrentUse(t *testing.T) {
	store := NewPendingStore()

	id, err := store.Issue(newPending())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.Get(id)
			_, _ = store.Attempt(id)
			newID, err := store.Issue(newPending())
			if err == nil {
				store.Consume(newID)
			}
		}()
	}
	wg.Wait()
}
