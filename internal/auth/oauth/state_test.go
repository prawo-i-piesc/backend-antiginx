package oauth

import (
	"sync"
	"testing"
	"time"
)

func TestIssueProducesDistinctSecrets(t *testing.T) {
	store := NewStateStore()

	firstID, firstFlow, err := store.Issue("google", "/dashboard", "")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	secondID, secondFlow, err := store.Issue("google", "/dashboard", "")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if firstID == secondID {
		t.Error("dwa przepływy dostały ten sam identyfikator ciasteczka")
	}
	if firstFlow.State == secondFlow.State {
		t.Error("dwa przepływy dostały ten sam state")
	}
	if firstFlow.Verifier == secondFlow.Verifier {
		t.Error("dwa przepływy dostały ten sam code_verifier")
	}
	if firstID == firstFlow.State {
		t.Error("identyfikator ciasteczka jest równy state, a mają być dwoma niezależnymi sekretami")
	}
}

func TestIssueSanitizesNext(t *testing.T) {
	store := NewStateStore()

	_, flow, err := store.Issue("google", "https://evil.pl", "")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if flow.Next != DefaultNext {
		t.Errorf("Next = %q, want %q", flow.Next, DefaultNext)
	}
}

func TestConsumeIsSingleUse(t *testing.T) {
	store := NewStateStore()

	id, _, err := store.Issue("google", "/dashboard", "")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, ok := store.Consume(id); !ok {
		t.Fatal("pierwsze użycie stanu nie powiodło się")
	}
	if _, ok := store.Consume(id); ok {
		t.Error("stan dał się użyć drugi raz")
	}
}

func TestConsumeUnknownID(t *testing.T) {
	store := NewStateStore()

	if _, ok := store.Consume("nie-ma-takiego"); ok {
		t.Error("nieznany identyfikator został przyjęty")
	}
	if _, ok := store.Consume(""); ok {
		t.Error("pusty identyfikator został przyjęty")
	}
}

func TestConsumeRejectsExpiredFlow(t *testing.T) {
	store := NewStateStore()

	id, flow, err := store.Issue("google", "/dashboard", "")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	flow.ExpiresAt = time.Now().Add(-time.Second)

	if _, ok := store.Consume(id); ok {
		t.Error("przeterminowany stan został przyjęty")
	}
}

func TestMatchesState(t *testing.T) {
	store := NewStateStore()

	_, flow, err := store.Issue("google", "/dashboard", "")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if !flow.MatchesState(flow.State) {
		t.Error("poprawny state nie został rozpoznany")
	}
	for _, wrong := range []string{"", "cokolwiek", flow.State + "x", flow.State[:len(flow.State)-1]} {
		if flow.MatchesState(wrong) {
			t.Errorf("niepoprawny state %q został przyjęty", wrong)
		}
	}
}

func TestIsLink(t *testing.T) {
	store := NewStateStore()

	_, loginFlow, err := store.Issue("google", "/dashboard", "")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if loginFlow.IsLink() {
		t.Error("przepływ logowania został uznany za wiązanie konta")
	}

	_, linkFlow, err := store.Issue("google", "/profile", "user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !linkFlow.IsLink() {
		t.Error("przepływ wiązania nie został rozpoznany")
	}
	if linkFlow.UserID != "user-1" {
		t.Errorf("UserID = %q", linkFlow.UserID)
	}
}

func TestStateStoreIsSafeForConcurrentUse(t *testing.T) {
	store := NewStateStore()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, _, err := store.Issue("google", "/dashboard", "")
			if err != nil {
				return
			}
			store.Consume(id)
		}()
	}
	wg.Wait()
}
