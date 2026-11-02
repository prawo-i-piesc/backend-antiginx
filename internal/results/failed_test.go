package results

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	amqp "github.com/rabbitmq/amqp091-go"
)

// deadTask buduje martwe zadanie tak, jak przekazuje je broker: treść to
// ScanTaskPayload opublikowany przez backend.
func deadTask(scanID uuid.UUID) []byte {
	return fmt.Appendf(nil, `{"Target":"huubi.dev","Parameters":[{"Name":"--tests","Arguments":["https"]}],"TaskId":%q}`, scanID)
}

func TestDecodeFailedTask(t *testing.T) {
	id := newID(t)
	if got, err := decodeFailedTask(deadTask(id)); err != nil || got != id {
		t.Errorf("decodeFailedTask = %s, %v; want %s", got, err, id)
	}
	for _, raw := range []string{`nie json`, `{}`, `{"TaskId":"nie-uuid"}`} {
		if _, err := decodeFailedTask([]byte(raw)); err == nil {
			t.Errorf("decodeFailedTask(%s) przyjął zadanie", raw)
		}
	}
}

func TestMarkFailedOnlyTouchesUnfinishedScans(t *testing.T) {
	db := testDB(t)
	store := NewStore(db)
	now := time.Now()

	pending, running, completed, premium := newID(t), newID(t), newID(t), newID(t)
	for _, scan := range []models.Scan{
		{ID: pending, TargetURL: "https://a.example", Status: models.ScanStatusPending, CreatedAt: now},
		{ID: running, TargetURL: "https://b.example", Status: models.ScanStatusRunning, CreatedAt: now},
		{ID: completed, TargetURL: "https://c.example", Status: models.ScanStatusCompleted, CreatedAt: now},
	} {
		if err := db.Create(&scan).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&models.PremiumScan{ID: premium, UserID: newID(t), TargetURL: "https://d.example", Status: models.ScanStatusPending, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	unknown := newID(t)
	if err := store.MarkFailed(context.Background(), []uuid.UUID{pending, running, completed, premium, unknown}); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	for id, want := range map[uuid.UUID]string{
		pending:   models.ScanStatusFailed,
		running:   models.ScanStatusFailed,
		completed: models.ScanStatusCompleted,
	} {
		var scan models.Scan
		if err := db.First(&scan, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if scan.Status != want {
			t.Errorf("skan %s: status %s, want %s", id, scan.Status, want)
		}
	}
	var p models.PremiumScan
	if err := db.First(&p, "id = ?", premium).Error; err != nil {
		t.Fatal(err)
	}
	if p.Status != models.ScanStatusFailed {
		t.Errorf("skan premium: status %s, want FAILED", p.Status)
	}
}

func TestFailedTasksAreMarkedAndAcked(t *testing.T) {
	db := testDB(t)
	store := NewStore(db)
	scanID := newID(t)
	if err := db.Create(&models.Scan{ID: scanID, TargetURL: "https://huubi.dev", Status: models.ScanStatusPending, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	acker := newFakeAcker()
	deliveries := make(chan amqp.Delivery, 2)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = collect(ctx, deliveries, failedBatchSize, decodeFailedTask, store.MarkFailed) }()

	deliveries <- delivery(acker, 1, []byte(`nie json`))
	deliveries <- delivery(acker, 2, deadTask(scanID))

	ops := acker.wait(t, 2)
	if ops[0] != "reject 1 requeue=false" || ops[1] != "ack 2 multiple=true" {
		t.Errorf("ops = %v, want odrzucenie śmiecia i ack zadania", ops)
	}
	var scan models.Scan
	if err := db.First(&scan, "id = ?", scanID).Error; err != nil {
		t.Fatal(err)
	}
	if scan.Status != models.ScanStatusFailed {
		t.Errorf("status = %s, want FAILED", scan.Status)
	}
}
