// Package live obsługuje podgląd skanu na żywo przez WebSocket.
//
// Źródłem prawdy zostaje baza: kolejne wyniki czyta z niej każde połączenie, a
// sygnał o zmianie mówi tylko, że warto sprawdzić już teraz. Zgubiony sygnał
// opóźnia więc wynik o interwał zapasowego odczytu, ale go nie gubi, a wolny
// klient nie zapełnia żadnego bufora po stronie serwera.
package live

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// Limity chronią bazę i pamięć: każde połączenie to goroutine i regularne
// odczyty, a darmowy podgląd nie wymaga konta.
const (
	maxConnections = 500
	maxPerClient   = 12
)

// Hub trzyma, kto czeka na zmiany którego skanu i ile jest otwartych połączeń.
type Hub struct {
	mu          sync.Mutex
	subscribers map[uuid.UUID]map[chan struct{}]struct{}
	perClient   map[string]int
	total       int

	closing  sync.Once
	done     chan struct{}
	finished *sync.Cond
}

func NewHub() *Hub {
	h := &Hub{
		subscribers: make(map[uuid.UUID]map[chan struct{}]struct{}),
		perClient:   make(map[string]int),
		done:        make(chan struct{}),
	}
	h.finished = sync.NewCond(&h.mu)
	return h
}

// Acquire rezerwuje miejsce na połączenie klienta (adres albo konto). Zwraca
// false, gdy limit jest wyczerpany albo serwer się zamyka.
func (h *Hub) Acquire(client string) (release func(), ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	select {
	case <-h.done:
		return nil, false
	default:
	}
	if h.total >= maxConnections || h.perClient[client] >= maxPerClient {
		return nil, false
	}
	h.total++
	h.perClient[client]++

	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.total--
		if h.perClient[client]--; h.perClient[client] == 0 {
			delete(h.perClient, client)
		}
		if h.total == 0 {
			h.finished.Broadcast()
		}
	}, true
}

// Subscribe zwraca kanał, który budzi się po zmianie skanu. Ma bufor na jeden
// sygnał: kilka zmian naraz to jeden odczyt, bo i tak czyta się wszystko.
func (h *Hub) Subscribe(scanID uuid.UUID) (<-chan struct{}, func()) {
	wake := make(chan struct{}, 1)

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subscribers[scanID] == nil {
		h.subscribers[scanID] = make(map[chan struct{}]struct{})
	}
	h.subscribers[scanID][wake] = struct{}{}

	return wake, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subscribers[scanID], wake)
		if len(h.subscribers[scanID]) == 0 {
			delete(h.subscribers, scanID)
		}
	}
}

// Notify budzi wszystkich czekających na ten skan.
func (h *Hub) Notify(scanID uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for wake := range h.subscribers[scanID] {
		wakeUp(wake)
	}
}

// NotifyAll budzi wszystkich, po przerwie, w której sygnały mogły przepaść.
func (h *Hub) NotifyAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, subscribers := range h.subscribers {
		for wake := range subscribers {
			wakeUp(wake)
		}
	}
}

func wakeUp(wake chan struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

// Done zamyka się, gdy serwer kończy pracę.
func (h *Hub) Done() <-chan struct{} {
	return h.done
}

// Shutdown każe połączeniom się zamknąć. http.Server.Shutdown ich nie obejmuje:
// po przejęciu połączenia WebSocketem serwer już o nim nie wie.
func (h *Hub) Shutdown() {
	h.closing.Do(func() { close(h.done) })
}

// Wait czeka, aż połączenia się zamkną, albo na koniec ctx.
func (h *Hub) Wait(ctx context.Context) {
	stop := context.AfterFunc(ctx, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.finished.Broadcast()
	})
	defer stop()

	h.mu.Lock()
	defer h.mu.Unlock()
	for h.total > 0 && ctx.Err() == nil {
		h.finished.Wait()
	}
}
