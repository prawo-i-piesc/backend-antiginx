package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/billing"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"github.com/prawo-i-piesc/backend/internal/schedules"
	"github.com/prawo-i-piesc/backend/internal/stats"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// maxCalendarSpan to najdłuższy przedział kalendarza: sześć tygodni siatki
// miesiąca z zapasem na strefy czasowe.
const maxCalendarSpan = 45 * 24 * time.Hour

// ScheduleHandler obsługuje harmonogramy konta i ich widok w panelu admina.
// Harmonogramy są tylko w planie Pro; kto plan stracił, może je wstrzymać
// i usunąć, ale nie dodać, zmienić ani wznowić.
type ScheduleHandler struct {
	db *gorm.DB
	// scans waliduje cel tak samo jak przy skanie z ręki i daje ikony witryn.
	scans *ScanHandler
	// limit to najwięcej harmonogramów na konto.
	limit int
	now   func() time.Time
}

func NewScheduleHandler(db *gorm.DB, scans *ScanHandler, limit int) *ScheduleHandler {
	return &ScheduleHandler{db: db, scans: scans, limit: limit, now: time.Now}
}

type scheduleRequest struct {
	TargetURL string   `json:"target_url" binding:"required,max=2048"`
	Tests     []string `json:"tests" binding:"required,min=1"`
	Frequency string   `json:"frequency" binding:"required"`
	Time      string   `json:"time" binding:"required"`
	Weekday   *int     `json:"weekday"`
	MonthDay  *int     `json:"month_day"`
	Timezone  string   `json:"timezone" binding:"required,max=64"`
}

type scheduleStateRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

