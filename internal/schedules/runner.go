package schedules

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/billing"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

// DefaultBatch to najwięcej harmonogramów brane w jednym obiegu; reszta
// czeka na następny.
const DefaultBatch = 50

// Launch zleca skan harmonogramu: zapisuje go i publikuje zadanie dla silnika.
// Zwraca ID skanu. ErrInvalidTarget mówi, że adresu nie wolno już skanować.
type Launch func(ctx context.Context, schedule models.Schedule) (uuid.UUID, error)

// ErrInvalidTarget zwraca Launch, gdy cel przestał przechodzić walidację,
// np. nazwa wskazuje teraz na adres prywatny.
var ErrInvalidTarget = errors.New("cel harmonogramu nie przechodzi walidacji")

// Runner co chwilę zleca skany harmonogramów, którym minął termin.
//
// Kilka replik backendu może działać naraz: termin przejmuje ta, która
// pierwsza przesunie next_run_at z wartości, którą odczytała. Pozostałe
// widzą zero zmienionych wierszy i ten harmonogram pomijają.
type Runner struct {
	db     *gorm.DB
	launch Launch
	batch  int
	now    func() time.Time
}

func NewRunner(db *gorm.DB, launch Launch) *Runner {
	return &Runner{db: db, launch: launch, batch: DefaultBatch, now: time.Now}
}

// Run sprawdza harmonogramy od razu, a potem co every, aż do końca ctx.
func (r *Runner) Run(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		r.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick obsługuje harmonogramy, którym minął termin, i zwraca, ile skanów
// zlecił.
func (r *Runner) Tick(ctx context.Context) int {
	now := r.now()
	var due []models.Schedule
	if err := r.db.WithContext(ctx).
		Where("enabled = ? AND blocked_at IS NULL AND next_run_at <= ?", true, now).
		Order("next_run_at").
		Limit(r.batch).
		Find(&due).Error; err != nil {
		log.Printf("Harmonogramy: nie udało się pobrać terminów: %v", err)
		return 0
	}

	launched := 0
	for i := range due {
		if ctx.Err() != nil {
			break
		}
		if r.run(ctx, &due[i], now) {
			launched++
		}
	}
	return launched
}

func (r *Runner) run(ctx context.Context, s *models.Schedule, now time.Time) bool {
	cadence, err := FromSchedule(s)
	if err != nil {
		// Zapis sprawdza definicję, więc to dane spoza API. Pauza, żeby
		// harmonogram nie wracał w każdym obiegu.
		log.Printf("Harmonogram %s: zła definicja, wstrzymuję: %v", s.ID, err)
		r.db.WithContext(ctx).Model(&models.Schedule{}).Where("id = ?", s.ID).Update("enabled", false)
		return false
	}
	// Terminy przegapione, gdy backend nie działał, przepadają: po przerwie
	// rusza jeden skan, a nie po jednym za każdy dzień.
	next, err := cadence.Next(now)
	if err != nil {
		log.Printf("Harmonogram %s: %v", s.ID, err)
		return false
	}

	claim := r.db.WithContext(ctx).Model(&models.Schedule{}).
		Where("id = ? AND next_run_at = ?", s.ID, s.NextRunAt).
		Updates(map[string]any{"next_run_at": next, "last_run_at": now})
	if claim.Error != nil {
		log.Printf("Harmonogram %s: nie udało się przejąć terminu: %v", s.ID, claim.Error)
		return false
	}
	if claim.RowsAffected != 1 {
		return false
	}

	plan, err := billing.UserPlan(r.db.WithContext(ctx), s.UserID)
	if err != nil {
		log.Printf("Harmonogram %s: nie udało się odczytać planu: %v", s.ID, err)
		r.fail(ctx, s.ID, models.ScheduleErrorProRequired)
		return false
	}
	if plan != models.PlanPro {
		r.fail(ctx, s.ID, models.ScheduleErrorProRequired)
		return false
	}

	scanID, err := r.launch(ctx, *s)
	if err != nil {
		reason := models.ScheduleErrorQueueUnavailable
		if errors.Is(err, ErrInvalidTarget) {
			reason = models.ScheduleErrorInvalidTarget
		}
		log.Printf("Harmonogram %s: skan nie ruszył: %v", s.ID, err)
		r.fail(ctx, s.ID, reason)
		return false
	}

	if err := r.db.WithContext(ctx).Model(&models.Schedule{}).Where("id = ?", s.ID).
		Updates(map[string]any{"last_scan_id": scanID, "last_error": ""}).Error; err != nil {
		log.Printf("Harmonogram %s: nie udało się zapisać skanu %s: %v", s.ID, scanID, err)
	}
	return true
}

func (r *Runner) fail(ctx context.Context, id uuid.UUID, reason string) {
	if err := r.db.WithContext(ctx).Model(&models.Schedule{}).Where("id = ?", id).
		Update("last_error", reason).Error; err != nil {
		log.Printf("Harmonogram %s: nie udało się zapisać powodu %s: %v", id, reason, err)
	}
}
