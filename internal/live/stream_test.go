package live

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
)

type fakeSource struct {
	mu          sync.Mutex
	state       State
	results     []models.ScanResult
	gone        bool
	failing     bool
	failResults bool
	reads       atomic.Int32
}

func (f *fakeSource) Scan(context.Context, uuid.UUID) (Scan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads.Add(1)
	if f.failing {
		return Scan{}, errors.New("baza leży")
	}
	if f.gone {
		return Scan{}, ErrScanGone
	}
	return Scan{State: f.state}, nil
}

func (f *fakeSource) Results(_ context.Context, _ uuid.UUID, except []uint) ([]models.ScanResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failResults {
		return nil, errors.New("baza leży")
	}
	out := []models.ScanResult{}
	for _, r := range f.results {
		if !slices.Contains(except, r.ID) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeSource) add(name string) {
	f.addWithID(uint(len(f.results)+1), name)
}

func (f *fakeSource) addWithID(id uint, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, models.ScanResult{ID: id, TestName: name})
}

func (f *fakeSource) setFailResults(failing bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failResults = failing
}

func (f *fakeSource) setStatus(status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Status = status
}

func (f *fakeSource) setGone() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gone = true
}

func (f *fakeSource) setFailing(failing bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing = failing
}

// fast skraca czasy na czas testu. Testy zmieniają zmienne pakietu, więc nie
// idą równolegle.
func fast(t *testing.T) {
	t.Helper()
	safety, heart, grace, life := safetyInterval, heartbeatInterval, finalGrace, maxLifetime
	safetyInterval, heartbeatInterval, finalGrace, maxLifetime = time.Hour, time.Hour, 10*time.Millisecond, time.Hour
	t.Cleanup(func() { safetyInterval, heartbeatInterval, finalGrace, maxLifetime = safety, heart, grace, life })
}

type event struct {
	Type    string              `json:"type"`
	Scan    json.RawMessage     `json:"scan"`
	Results []models.ScanResult `json:"results"`
	Status  string              `json:"status"`
}

type client struct {
	t    *testing.T
	conn *websocket.Conn
	hub  *Hub
	id   uuid.UUID
}

func connect(t *testing.T, source Source, expires time.Time) *client {
	t.Helper()
	hub := NewHub()
	id := uuid.New()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		Stream{Hub: hub, ScanID: id, Source: source, SessionExpires: expires}.Serve(r.Context(), conn)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return &client{t: t, conn: conn, hub: hub, id: id}
}

func (c *client) next() event {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		c.t.Fatalf("Read: %v", err)
	}
	var e event
	if err := json.Unmarshal(data, &e); err != nil {
		c.t.Fatalf("nie JSON (%s): %v", data, err)
	}
	return e
}

