package schedules

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Subscription{}, &models.PlanGrant{}, &models.Schedule{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedOwner(t *testing.T, db *gorm.DB, pro bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := db.Create(&models.User{ID: id, Email: id.String() + "@example.com", Role: models.UserRoleUser, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if pro {
		if err := db.Create(&models.PlanGrant{UserID: id, GrantedBy: id, GrantedAt: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func seedSchedule(t *testing.T, db *gorm.DB, owner uuid.UUID, next time.Time, edit func(*models.Schedule)) models.Schedule {
	t.Helper()
	s := models.Schedule{
		ID: uuid.New(), UserID: owner, TargetURL: "https://example.com",
		Tests:     models.PlannedTests([]string{"https"}),
		Frequency: models.ScheduleDaily, Time: "08:00", Timezone: "UTC",
		Enabled: true, NextRunAt: &next,
	}
	if edit != nil {
		edit(&s)
	}
	if err := db.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

type launches struct {
	scheduleIDs []uuid.UUID
	err         error
}

func (l *launches) launch(_ context.Context, s models.Schedule) (uuid.UUID, error) {
	if l.err != nil {
		return uuid.Nil, l.err
	}
	l.scheduleIDs = append(l.scheduleIDs, s.ID)
	return uuid.New(), nil
}

func reload(t *testing.T, db *gorm.DB, id uuid.UUID) models.Schedule {
	t.Helper()
	var s models.Schedule
	if err := db.Take(&s, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

var now = time.Date(2026, 11, 16, 8, 0, 30, 0, time.UTC)

func newTestRunner(db *gorm.DB, l *launches) *Runner {
	r := NewRunner(db, l.launch)
	r.now = func() time.Time { return now }
	return r
}

func TestTickLaunchesDueSchedulesOnce(t *testing.T) {
	db := testDB(t)
	owner := seedOwner(t, db, true)
	due := seedSchedule(t, db, owner, now.Add(-30*time.Second), nil)
	later := seedSchedule(t, db, owner, now.Add(time.Hour), nil)

	l := &launches{}
	r := newTestRunner(db, l)
	if n := r.Tick(context.Background()); n != 1 {
		t.Fatalf("Tick launched %d, want 1", n)
	}
	if len(l.scheduleIDs) != 1 || l.scheduleIDs[0] != due.ID {
		t.Fatalf("launched %v, want only %s", l.scheduleIDs, due.ID)
	}

	got := reload(t, db, due.ID)
	if want := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC); got.NextRunAt == nil || !got.NextRunAt.Equal(want) {
		t.Errorf("next_run_at = %v, want %s", got.NextRunAt, want)
	}
	if got.LastScanID == nil || got.LastError != "" {
		t.Errorf("last_scan_id = %v, last_error = %q; want a scan and no error", got.LastScanID, got.LastError)
	}
	if untouched := reload(t, db, later.ID); untouched.LastRunAt != nil {
		t.Errorf("a schedule that is not due ran at %v", untouched.LastRunAt)
	}

	// Drugi obieg w tej samej chwili nie ma już czego uruchomić.
	if n := r.Tick(context.Background()); n != 0 {
		t.Errorf("second Tick launched %d, want 0", n)
	}
}

// Druga replika odczytała ten sam termin, ale pierwsza już go przesunęła.
func TestRunSkipsATermAnotherReplicaTook(t *testing.T) {
	db := testDB(t)
	owner := seedOwner(t, db, true)
	stale := seedSchedule(t, db, owner, now.Add(-time.Minute), nil)

	first, second := &launches{}, &launches{}
	newTestRunner(db, first).Tick(context.Background())
	if newTestRunner(db, second).run(context.Background(), &stale, now) {
		t.Fatal("the second replica launched a term the first one had taken")
	}
	if len(first.scheduleIDs) != 1 || len(second.scheduleIDs) != 0 {
		t.Errorf("launches %d and %d, want 1 and 0", len(first.scheduleIDs), len(second.scheduleIDs))
	}
}

func TestTickSkipsPausedAndBlocked(t *testing.T) {
	db := testDB(t)
	owner := seedOwner(t, db, true)
	blockedAt := now.Add(-time.Hour)
	seedSchedule(t, db, owner, now.Add(-time.Minute), func(s *models.Schedule) { s.Enabled = false })
	seedSchedule(t, db, owner, now.Add(-time.Minute), func(s *models.Schedule) { s.BlockedAt = &blockedAt })

	l := &launches{}
	if n := newTestRunner(db, l).Tick(context.Background()); n != 0 {
		t.Errorf("Tick launched %d paused or blocked schedules", n)
	}
}

// Bez planu Pro termin mija bez skanu, z powodem dla użytkownika, i nie
// wraca w każdym obiegu.
func TestTickWithoutProRecordsTheReason(t *testing.T) {
	db := testDB(t)
	owner := seedOwner(t, db, false)
	s := seedSchedule(t, db, owner, now.Add(-time.Minute), nil)

	l := &launches{}
	newTestRunner(db, l).Tick(context.Background())
	got := reload(t, db, s.ID)
	if len(l.scheduleIDs) != 0 || got.LastError != models.ScheduleErrorProRequired {
		t.Errorf("launched %d, last_error %q; want none and %s", len(l.scheduleIDs), got.LastError, models.ScheduleErrorProRequired)
	}
	if got.NextRunAt == nil || !got.NextRunAt.After(now) {
		t.Errorf("next_run_at = %v, want a later term", got.NextRunAt)
	}
}

func TestTickRecordsLaunchFailures(t *testing.T) {
	db := testDB(t)
	owner := seedOwner(t, db, true)
	queue := seedSchedule(t, db, owner, now.Add(-time.Minute), nil)

	newTestRunner(db, &launches{err: errors.New("broker down")}).Tick(context.Background())
	if got := reload(t, db, queue.ID).LastError; got != models.ScheduleErrorQueueUnavailable {
		t.Errorf("last_error = %q, want %s", got, models.ScheduleErrorQueueUnavailable)
	}

	target := seedSchedule(t, db, owner, now.Add(-time.Minute), nil)
	newTestRunner(db, &launches{err: ErrInvalidTarget}).Tick(context.Background())
	if got := reload(t, db, target.ID).LastError; got != models.ScheduleErrorInvalidTarget {
		t.Errorf("last_error = %q, want %s", got, models.ScheduleErrorInvalidTarget)
	}
}