// scheduleScan to skan z harmonogramu w skrócie: ostatni na liście
// i uruchomienia w kalendarzu.
type scheduleScan struct {
	ID        uuid.UUID `json:"id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	Risk      *int      `json:"risk"`
}

// scheduleRow to harmonogram na liście konta i w panelu admina.
type scheduleRow struct {
	ID        uuid.UUID      `json:"id"`
	TargetURL string         `json:"target_url"`
	Tests     datatypes.JSON `json:"tests"`
	Frequency string         `json:"frequency"`
	Time      string         `json:"time"`
	Weekday   *int           `json:"weekday"`
	MonthDay  *int           `json:"month_day"`
	Timezone  string         `json:"timezone"`
	Enabled   bool           `json:"enabled"`
	// Blocked to blokada administratora, z powodem dla użytkownika.
	Blocked     bool       `json:"blocked"`
	BlockedAt   *time.Time `json:"blocked_at"`
	BlockReason string     `json:"block_reason"`
	// NextRunAt jest pusty, dopóki harmonogram jest wstrzymany albo zablokowany.
	NextRunAt *time.Time `json:"next_run_at"`
	LastRunAt *time.Time `json:"last_run_at"`
	LastError string     `json:"last_error"`
	// LastScan to najnowszy skan z tego harmonogramu.
	LastScan *scheduleScan `json:"last_scan"`
	// Runs to liczba skanów z tego harmonogramu.
	Runs      int64     `json:"runs"`
	CreatedAt time.Time `json:"created_at"`
	Favicon   *string   `json:"favicon"`
	// Właściciel, tylko w panelu admina.
	UserID    *uuid.UUID `json:"user_id,omitempty"`
	UserEmail *string    `json:"user_email,omitempty"`
}

func newScheduleRow(s *models.Schedule) scheduleRow {
	row := scheduleRow{
		ID:          s.ID,
		TargetURL:   s.TargetURL,
		Tests:       s.Tests,
		Frequency:   s.Frequency,
		Time:        s.Time,
		Weekday:     s.Weekday,
		MonthDay:    s.MonthDay,
		Timezone:    s.Timezone,
		Enabled:     s.Enabled,
		Blocked:     s.BlockedAt != nil,
		BlockedAt:   s.BlockedAt,
		BlockReason: s.BlockReason,
		LastRunAt:   s.LastRunAt,
		LastError:   s.LastError,
		CreatedAt:   s.CreatedAt,
	}
	if s.Active() {
		row.NextRunAt = s.NextRunAt
	}
	return row
}

// describeSchedules dopisuje do wierszy ostatni skan, liczbę uruchomień
// i ikonę witryny, jednym zapytaniem na każde z nich.
func (h *ScheduleHandler) describeSchedules(ctx context.Context, rows []scheduleRow) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(rows))
	targets := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
		targets[i] = rows[i].TargetURL
	}

	db := h.db.WithContext(ctx)
	var counts []struct {
		ScheduleID uuid.UUID
		Count      int64
	}
	if err := db.Model(&models.PremiumScan{}).
		Select("schedule_id, COUNT(*) AS count").
		Where("schedule_id IN ?", ids).
		Group("schedule_id").
		Scan(&counts).Error; err != nil {
		return err
	}
	runs := make(map[uuid.UUID]int64, len(counts))
	for _, row := range counts {
		runs[row.ScheduleID] = row.Count
	}

	// Najnowszy skan każdego harmonogramu, także taki, który nie ruszył
	// (FAILED po odrzuceniu przez kolejkę).
	var latest []struct {
		ID         uuid.UUID
		ScheduleID uuid.UUID
		Status     string
		CreatedAt  time.Time
	}
	if err := db.Model(&models.PremiumScan{}).
		Select("id, schedule_id, status, created_at").
		Where("schedule_id IN ?", ids).
		Where("created_at = (SELECT MAX(p.created_at) FROM premium_scans p WHERE p.schedule_id = premium_scans.schedule_id)").
		Scan(&latest).Error; err != nil {
		return err
	}
	lastScans := make(map[uuid.UUID]*scheduleScan, len(latest))
	scanIDs := make([]uuid.UUID, 0, len(latest))
	for _, scan := range latest {
		lastScans[scan.ScheduleID] = &scheduleScan{ID: scan.ID, Status: scan.Status, CreatedAt: scan.CreatedAt}
		scanIDs = append(scanIDs, scan.ID)
	}
	risks, err := stats.ScanRisks(db, scanIDs)
	if err != nil {
		return err
	}

	icons := h.scans.icons.Lookup(ctx, targets...)
	for i := range rows {
		rows[i].Runs = runs[rows[i].ID]
		if last := lastScans[rows[i].ID]; last != nil {
			if risk, ok := risks[last.ID]; ok {
				last.Risk = &risk
			}
			rows[i].LastScan = last
		}
		rows[i].Favicon = icons.For(rows[i].TargetURL)
	}
	return nil
}

// HandleList zwraca harmonogramy konta, od najnowszych, razem z limitem.
func (h *ScheduleHandler) HandleList(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	var schedules []models.Schedule
	if err := h.db.WithContext(c.Request.Context()).
		Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").
		Find(&schedules).Error; err != nil {
		log.Printf("Schedules: nie udało się pobrać harmonogramów: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	rows := make([]scheduleRow, len(schedules))
	for i := range schedules {
		rows[i] = newScheduleRow(&schedules[i])
	}
	if err := h.describeSchedules(c.Request.Context(), rows); err != nil {
		log.Printf("Schedules: nie udało się opisać harmonogramów: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.JSON(http.StatusOK, gin.H{"schedules": rows, "limit": h.limit})
}

// requirePro przepuszcza tylko konta w planie Pro.
func (h *ScheduleHandler) requirePro(c *gin.Context, userID uuid.UUID) bool {
	plan, err := billing.UserPlan(h.db.WithContext(c.Request.Context()), userID)
	if err != nil {
		log.Printf("Schedules: nie udało się odczytać planu %s: %v", userID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return false
	}
	if plan != models.PlanPro {
		httpx.Fail(c, httpx.CodeProRequired)
		return false
	}
	return true
}

// definition sprawdza żądanie i zwraca harmonogram z polami definicji
// (cel, testy, cykl) oraz najbliższym terminem.
func (h *ScheduleHandler) definition(c *gin.Context) (models.Schedule, bool) {
	var req scheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return models.Schedule{}, false
	}

	target, ok := h.scans.validTarget(c, req.TargetURL)
	if !ok {
		return models.Schedule{}, false
	}

	var tests []string
	seen := make(map[string]bool, len(req.Tests))
	for _, test := range req.Tests {
		if AllowedPremiumTests[test] && !seen[test] {
			seen[test] = true
			tests = append(tests, test)
		}
	}
	if len(tests) == 0 {
		httpx.Fail(c, httpx.CodeNoValidTests)
		return models.Schedule{}, false
	}

	cadence, err := schedules.Parse(schedules.Definition{
		Frequency: req.Frequency,
		Time:      req.Time,
		Weekday:   req.Weekday,
		MonthDay:  req.MonthDay,
		Timezone:  req.Timezone,
	})
	if err != nil {
		var field *schedules.FieldError
		if errors.As(err, &field) {
			httpx.FailWithFields(c, httpx.CodeValidationFailed, map[string]string{field.Field: field.Reason})
		} else {
			httpx.Fail(c, httpx.CodeValidationFailed)
		}
		return models.Schedule{}, false
	}
	next, err := cadence.Next(h.now())
	if err != nil {
		log.Printf("Schedules: brak terminu dla poprawnej definicji: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return models.Schedule{}, false
	}

	schedule := models.Schedule{
		TargetURL: target.URL,
		Tests:     models.PlannedTests(tests),
		Frequency: req.Frequency,
		Time:      req.Time,
		Timezone:  req.Timezone,
		NextRunAt: &next,
	}
	// Dzień tygodnia i miesiąca zostają tylko przy cyklu, który ich używa.
	switch req.Frequency {
	case models.ScheduleWeekly:
		schedule.Weekday = req.Weekday
	case models.ScheduleMonthly:
		schedule.MonthDay = req.MonthDay
	}
	return schedule, true
}

// HandleCreate dodaje harmonogram. Nowy od razu działa.
func (h *ScheduleHandler) HandleCreate(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok || !h.requirePro(c, userID) {
		return
	}
	schedule, ok := h.definition(c)
	if !ok {
		return
	}

	id, err := uuid.NewV7()
	if err != nil {
		log.Printf("Schedules: nie udało się wygenerować UUIDv7: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	schedule.ID = id
	schedule.UserID = userID
	schedule.Enabled = true

	err = h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&models.Schedule{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(h.limit) {
			return errScheduleLimit
		}
		return tx.Create(&schedule).Error
	})
	if errors.Is(err, errScheduleLimit) {
		httpx.Fail(c, httpx.CodeScheduleLimit)
		return
	}
	if err != nil {
		log.Printf("Schedules: nie udało się zapisać harmonogramu: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	h.scans.icons.Want(schedule.TargetURL)

	h.respond(c, http.StatusCreated, &schedule)
}

var errScheduleLimit = errors.New("limit harmonogramów")

// HandleUpdate zmienia cel, testy albo cykl. Pauza i blokada zostają.
func (h *ScheduleHandler) HandleUpdate(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	existing, ok := h.find(c, userID)
	if !ok || !h.requirePro(c, userID) {
		return
	}
	schedule, ok := h.definition(c)
	if !ok {
		return
	}

	if err := h.db.WithContext(c.Request.Context()).Model(existing).
		Select("target_url", "tests", "frequency", "time", "weekday", "month_day", "timezone", "next_run_at").
		Updates(&schedule).Error; err != nil {
		log.Printf("Schedules: nie udało się zmienić harmonogramu %s: %v", existing.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	h.scans.icons.Want(existing.TargetURL)

	h.respond(c, http.StatusOK, existing)
}

// HandleSetState wstrzymuje albo wznawia harmonogram. Wznowienie liczy
// termin od teraz, więc nie odrabia tych, które minęły w czasie pauzy.
func (h *ScheduleHandler) HandleSetState(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	var req scheduleStateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}
	schedule, ok := h.find(c, userID)
	if !ok {
		return
	}

	updates := map[string]any{"enabled": *req.Enabled}
	if *req.Enabled {
		if schedule.BlockedAt != nil {
			httpx.Fail(c, httpx.CodeScheduleBlocked)
			return
		}
		if !h.requirePro(c, userID) {
			return
		}
		next, ok := h.nextRun(c, schedule)
		if !ok {
			return
		}
		updates["next_run_at"] = next
	}

	if err := h.db.WithContext(c.Request.Context()).Model(schedule).Updates(updates).Error; err != nil {
		log.Printf("Schedules: nie udało się zmienić stanu harmonogramu %s: %v", schedule.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	h.respond(c, http.StatusOK, schedule)
}

// HandleDelete usuwa harmonogram. Skany, które zlecił, zostają w historii.
func (h *ScheduleHandler) HandleDelete(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	schedule, ok := h.find(c, userID)
	if !ok {
		return
	}
	if err := h.db.WithContext(c.Request.Context()).Delete(schedule).Error; err != nil {
		log.Printf("Schedules: nie udało się usunąć harmonogramu %s: %v", schedule.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	c.Status(http.StatusNoContent)
}

type calendarQuery struct {
	From time.Time `form:"from" binding:"required" time_format:"2006-01-02T15:04:05Z07:00"`
	To   time.Time `form:"to" binding:"required" time_format:"2006-01-02T15:04:05Z07:00"`
}

type calendarTerm struct {
	ScheduleID uuid.UUID `json:"schedule_id"`
	TargetURL  string    `json:"target_url"`
	At         time.Time `json:"at"`
}

type calendarRun struct {
	ScanID     uuid.UUID `json:"scan_id"`
	ScheduleID uuid.UUID `json:"schedule_id"`
	TargetURL  string    `json:"target_url"`
	Status     string    `json:"status"`
	At         time.Time `json:"at"`
	Risk       *int      `json:"risk"`
}

// HandleCalendar zwraca terminy i uruchomienia harmonogramów konta w
// przedziale [from, to): zaplanowane od teraz i skany, które już zlecił
// któryś harmonogram.
func (h *ScheduleHandler) HandleCalendar(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	var query calendarQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httpx.FailValidation(c, err)
		return
	}
	if !query.To.After(query.From) || query.To.Sub(query.From) > maxCalendarSpan {
		httpx.FailWithFields(c, httpx.CodeValidationFailed, map[string]string{"to": "range"})
		return
	}

	ctx := c.Request.Context()
	db := h.db.WithContext(ctx)

	var active []models.Schedule
	if err := db.Where("user_id = ? AND enabled = ? AND blocked_at IS NULL", userID, true).Find(&active).Error; err != nil {
		log.Printf("Schedules: nie udało się pobrać harmonogramów do kalendarza: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	// Termin, który już minął, jest w kalendarzu skanem, a nie planem.
	from := query.From
	if now := h.now(); now.After(from) {
		from = now
	}
	planned := []calendarTerm{}
	for i := range active {
		cadence, err := schedules.FromSchedule(&active[i])
		if err != nil {
			continue
		}
		for _, at := range cadence.Between(from, query.To) {
			planned = append(planned, calendarTerm{ScheduleID: active[i].ID, TargetURL: active[i].TargetURL, At: at})
		}
	}

	var scans []struct {
		ID         uuid.UUID
		ScheduleID uuid.UUID
		TargetURL  string
		Status     string
		CreatedAt  time.Time
	}
	if err := db.Model(&models.PremiumScan{}).
		Select("id, schedule_id, target_url, status, created_at").
		Where("user_id = ? AND schedule_id IS NOT NULL AND created_at >= ? AND created_at < ?", userID, query.From, query.To).
		Order("created_at").
		Scan(&scans).Error; err != nil {
		log.Printf("Schedules: nie udało się pobrać uruchomień: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	ids := make([]uuid.UUID, len(scans))
	for i := range scans {
		ids[i] = scans[i].ID
	}
	risks, err := stats.ScanRisks(db, ids)
	if err != nil {
		log.Printf("Schedules: nie udało się policzyć ryzyka uruchomień: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	runs := make([]calendarRun, len(scans))
	for i, scan := range scans {
		runs[i] = calendarRun{ScanID: scan.ID, ScheduleID: scan.ScheduleID, TargetURL: scan.TargetURL, Status: scan.Status, At: scan.CreatedAt}
		if risk, ok := risks[scan.ID]; ok {
			runs[i].Risk = &risk
		}
	}

	c.JSON(http.StatusOK, gin.H{"planned": planned, "runs": runs})
}

// find szuka harmonogramu z :id należącego do konta.
func (h *ScheduleHandler) find(c *gin.Context, userID uuid.UUID) (*models.Schedule, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.FailWithFields(c, httpx.CodeValidationFailed, map[string]string{"id": "invalid"})
		return nil, false
	}
	var schedule models.Schedule
	err = h.db.WithContext(c.Request.Context()).Where("id = ? AND user_id = ?", id, userID).Take(&schedule).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httpx.Fail(c, httpx.CodeScheduleNotFound)
		return nil, false
	}
	if err != nil {
		log.Printf("Schedules: nie udało się pobrać harmonogramu %s: %v", id, err)
		httpx.Fail(c, httpx.CodeInternal)
		return nil, false
	}
	return &schedule, true
}

// nextRun liczy najbliższy termin zapisanego harmonogramu od teraz.
func (h *ScheduleHandler) nextRun(c *gin.Context, schedule *models.Schedule) (time.Time, bool) {
	cadence, err := schedules.FromSchedule(schedule)
	if err == nil {
		var next time.Time
		if next, err = cadence.Next(h.now()); err == nil {
			return next, true
		}
	}
	log.Printf("Schedules: harmonogram %s nie ma terminu: %v", schedule.ID, err)
	httpx.Fail(c, httpx.CodeInternal)
	return time.Time{}, false
}

// respond odsyła harmonogram w kształcie wiersza listy.
func (h *ScheduleHandler) respond(c *gin.Context, status int, schedule *models.Schedule) {
	if err := h.db.WithContext(c.Request.Context()).Take(schedule, "id = ?", schedule.ID).Error; err != nil {
		log.Printf("Schedules: nie udało się odczytać harmonogramu %s: %v", schedule.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	rows := []scheduleRow{newScheduleRow(schedule)}
	if err := h.describeSchedules(c.Request.Context(), rows); err != nil {
		log.Printf("Schedules: nie udało się opisać harmonogramu %s: %v", schedule.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	c.JSON(status, rows[0])
}