// closedWith czyta do zamknięcia i zwraca jego kod.
func (c *client) closedWith() websocket.StatusCode {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := c.conn.Read(ctx); err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

func running() *fakeSource {
	return &fakeSource{state: State{Status: models.ScanStatusRunning}}
}

func TestStreamSendsSnapshotThenChangesThenClosesWhenFinished(t *testing.T) {
	fast(t)
	source := running()
	source.add("https")

	c := connect(t, source, time.Time{})
	if e := c.next(); e.Type != "snapshot" || !strings.Contains(string(e.Scan), "https") {
		t.Fatalf("pierwsze zdarzenie = %+v, want snapshot z wynikiem", e)
	}

	source.add("hsts")
	c.hub.Notify(c.id)
	e := c.next()
	if e.Type != "results" || len(e.Results) != 1 || e.Results[0].TestName != "hsts" {
		t.Fatalf("zdarzenie = %+v, want tylko nowy wynik", e)
	}

	source.setStatus(models.ScanStatusCompleted)
	c.hub.Notify(c.id)
	if e := c.next(); e.Type != "status" || e.Status != models.ScanStatusCompleted {
		t.Fatalf("zdarzenie = %+v, want status COMPLETED", e)
	}
	if code := c.closedWith(); code != websocket.StatusNormalClosure {
		t.Errorf("kod zamknięcia = %d, want 1000", code)
	}
}

func TestStreamSendsStatusChangeOnlyOnce(t *testing.T) {
	fast(t)
	started := time.Now()
	source := &fakeSource{state: State{Status: models.ScanStatusPending}}

	c := connect(t, source, time.Time{})
	c.next() // snapshot

	source.mu.Lock()
	source.state = State{Status: models.ScanStatusRunning, StartedAt: &started}
	source.mu.Unlock()
	c.hub.Notify(c.id)
	if e := c.next(); e.Type != "status" || e.Status != models.ScanStatusRunning {
		t.Fatalf("zdarzenie = %+v, want status RUNNING", e)
	}

	// Ten sam stan z nowego odczytu, z innym wskaźnikiem na ten sam czas.
	again := started
	source.mu.Lock()
	source.state.StartedAt = &again
	source.mu.Unlock()
	source.add("https")
	c.hub.Notify(c.id)
	if e := c.next(); e.Type != "results" {
		t.Fatalf("zdarzenie = %+v, want results", e)
	}

	// Następne zdarzenie ma mówić o końcu; powtórzony stan RUNNING byłby przed nim.
	source.setStatus(models.ScanStatusCompleted)
	c.hub.Notify(c.id)
	if e := c.next(); e.Type != "status" || e.Status != models.ScanStatusCompleted {
		t.Fatalf("zdarzenie = %+v, niezmieniony stan nie powinien iść drugi raz", e)
	}
}

// Numer wiersza powstaje przy wstawieniu, a widać go po commicie: przy dwóch
// odbiorcach kolejki niższy numer może pojawić się po wyższym i nie wolno go zgubić.
func TestStreamDeliversResultCommittedOutOfOrder(t *testing.T) {
	fast(t)
	source := running()

	c := connect(t, source, time.Time{})
	c.next() // snapshot

	source.addWithID(2, "wyższy numer, wcześniejszy commit")
	c.hub.Notify(c.id)
	if e := c.next(); e.Type != "results" || e.Results[0].ID != 2 {
		t.Fatalf("zdarzenie = %+v, want wynik 2", e)
	}

	source.addWithID(1, "niższy numer, późniejszy commit")
	c.hub.Notify(c.id)
	if e := c.next(); e.Type != "results" || len(e.Results) != 1 || e.Results[0].ID != 1 {
		t.Fatalf("zdarzenie = %+v, wynik 1 zaginął albo poszedł z powtórką", e)
	}
}

// To samo dla wyników, które były już w pierwszym odczycie.
func TestStreamDoesNotRepeatResultsFromSnapshot(t *testing.T) {
	fast(t)
	source := running()
	source.addWithID(5, "z pierwszego odczytu")

	c := connect(t, source, time.Time{})
	c.next() // snapshot

	source.addWithID(3, "później")
	c.hub.Notify(c.id)
	if e := c.next(); e.Type != "results" || len(e.Results) != 1 || e.Results[0].ID != 3 {
		t.Fatalf("zdarzenie = %+v, want tylko wynik 3", e)
	}
}

// Kod 1000 obiecuje komplet, więc po nieudanym ostatnim odczycie klient ma wrócić.
func TestStreamFinishDoesNotPromiseCompletenessAfterFailedRead(t *testing.T) {
	fast(t)
	source := running()

	c := connect(t, source, time.Time{})
	c.next() // snapshot

	finalGrace = 60 * time.Millisecond
	source.setStatus(models.ScanStatusCompleted)
	c.hub.Notify(c.id)
	time.Sleep(20 * time.Millisecond)
	source.setFailResults(true)

	if code := c.closedWith(); code != websocket.StatusTryAgainLater {
		t.Errorf("kod zamknięcia = %d, want 1013", code)
	}
}

func TestStreamHeartbeatDoesNotReadTheDatabase(t *testing.T) {
	fast(t)
	heartbeatInterval = 5 * time.Millisecond
	source := running()

	c := connect(t, source, time.Time{})
	c.next() // snapshot
	reads := source.reads.Load()

	for range 3 {
		if e := c.next(); e.Type != "heartbeat" {
			t.Fatalf("zdarzenie = %+v, want heartbeat", e)
		}
	}
	if got := source.reads.Load(); got != reads {
		t.Errorf("odczytów podczas heartbeatów: %d", got-reads)
	}
}

// Koniec skanu może wejść do bazy chwilę przed ostatnimi wynikami innego odbiorcy.
func TestStreamWaitsForLateResultsBeforeFinalStatus(t *testing.T) {
	fast(t)
	finalGrace = 80 * time.Millisecond
	source := running()

	c := connect(t, source, time.Time{})
	c.next() // snapshot

	source.setStatus(models.ScanStatusCompleted)
	c.hub.Notify(c.id)
	time.Sleep(20 * time.Millisecond)
	source.add("spóźniony")

	e := c.next()
	if e.Type != "results" || len(e.Results) != 1 || e.Results[0].TestName != "spóźniony" {
		t.Fatalf("zdarzenie = %+v, want spóźniony wynik przed statusem", e)
	}
	if e := c.next(); e.Type != "status" || e.Status != models.ScanStatusCompleted {
		t.Fatalf("zdarzenie = %+v, want status COMPLETED na końcu", e)
	}
}

func TestStreamFindsChangesWithoutSignal(t *testing.T) {
	fast(t)
	safetyInterval = 10 * time.Millisecond
	source := running()

	c := connect(t, source, time.Time{})
	c.next() // snapshot

	source.add("https") // bez Notify: sygnał przepadł
	if e := c.next(); e.Type != "results" || len(e.Results) != 1 {
		t.Fatalf("zdarzenie = %+v, zapasowy odczyt powinien znaleźć wynik", e)
	}
}

func TestStreamSurvivesTemporaryDatabaseError(t *testing.T) {
	fast(t)
	safetyInterval = 10 * time.Millisecond
	source := running()

	c := connect(t, source, time.Time{})
	c.next() // snapshot

	source.setFailing(true)
	before := source.reads.Load()
	for source.reads.Load() < before+2 {
		time.Sleep(5 * time.Millisecond)
	}
	source.add("https")
	source.setFailing(false)

	if e := c.next(); e.Type != "results" {
		t.Fatalf("zdarzenie = %+v, połączenie powinno przetrwać błąd bazy", e)
	}
}

func TestStreamFinishedScanClosesAfterSnapshot(t *testing.T) {
	fast(t)
	source := &fakeSource{state: State{Status: models.ScanStatusFailed}}

	c := connect(t, source, time.Time{})
	if e := c.next(); e.Type != "snapshot" {
		t.Fatalf("zdarzenie = %+v, want snapshot", e)
	}

	// Stan końcowy był już w pierwszym odczycie: za nim nie ma być nic poza zamknięciem.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.conn.Read(ctx)
	if err == nil {
		t.Fatalf("po pierwszym odczycie przyszło jeszcze: %s", data)
	}
	if code := websocket.CloseStatus(err); code != websocket.StatusNormalClosure {
		t.Errorf("kod zamknięcia = %d, want 1000", code)
	}
}

func TestStreamClosesWhenScanDisappears(t *testing.T) {
	fast(t)
	source := running()

	c := connect(t, source, time.Time{})
	c.next() // snapshot

	source.setGone()
	c.hub.Notify(c.id)
	if code := c.closedWith(); code != CloseScanGone {
		t.Errorf("kod zamknięcia = %d, want %d", code, CloseScanGone)
	}
}

func TestStreamClosesWhenServerShutsDown(t *testing.T) {
	fast(t)
	c := connect(t, running(), time.Time{})
	c.next() // snapshot

	c.hub.Shutdown()
	if code := c.closedWith(); code != websocket.StatusGoingAway {
		t.Errorf("kod zamknięcia = %d, want 1001", code)
	}
}

func TestStreamClosesWhenSessionExpires(t *testing.T) {
	fast(t)
	c := connect(t, running(), time.Now().Add(30*time.Millisecond))
	c.next() // snapshot

	if code := c.closedWith(); code != CloseSessionExpired {
		t.Errorf("kod zamknięcia = %d, want %d", code, CloseSessionExpired)
	}
}

func TestStreamClosesAfterMaxLifetime(t *testing.T) {
	fast(t)
	maxLifetime = 30 * time.Millisecond
	c := connect(t, running(), time.Time{})
	c.next() // snapshot

	if code := c.closedWith(); code != CloseLifetimeExceeded {
		t.Errorf("kod zamknięcia = %d, want %d", code, CloseLifetimeExceeded)
	}
}

func TestStreamSendsHeartbeats(t *testing.T) {
	fast(t)
	heartbeatInterval = 10 * time.Millisecond
	c := connect(t, running(), time.Time{})
	c.next() // snapshot

	// Ramki ping wymagają czytającego klienta, więc czytamy dalej.
	if e := c.next(); e.Type != "heartbeat" {
		t.Fatalf("zdarzenie = %+v, want heartbeat", e)
	}
}

func TestStreamReleasesSubscriptionWhenClientLeaves(t *testing.T) {
	fast(t)
	c := connect(t, running(), time.Time{})
	c.next() // snapshot

	_ = c.conn.Close(websocket.StatusNormalClosure, "")

	deadline := time.Now().Add(2 * time.Second)
	for {
		c.hub.mu.Lock()
		left := len(c.hub.subscribers)
		c.hub.mu.Unlock()
		if left == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("po odejściu klienta zostało %d subskrypcji", left)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStateJSONIsFlatInStatusEvent(t *testing.T) {
	now := time.Now().UTC()
	data, err := json.Marshal(statusEvent{Type: "status", State: State{Status: "RUNNING", StartedAt: &now}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "RUNNING" || got["started_at"] == nil || got["completed_at"] != nil {
		t.Errorf("kształt zdarzenia: %s", data)
	}
}
