package results

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// fakeAcker zapisuje, co consumer zrobił z dostawami.
type fakeAcker struct {
	mu   sync.Mutex
	ops  []string
	done chan struct{}
}

func newFakeAcker() *fakeAcker {
	return &fakeAcker{done: make(chan struct{}, 16)}
}

func (a *fakeAcker) record(op string) {
	a.mu.Lock()
	a.ops = append(a.ops, op)
	a.mu.Unlock()
	a.done <- struct{}{}
}

func (a *fakeAcker) Ack(tag uint64, multiple bool) error {
	a.record(fmt.Sprintf("ack %d multiple=%t", tag, multiple))
	return nil
}

func (a *fakeAcker) Nack(tag uint64, multiple, requeue bool) error {
	a.record(fmt.Sprintf("nack %d multiple=%t requeue=%t", tag, multiple, requeue))
	return nil
}

func (a *fakeAcker) Reject(tag uint64, requeue bool) error {
	a.record(fmt.Sprintf("reject %d requeue=%t", tag, requeue))
	return nil
}

func (a *fakeAcker) wait(t *testing.T, n int) []string {
	t.Helper()
	for range n {
		select {
		case <-a.done:
		case <-time.After(2 * time.Second):
			t.Fatalf("czekam na %d operacji, mam %v", n, a.snapshot())
		}
	}
	return a.snapshot()
}

func (a *fakeAcker) snapshot() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.ops...)
}

func delivery(a *fakeAcker, tag uint64, raw []byte) amqp.Delivery {
	return amqp.Delivery{Acknowledger: a, DeliveryTag: tag, Body: raw}
}

type recordingSaver struct {
	mu      sync.Mutex
	batches [][]message
	err     error
}

func (s *recordingSaver) save(_ context.Context, batch []message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, append([]message(nil), batch...))
	return s.err
}

func (s *recordingSaver) sizes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sizes []int
	for _, b := range s.batches {
		sizes = append(sizes, len(b))
	}
	return sizes
}

func runCollect(t *testing.T, deliveries chan amqp.Delivery, size int, save saveFunc[message]) (context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() { errs <- collect(ctx, deliveries, size, decode, save) }()
	t.Cleanup(cancel)
	return cancel, errs
}

func TestCollectAcksFullBatchWithOneMultipleAck(t *testing.T) {
	acker := newFakeAcker()
	saver := &recordingSaver{}
	deliveries := make(chan amqp.Delivery, 3)
	runCollect(t, deliveries, 3, saver.save)

	scanID := newID(t)
	for tag := uint64(1); tag <= 3; tag++ {
		deliveries <- delivery(acker, tag, body(scanID, "https", "Info", false, Success))
	}

	if ops := acker.wait(t, 1); ops[0] != "ack 3 multiple=true" {
		t.Errorf("ops = %v, want jedno ack 3 multiple=true", ops)
	}
	if sizes := saver.sizes(); len(sizes) != 1 || sizes[0] != 3 {
		t.Errorf("paczki = %v, want [3]", sizes)
	}
}

func TestCollectFlushesPartialBatchOnTick(t *testing.T) {
	acker := newFakeAcker()
	saver := &recordingSaver{}
	deliveries := make(chan amqp.Delivery, 1)
	runCollect(t, deliveries, 100, saver.save)

	deliveries <- delivery(acker, 7, body(newID(t), "https", "Info", false, Success))

	if ops := acker.wait(t, 1); ops[0] != "ack 7 multiple=true" {
		t.Errorf("ops = %v, want ack 7 multiple=true", ops)
	}
}

func TestCollectRejectsUnreadableMessagesWithoutRequeue(t *testing.T) {
	acker := newFakeAcker()
	saver := &recordingSaver{}
	deliveries := make(chan amqp.Delivery, 2)
	runCollect(t, deliveries, 1, saver.save)

	deliveries <- delivery(acker, 1, []byte(`nie json`))
	deliveries <- delivery(acker, 2, body(newID(t), "https", "Info", false, Success))

	ops := acker.wait(t, 2)
	if ops[0] != "reject 1 requeue=false" || ops[1] != "ack 2 multiple=true" {
		t.Errorf("ops = %v, want reject 1, potem ack 2", ops)
	}
	if sizes := saver.sizes(); len(sizes) != 1 || sizes[0] != 1 {
		t.Errorf("paczki = %v, want [1] bez nieczytelnej wiadomości", sizes)
	}
}

func TestCollectRequeuesWholeBatchWhenSaveFails(t *testing.T) {
	minRetryDelay = time.Millisecond
	t.Cleanup(func() { minRetryDelay = time.Second })

	acker := newFakeAcker()
	saver := &recordingSaver{err: errors.New("baza leży")}
	deliveries := make(chan amqp.Delivery, 2)
	runCollect(t, deliveries, 2, saver.save)

	scanID := newID(t)
	deliveries <- delivery(acker, 1, body(scanID, "https", "Info", false, Success))
	deliveries <- delivery(acker, 2, body(scanID, "hsts", "Info", false, Success))

	if ops := acker.wait(t, 1); ops[0] != "nack 2 multiple=true requeue=true" {
		t.Errorf("ops = %v, want nack 2 multiple=true requeue=true", ops)
	}
}

func TestCollectFlushesOnShutdown(t *testing.T) {
	acker := newFakeAcker()
	saver := &recordingSaver{}
	// Kanał bez bufora: po wysłaniu wiadomość na pewno jest już w paczce,
	// więc po anulowaniu collect musi ją zapisać i potwierdzić przed wyjściem.
	deliveries := make(chan amqp.Delivery)
	cancel, errs := runCollect(t, deliveries, 100, saver.save)

	deliveries <- delivery(acker, 1, body(newID(t), "https", "Info", false, Success))
	cancel()

	if err := <-errs; err != nil {
		t.Errorf("collect = %v, want nil po anulowaniu", err)
	}
	if ops := acker.snapshot(); len(ops) != 1 || ops[0] != "ack 1 multiple=true" {
		t.Errorf("ops = %v, want ack 1 przed wyjściem", ops)
	}
}

func TestCollectStopsWhenDeliveriesClose(t *testing.T) {
	deliveries := make(chan amqp.Delivery)
	_, errs := runCollect(t, deliveries, 10, (&recordingSaver{}).save)

	close(deliveries)

	if err := <-errs; !errors.Is(err, errDeliveriesClosed) {
		t.Errorf("collect = %v, want errDeliveriesClosed", err)
	}
}
