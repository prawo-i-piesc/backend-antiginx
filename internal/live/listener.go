package live

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"gorm.io/gorm"
)

// channel to kanał NOTIFY z ID skanu, który właśnie się zmienił. Wyniki zapisuje
// odbiorca na dowolnej replice, a klient może być podłączony do innej, więc
// sygnał musi przejść przez coś wspólnego dla nich wszystkich.
const channel = "scan_events"

const (
	minListenRetry = time.Second
	maxListenRetry = 30 * time.Second
)

// Publish zgłasza zmianę skanów w transakcji tx: Postgres doręczy sygnał dopiero
// po commicie, więc nikt nie przyjdzie po wyniki, których jeszcze nie widać.
func Publish(tx *gorm.DB, ids []uuid.UUID) error {
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if err := tx.Exec("SELECT pg_notify(?, ?)", channel, id.String()).Error; err != nil {
			return err
		}
	}
	return nil
}

// Listener przekazuje sygnały z Postgresa do huba na własnym połączeniu.
type Listener struct {
	dsn string
	hub *Hub
}

func NewListener(dsn string, hub *Hub) *Listener {
	return &Listener{dsn: dsn, hub: hub}
}

// Run blokuje do zakończenia ctx. Utrata połączenia nie zamyka procesu: bez
// sygnałów podgląd działa dalej, tylko wolniej, na zapasowych odczytach.
func (l *Listener) Run(ctx context.Context) {
	delay := minListenRetry
	for {
		err := l.listen(ctx, func() { delay = minListenRetry })
		if ctx.Err() != nil {
			return
		}
		log.Printf("live: nasłuch sygnałów przerwany, ponowię za %s: %v", delay, err)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
		delay = min(delay*2, maxListenRetry)
	}
}

func (l *Listener) listen(ctx context.Context, connected func()) error {
	conn, err := pgx.Connect(ctx, l.dsn)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()

	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		return err
	}
	connected()
	// Co minęło w czasie przerwy, nadrabiamy od razu.
	l.hub.NotifyAll()

	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		id, err := uuid.Parse(notification.Payload)
		if err != nil {
			log.Printf("live: pomijam sygnał z nieprawidłowym ID skanu: %v", err)
			continue
		}
		l.hub.Notify(id)
	}
}
