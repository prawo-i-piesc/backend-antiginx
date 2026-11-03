package live

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/datatypes"
)

// Kody zamknięcia połączenia poza standardowymi: mówią klientowi, co dalej.
const (
	// CloseSessionExpired: token wygasł; odśwież sesję i połącz się ponownie.
	CloseSessionExpired websocket.StatusCode = 4401
	// CloseScanGone: skanu już nie ma; nie ma po co wracać.
	CloseScanGone websocket.StatusCode = 4404
	// CloseLifetimeExceeded: minął limit czasu połączenia; połącz się ponownie.
	CloseLifetimeExceeded websocket.StatusCode = 4408
)

// Zmienne tylko po to, żeby testy nie czekały sekundy.
var (
	// safetyInterval to zapasowy odczyt bazy na wypadek zgubionego sygnału.
	safetyInterval = 5 * time.Second
	// heartbeatInterval jest krótszy od limitów bezczynności proxy po drodze.
	heartbeatInterval = 20 * time.Second
	// finalGrace: wiadomości rozchodzą się na kilku odbiorców, więc koniec skanu
	// może wejść do bazy chwilę przed ostatnimi wynikami. Czekamy na nie, zanim
	// powiemy klientowi, że to już wszystko.
	finalGrace = time.Second
	// maxLifetime domyka połączenia do skanów, które nigdy się nie skończą.
	maxLifetime = 15 * time.Minute
)

const writeTimeout = 10 * time.Second

// ErrScanGone: skanu nie ma albo nie należy do pytającego.
var ErrScanGone = errors.New("skan nie istnieje")

