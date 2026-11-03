package live

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NOTIFY istnieje tylko na Postgresie, więc ten test potrzebuje prawdziwej bazy,
// np. TEST_DATABASE_URL=postgres://ag:ag@localhost:55432/antiginx?sslmode=disable.
func TestPublishReachesListenerOnlyAfterCommit(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("bez TEST_DATABASE_URL nie ma Postgresa, na którym można to sprawdzić")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("połączenie z bazą: %v", err)
	}

	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Po podłączeniu listener budzi wszystkich, więc to znak, że już nasłuchuje.
	ready, stopReady := hub.Subscribe(uuid.New())
	defer stopReady()
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewListener(dsn, hub).Run(ctx)
	}()
	waitFor(t, ready, "podłączenia listenera")

	id := uuid.New()
	wake, unsubscribe := hub.Subscribe(id)
	defer unsubscribe()

	// Cofnięta transakcja nie budzi nikogo.
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := Publish(tx, []uuid.UUID{id}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		return gorm.ErrInvalidTransaction
	}); err == nil {
		t.Fatal("transakcja miała się cofnąć")
	}
	expectSilence(t, wake, "po cofniętej transakcji")

	// Zatwierdzona budzi, ale dopiero po commicie.
	tx := db.Begin()
	if err := Publish(tx, []uuid.UUID{id, id}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	expectSilence(t, wake, "przed commitem")
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit: %v", err)
	}
	waitFor(t, wake, "sygnału po commicie")

	// Sygnał dla innego skanu nie dotyczy tego.
	if err := db.Transaction(func(tx *gorm.DB) error { return Publish(tx, []uuid.UUID{uuid.New()}) }); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	expectSilence(t, wake, "po sygnale dla innego skanu")

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("listener nie zakończył się po końcu kontekstu")
	}
}

func waitFor(t *testing.T, wake <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-wake:
	case <-time.After(5 * time.Second):
		t.Fatalf("nie doczekałem się %s", what)
	}
}

func expectSilence(t *testing.T, wake <-chan struct{}, when string) {
	t.Helper()
	select {
	case <-wake:
		t.Fatalf("sygnał %s", when)
	case <-time.After(150 * time.Millisecond):
	}
}
