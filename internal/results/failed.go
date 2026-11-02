package results

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

// FailedQueue to kolejka backendu podpięta do scan_dlx obok scan_dlq. scan_dlx
// jest fanoutem, więc każde martwe zadanie trafia do obu: scan_dlq zostaje
// nietknięta do podglądu, a stąd backend dowiaduje się, że skan padł.
const FailedQueue = "scan_failed"

// failedBatchSize jest mały, bo martwych zadań jest mało, a każde powinno
// zmienić status skanu od razu.
const failedBatchSize = 20

var errMissingTaskID = errors.New("zadanie bez TaskId będącego UUID")

// FailedTasks oznacza jako FAILED skany, których zadania daemon nie zdołał
// wykonać w limicie dostarczeń scan_queue. Bez tego skan, na którym silnik się
// wywraca, zostaje w PENDING na zawsze, bo daemon nie zgłasza porażki.
type FailedTasks struct {
	conn  *amqp.Connection
	store *Store
	queue string
}

func NewFailedTasks(conn *amqp.Connection, store *Store, queue string) *FailedTasks {
	return &FailedTasks{conn: conn, store: store, queue: queue}
}

// Run blokuje do zakończenia ctx i zwraca błąd, gdy odbiorca padnie, tak jak
// Consumer.Run.
func (f *FailedTasks) Run(ctx context.Context) error {
	ch, err := f.conn.Channel()
	if err != nil {
		return err
	}
	defer func() {
		_ = ch.Close()
	}()

	if err := ch.Qos(failedBatchSize*2, 0, false); err != nil {
		return err
	}
	deliveries, err := ch.Consume(f.queue, "failed-tasks", false, false, false, false, nil)
	if err != nil {
		return err
	}
	return collect(ctx, deliveries, failedBatchSize, decodeFailedTask, f.store.MarkFailed)
}

// decodeFailedTask czyta ID skanu z martwego zadania. Broker przekazuje treść
// bez zmian, więc to ScanTaskPayload, który backend sam opublikował.
func decodeFailedTask(body []byte) (uuid.UUID, error) {
	var task struct {
		TaskId string `json:"TaskId"`
	}
	if err := json.Unmarshal(body, &task); err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.Parse(task.TaskId)
	if err != nil {
		return uuid.Nil, errMissingTaskID
	}
	return id, nil
}

// MarkFailed ustawia FAILED skanom z paczki, które jeszcze czekają albo trwają.
// Skan już zakończony zostaje, jak był: martwe zadanie mogło być powtórzeniem
// czegoś, co wcześniej doszło do końca.
func (s *Store) MarkFailed(ctx context.Context, ids []uuid.UUID) error {
	var marked int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		marked = 0
		for _, model := range []any{&models.Scan{}, &models.PremiumScan{}} {
			res := tx.Model(model).
				Where("id IN ? AND status IN ?", ids, []string{models.ScanStatusPending, models.ScanStatusRunning}).
				Update("status", models.ScanStatusFailed)
			if res.Error != nil {
				return res.Error
			}
			marked += res.RowsAffected
		}
		return nil
	})
	if err != nil {
		return err
	}
	log.Printf("results: daemon nie wykonał %d zadań, %d skanów oznaczono jako FAILED", len(ids), marked)
	return nil
}
