package auth

import (
	"errors"
	"sync"
	"testing"
)

func TestMFAStoreIssueAndAttempt(t *testing.T) {
	store := NewMFAStore()
	store.Issue("challenge-1", "user-1")

	userID, err := store.Attempt("challenge-1")
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	if userID != "user-1" {
		t.Errorf("userID = %q, want %q", userID, "user-1")
	}
}

func TestMFAStoreUnknownChallenge(t *testing.T) {
	store := NewMFAStore()

	if _, err := store.Attempt("nie-ma-takiego"); !errors.Is(err, ErrChallengeNotFound) {
		t.Errorf("err = %v, want ErrChallengeNotFound", err)
	}
}

func TestMFAStoreConsumeMakesChallengeSingleUse(t *testing.T) {
	store := NewMFAStore()
	store.Issue("challenge-1", "user-1")

	if _, err := store.Attempt("challenge-1"); err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	store.Consume("challenge-1")

	if _, err := store.Attempt("challenge-1"); !errors.Is(err, ErrChallengeNotFound) {
		t.Errorf("zużyty token nadal działa, err = %v", err)
	}
}

func TestMFAStoreLimitsAttempts(t *testing.T) {
	store := NewMFAStore()
	store.Issue("challenge-1", "user-1")

	for i := 0; i < MFAMaxAttempts; i++ {
		if _, err := store.Attempt("challenge-1"); err != nil {
			t.Fatalf("próba %d odrzucona przed limitem: %v", i+1, err)
		}
	}

	if _, err := store.Attempt("challenge-1"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("err = %v, want ErrTooManyAttempts", err)
	}

	if _, err := store.Attempt("challenge-1"); !errors.Is(err, ErrChallengeNotFound) {
		t.Errorf("token nie został unieważniony po przekroczeniu limitu, err = %v", err)
	}
}

func TestMFAStoreEnrollmentFailures(t *testing.T) {
	store := NewMFAStore()

	for i := 1; i < EnrollmentMaxTry; i++ {
		if store.CountEnrollmentFailure("user-1") {
			t.Fatalf("enrollment skasowany już przy próbie %d", i)
		}
	}

	if !store.CountEnrollmentFailure("user-1") {
		t.Error("enrollment nie został skasowany po osiągnięciu limitu")
	}
}

func TestMFAStoreEnrollmentFailuresResetPerUser(t *testing.T) {
	store := NewMFAStore()

	for i := 1; i < EnrollmentMaxTry; i++ {
		store.CountEnrollmentFailure("user-1")
	}
	store.ResetEnrollmentFailures("user-1")

	if store.CountEnrollmentFailure("user-1") {
		t.Error("licznik nie został wyzerowany")
	}
}

func TestMFAStoreEnrollmentFailuresAreIndependent(t *testing.T) {
	store := NewMFAStore()

	for i := 1; i < EnrollmentMaxTry; i++ {
		store.CountEnrollmentFailure("user-1")
	}

	if store.CountEnrollmentFailure("user-2") {
		t.Error("próby jednego użytkownika skasowały enrollment innego")
	}
}

func TestMFAStoreIsSafeForConcurrentUse(t *testing.T) {
	store := NewMFAStore()
	store.Issue("challenge-1", "user-1")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.Attempt("challenge-1")
			store.CountEnrollmentFailure("user-1")
		}()
	}
	wg.Wait()
}