// State to ta część skanu, która zmienia się w trakcie.
type State struct {
	Status      string     `json:"status"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

func (s State) finished() bool {
	return s.Status == models.ScanStatusCompleted || s.Status == models.ScanStatusFailed
}

// same porównuje wartości: wskaźniki z dwóch odczytów nigdy nie są równe.
func (s State) same(other State) bool {
	return s.Status == other.Status && sameTime(s.StartedAt, other.StartedAt) && sameTime(s.CompletedAt, other.CompletedAt)
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// Scan to skan darmowy albo z konta: pola wspólne odpowiedzi REST, bez właściciela.
type Scan struct {
	ID        uuid.UUID      `json:"id"`
	TargetURL string         `json:"target_url"`
	CreatedAt time.Time      `json:"created_at"`
	Tests     datatypes.JSON `json:"tests"`
	State
	Results []models.ScanResult `gorm:"-" json:"results"`
}

// Source czyta skany z bazy, w granicach tego, co klient może zobaczyć.
type Source interface {
	// Scan zwraca skan bez wyników albo ErrScanGone.
	Scan(ctx context.Context, id uuid.UUID) (Scan, error)
	// Results zwraca wyniki skanu oprócz już wysłanych, najstarsze pierwsze.
	Results(ctx context.Context, id uuid.UUID, except []uint) ([]models.ScanResult, error)
}

type snapshotEvent struct {
	Type string `json:"type"`
	Scan Scan   `json:"scan"`
}

type resultsEvent struct {
	Type    string              `json:"type"`
	Results []models.ScanResult `json:"results"`
}

type statusEvent struct {
	Type string `json:"type"`
	State
}

type heartbeatEvent struct {
	Type string `json:"type"`
}

// Stream to jedno połączenie klienta z podglądem jednego skanu.
type Stream struct {
	Hub    *Hub
	ScanID uuid.UUID
	Source Source
	// SessionExpires to koniec ważności tokenu; zero, gdy skan nie wymaga konta.
	SessionExpires time.Time
}

// Serve wysyła stan skanu i jego zmiany, aż skan się skończy, klient odejdzie
// albo serwer się zamknie, po czym zamyka połączenie.
func (s Stream) Serve(ctx context.Context, conn *websocket.Conn) {
	// Klient nic nie wysyła: CloseRead obsługuje ramki kontrolne, potrzebne do
	// Ping, i kończy ctx, gdy połączenie się zerwie.
	ctx = conn.CloseRead(ctx)

	code, reason := s.run(ctx, conn)
	if ctx.Err() != nil {
		_ = conn.CloseNow()
		return
	}
	_ = conn.Close(code, reason)
}

func (s Stream) run(ctx context.Context, conn *websocket.Conn) (websocket.StatusCode, string) {
	// Przed odczytem stanu: zmiana w trakcie odczytu obudzi nas od razu.
	wake, unsubscribe := s.Hub.Subscribe(s.ScanID)
	defer unsubscribe()

	scan, err := s.Source.Scan(ctx, s.ScanID)
	if err != nil {
		return closeForReadError(s.ScanID, err)
	}
	scan.Results, err = s.Source.Results(ctx, s.ScanID, nil)
	if err != nil {
		return closeForReadError(s.ScanID, err)
	}
	if err := write(ctx, conn, snapshotEvent{Type: "snapshot", Scan: scan}); err != nil {
		return websocket.StatusInternalError, "write failed"
	}

	// Co wysłane, pamiętamy po ID, a nie „wszystko do ostatniego”. Numer wiersza
	// powstaje przy wstawieniu, a widać go dopiero po commicie, więc przy kilku
	// odbiorcach kolejki niższy numer może pojawić się po wyższym.
	sent := make([]uint, 0, len(scan.Results))
	for _, result := range scan.Results {
		sent = append(sent, result.ID)
	}

	state := scan.State
	if state.finished() {
		return s.finish(ctx, conn, state, sent, false)
	}

	code, reason := CloseLifetimeExceeded, "lifetime exceeded"
	lifetime := maxLifetime
	if !s.SessionExpires.IsZero() && time.Until(s.SessionExpires) < lifetime {
		code, reason = CloseSessionExpired, "session expired"
		lifetime = max(time.Until(s.SessionExpires), 0)
	}
	expired := time.NewTimer(lifetime)
	defer expired.Stop()

	safety := time.NewTicker(safetyInterval)
	defer safety.Stop()
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return websocket.StatusNormalClosure, ""
		case <-s.Hub.Done():
			return websocket.StatusGoingAway, "server restarting"
		case <-expired.C:
			return code, reason
		case <-heartbeat.C:
			if err := beat(ctx, conn); err != nil {
				return websocket.StatusInternalError, "heartbeat failed"
			}
			continue
		case <-wake:
		case <-safety.C:
		}

		// Stan przed wynikami: skan widziany jako zakończony ma już w bazie wszystko,
		// co wyślemy poniżej, poza spóźnialskimi, na które czeka finish.
		next, err := s.Source.Scan(ctx, s.ScanID)
		if errors.Is(err, ErrScanGone) {
			return CloseScanGone, "scan not found"
		}
		if err != nil {
			// Chwilowy błąd bazy: następny odczyt spróbuje jeszcze raz.
			log.Printf("live: odczyt stanu skanu %s nie powiódł się: %v", s.ScanID, err)
			continue
		}

		results, err := s.Source.Results(ctx, s.ScanID, sent)
		if err != nil {
			log.Printf("live: odczyt wyników skanu %s nie powiódł się: %v", s.ScanID, err)
			continue
		}
		if sent, err = send(ctx, conn, results, sent); err != nil {
			return websocket.StatusInternalError, "write failed"
		}

		if next.finished() {
			return s.finish(ctx, conn, next.State, sent, true)
		}
		if !next.same(state) {
			state = next.State
			if err := write(ctx, conn, statusEvent{Type: "status", State: state}); err != nil {
				return websocket.StatusInternalError, "write failed"
			}
		}
	}
}

// finish domyka połączenie po końcu skanu: czeka na spóźnione wyniki i dopiero
// wtedy podaje stan końcowy, chyba że klient już go ma z pierwszego odczytu.
// Kod 1000 obiecuje, że klient ma wszystko, więc nieudany ostatni odczyt kończy
// się kodem, po którym klient wraca po pełny stan.
func (s Stream) finish(ctx context.Context, conn *websocket.Conn, state State, sent []uint, announce bool) (websocket.StatusCode, string) {
	select {
	case <-time.After(finalGrace):
	case <-ctx.Done():
		return websocket.StatusNormalClosure, ""
	case <-s.Hub.Done():
		return websocket.StatusGoingAway, "server restarting"
	}

	results, err := s.Source.Results(ctx, s.ScanID, sent)
	if err != nil {
		return closeForReadError(s.ScanID, err)
	}
	if _, err := send(ctx, conn, results, sent); err != nil {
		return websocket.StatusInternalError, "write failed"
	}
	if announce {
		if err := write(ctx, conn, statusEvent{Type: "status", State: state}); err != nil {
			return websocket.StatusInternalError, "write failed"
		}
	}
	return websocket.StatusNormalClosure, "scan finished"
}

// send wysyła nowe wyniki i dopisuje je do wysłanych.
func send(ctx context.Context, conn *websocket.Conn, results []models.ScanResult, sent []uint) ([]uint, error) {
	if len(results) == 0 {
		return sent, nil
	}
	if err := write(ctx, conn, resultsEvent{Type: "results", Results: results}); err != nil {
		return sent, err
	}
	for _, result := range results {
		sent = append(sent, result.ID)
	}
	return sent, nil
}

// beat sprawdza, czy klient żyje, i daje mu znać, że serwer też: JavaScript nie
// widzi ramek ping, więc bez zdarzenia nie odróżniłby ciszy od zerwanego łącza.
func beat(ctx context.Context, conn *websocket.Conn) error {
	pingCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := conn.Ping(pingCtx); err != nil {
		return err
	}
	return write(ctx, conn, heartbeatEvent{Type: "heartbeat"})
}

func write(ctx context.Context, conn *websocket.Conn, event any) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(ctx, conn, event)
}

func closeForReadError(scanID uuid.UUID, err error) (websocket.StatusCode, string) {
	if errors.Is(err, ErrScanGone) {
		return CloseScanGone, "scan not found"
	}
	log.Printf("live: odczyt skanu %s nie powiódł się: %v", scanID, err)
	return websocket.StatusTryAgainLater, "try again"
}
