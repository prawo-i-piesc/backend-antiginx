package live

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAcquireEnforcesPerClientLimitAndReleases(t *testing.T) {
	hub := NewHub()

	releases := make([]func(), 0, maxPerClient)
	for range maxPerClient {
		release, ok := hub.Acquire("ip:1")
		if !ok {
			t.Fatal("limit klienta zadziałał za wcześnie")
		}
		releases = append(releases, release)
	}
	if _, ok := hub.Acquire("ip:1"); ok {
		t.Fatal("przyjęto połączenie ponad limit klienta")
	}
	if _, ok := hub.Acquire("ip:2"); !ok {
		t.Fatal("limit jednego klienta nie powinien dotykać innych")
	}

	releases[0]()
	if _, ok := hub.Acquire("ip:1"); !ok {
		t.Fatal("miejsce po zwolnieniu powinno wrócić")
	}
}

func TestAcquireEnforcesGlobalLimit(t *testing.T) {
	hub := NewHub()
	for i := range maxConnections {
		if _, ok := hub.Acquire(fmt.Sprintf("ip:%d", i)); !ok {
			t.Fatalf("limit ogólny zadziałał przy %d", i)
		}
	}
	if _, ok := hub.Acquire("ip:new"); ok {
		t.Fatal("przyjęto połączenie ponad limit ogólny")
	}
}

func TestNotifyCoalescesAndReachesOnlyThatScan(t *testing.T) {
	hub := NewHub()
	watched, other := uuid.New(), uuid.New()

	wake, unsubscribe := hub.Subscribe(watched)
	defer unsubscribe()
	wakeOther, unsubscribeOther := hub.Subscribe(other)
	defer unsubscribeOther()

	hub.Notify(watched)
	hub.Notify(watched)
	hub.Notify(watched)

	select {
	case <-wake:
	default:
		t.Fatal("sygnał nie dotarł")
	}
	select {
	case <-wake:
		t.Fatal("kilka zmian naraz powinno dać jeden odczyt")
	default:
	}
	select {
	case <-wakeOther:
		t.Fatal("sygnał dotarł do innego skanu")
	default:
	}

	hub.NotifyAll()
	select {
	case <-wakeOther:
	default:
		t.Fatal("NotifyAll nie obudził wszystkich")
	}
}

func TestUnsubscribeForgetsTheScan(t *testing.T) {
	hub := NewHub()
	scanID := uuid.New()

	_, unsubscribe := hub.Subscribe(scanID)
	unsubscribe()

	if len(hub.subscribers) != 0 {
		t.Fatalf("po wypisaniu zostało %d wpisów, mapa rosłaby bez końca", len(hub.subscribers))
	}
	hub.Notify(scanID) // nikt nie czeka: nie może się wywrócić
}

func TestShutdownRefusesNewConnectionsAndWaitFollowsReleases(t *testing.T) {
	hub := NewHub()
	release, ok := hub.Acquire("ip:1")
	if !ok {
		t.Fatal("Acquire")
	}

	hub.Shutdown()
	hub.Shutdown() // drugie wywołanie nie może panikować

	select {
	case <-hub.Done():
	default:
		t.Fatal("Done nie zamknął się po Shutdown")
	}
	if _, ok := hub.Acquire("ip:2"); ok {
		t.Fatal("przyjęto połączenie w trakcie zamykania")
	}

	waited := make(chan struct{})
	go func() {
		hub.Wait(context.Background())
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("Wait wrócił przed zwolnieniem połączenia")
	case <-time.After(20 * time.Millisecond):
	}

	release()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("Wait nie wrócił po zwolnieniu połączenia")
	}
}

func TestWaitGivesUpWhenContextEnds(t *testing.T) {
	hub := NewHub()
	if _, ok := hub.Acquire("ip:1"); !ok {
		t.Fatal("Acquire")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		hub.Wait(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait nie respektuje końca kontekstu")
	}
}
