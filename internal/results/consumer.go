package results

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	flushInterval        = 50 * time.Millisecond
	shutdownFlushTimeout = 5 * time.Second
	maxRetryDelay        = 30 * time.Second
)

// minRetryDelay jest zmienną tylko po to, żeby testy nie czekały sekundy.
var minRetryDelay = time.Second

var errDeliveriesClosed = errors.New("RabbitMQ zamknął strumień wyników")

type saveFunc func(context.Context, []message) error

// Consumer uruchamia kilka niezależnych odbiorców kolejki wynikowej, każdego
// na własnym kanale, żeby potwierdzenia jednej paczki nie mieszały się z
// paczkami innych.
type Consumer struct {
	conn      *amqp.Connection
	store     *Store
	queue     string
	workers   int
	batchSize int
}

func NewConsumer(conn *amqp.Connection, store *Store, queue string, workers, batchSize int) *Consumer {
	return &Consumer{conn: conn, store: store, queue: queue, workers: workers, batchSize: batchSize}
}

// Run blokuje do zakończenia ctx. Gdy któryś odbiorca padnie, zatrzymuje
// pozostałych i zwraca jego błąd, żeby proces mógł się zamknąć i wstać od
// nowa zamiast po cichu przestać odbierać wyniki.
func (c *Consumer) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, c.workers)
	var wg sync.WaitGroup
	for i := range c.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- c.consume(ctx, i)
			cancel()
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Consumer) consume(ctx context.Context, id int) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	// Prefetch większy od paczki, żeby kolejna zbierała się, zanim poprzednia
	// zostanie potwierdzona.
	if err := ch.Qos(c.batchSize*2, 0, false); err != nil {
		return err
	}
	deliveries, err := ch.Consume(c.queue, fmt.Sprintf("results-%d", id), false, false, false, false, nil)
	if err != nil {
		return err
	}
	return collect(ctx, deliveries, c.batchSize, c.store.SaveBatch)
}

// collect zbiera wiadomości w paczki i potwierdza każdą jednym Ack z
// multiple=true na ostatniej dostawie. Nieudany zapis wraca do kolejki w
// całości, ale dopiero po odczekaniu, żeby leżąca baza nie kręciła wiadomości
// w kółko.
func collect(ctx context.Context, deliveries <-chan amqp.Delivery, size int, save saveFunc) error {
	batch := make([]message, 0, size)
	var last amqp.Delivery
	retryDelay := minRetryDelay

	tick := time.NewTicker(flushInterval)
	defer tick.Stop()

	flush := func(ctx context.Context) {
		if len(batch) == 0 {
			return
		}
		defer func() { batch = batch[:0] }()

		if err := save(ctx, batch); err != nil {
			log.Printf("results: zapis paczki %d wyników nie powiódł się, ponowię za %s: %v", len(batch), retryDelay, err)
			select {
			case <-time.After(retryDelay):
			case <-ctx.Done():
			}
			retryDelay = min(retryDelay*2, maxRetryDelay)
			_ = last.Nack(true, true)
			return
		}
		retryDelay = minRetryDelay
		_ = last.Ack(true)
	}

	for {
		select {
		case d, ok := <-deliveries:
			if !ok {
				// Kanał już nie potwierdzi paczki, a niepotwierdzone wiadomości
				// broker i tak odda ponownie, więc nie zapisujemy jej na ślepo.
				return errDeliveriesClosed
			}
			msg, err := decode(d.Body)
			if err != nil {
				log.Printf("results: odrzucam wiadomość, której nie da się odczytać: %v", err)
				_ = d.Reject(false)
				continue
			}
			batch = append(batch, msg)
			last = d
			if len(batch) == size {
				flush(ctx)
			}
		case <-tick.C:
			flush(ctx)
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownFlushTimeout)
			flush(flushCtx)
			cancel()
			return nil
		}
	}
}
