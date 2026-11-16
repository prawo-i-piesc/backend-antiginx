package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

// scheduleClock to "teraz" w testach harmonogramów: poniedziałek, 07:00 UTC.
var scheduleClock = time.Date(2026, 11, 16, 7, 0, 0, 0, time.UTC)

func scheduleRouter(db *gorm.DB, userID uuid.UUID, limit int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewScheduleHandler(db, NewScanHandler(nil, db, false), limit)
	h.now = func() time.Time { return scheduleClock }

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", userID.String())
		c.Next()
	})
	r.GET("/schedules", h.HandleList)
	r.GET("/schedules/calendar", h.HandleCalendar)
	r.POST("/schedules", h.HandleCreate)
	r.PUT("/schedules/:id", h.HandleUpdate)
	r.PATCH("/schedules/:id", h.HandleSetState)
	r.DELETE("/schedules/:id", h.HandleDelete)
	r.GET("/admin/schedules", h.HandleAdminList)
	r.PUT("/admin/schedules/:id/block", h.HandleAdminBlock)
	r.DELETE("/admin/schedules/:id/block", h.HandleAdminUnblock)
	return r
}

func grantPro(t *testing.T, db *gorm.DB, userID uuid.UUID) {
	t.Helper()
	if err := db.Create(&models.PlanGrant{UserID: userID, GrantedBy: userID, GrantedAt: time.Now()}).Error; err != nil {
		t.Fatalf("nadanie Pro: %v", err)
	}
}

type scheduleResponse struct {
	ID          uuid.UUID  `json:"id"`
	TargetURL   string     `json:"target_url"`
	Tests       []string   `json:"tests"`
	Frequency   string     `json:"frequency"`
	Weekday     *int       `json:"weekday"`
	MonthDay    *int       `json:"month_day"`
	Enabled     bool       `json:"enabled"`
	Blocked     bool       `json:"blocked"`
	BlockReason string     `json:"block_reason"`
	NextRunAt   *time.Time `json:"next_run_at"`
	Runs        int        `json:"runs"`
	LastScan    *struct {
		ID     uuid.UUID `json:"id"`
		Status string    `json:"status"`
	} `json:"last_scan"`
	UserEmail *string `json:"user_email"`
}

const weeklyBody = `{"target_url":"https://example.com","tests":["https","csp","https","nope"],"frequency":"weekly","time":"08:00","weekday":5,"month_day":12,"timezone":"Europe/Warsaw"}`

func createSchedule(t *testing.T, r *gin.Engine, body string) scheduleResponse {
	t.Helper()
	w := send(t, r, http.MethodPost, "/schedules", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /schedules: %d %s", w.Code, w.Body)
	}
	return decode[scheduleResponse](t, w.Body.Bytes())
}

func TestCreateScheduleNeedsPro(t *testing.T) {
	db := testDB(t)
	owner := seedUser(t, db, "free@example.com", "", models.UserRoleUser, time.Now())
	r := scheduleRouter(db, owner, 10)

	w := send(t, r, http.MethodPost, "/schedules", weeklyBody)
	if w.Code != http.StatusForbidden || errorCode(t, w.Body.Bytes()) != httpx.CodeProRequired {
		t.Fatalf("got %d %s, want 403 PRO_REQUIRED", w.Code, w.Body)
	}
	var count int64
	db.Model(&models.Schedule{}).Count(&count)
	if count != 0 {
		t.Errorf("saved %d schedules without Pro", count)
	}
}

func TestCreateSchedule(t *testing.T) {
	db := testDB(t)
	owner := seedUser(t, db, "pro@example.com", "", models.UserRoleUser, time.Now())
	grantPro(t, db, owner)
	r := scheduleRouter(db, owner, 10)

	got := createSchedule(t, r, weeklyBody)
	if !got.Enabled || got.Frequency != "weekly" || got.Weekday == nil || *got.Weekday != 5 {
		t.Errorf("schedule = %+v, want an enabled weekly one on Friday", got)
	}
	// Dzień miesiąca nie należy do cyklu tygodniowego, a testy są bez
	// powtórek i nieznanych.
	if got.MonthDay != nil || len(got.Tests) != 2 {
		t.Errorf("month_day = %v, tests = %v; want none and [https csp]", got.MonthDay, got.Tests)
	}
	// Piątek 20 listopada, 08:00 w Warszawie, czyli 07:00 UTC.
	if want := time.Date(2026, 11, 20, 7, 0, 0, 0, time.UTC); got.NextRunAt == nil || !got.NextRunAt.Equal(want) {
		t.Errorf("next_run_at = %v, want %s", got.NextRunAt, want)
	}

	list := decode[struct {
		Schedules []scheduleResponse `json:"schedules"`
		Limit     int                `json:"limit"`
	}](t, get(t, r, "/schedules").Body.Bytes())
	if len(list.Schedules) != 1 || list.Limit != 10 {
		t.Errorf("list = %+v, want one schedule and the limit", list)
	}
}

func TestCreateScheduleValidates(t *testing.T) {
	db := testDB(t)
	owner := seedUser(t, db, "pro@example.com", "", models.UserRoleUser, time.Now())
	grantPro(t, db, owner)
	r := scheduleRouter(db, owner, 10)

	for _, tc := range []struct {
		name, body string
		code       httpx.Code
		field      string
	}{
		{"weekday", `{"target_url":"https://example.com","tests":["https"],"frequency":"weekly","time":"08:00","timezone":"UTC"}`, httpx.CodeValidationFailed, "weekday"},
		{"timezone", `{"target_url":"https://example.com","tests":["https"],"frequency":"daily","time":"08:00","timezone":"Nowhere/Land"}`, httpx.CodeValidationFailed, "timezone"},
		{"private target", `{"target_url":"http://10.0.0.1","tests":["https"],"frequency":"daily","time":"08:00","timezone":"UTC"}`, httpx.CodeValidationFailed, "target_url"},
		{"tests", `{"target_url":"https://example.com","tests":["nope"],"frequency":"daily","time":"08:00","timezone":"UTC"}`, httpx.CodeNoValidTests, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := send(t, r, http.MethodPost, "/schedules", tc.body)
			resp := decode[httpx.ErrorResponse](t, w.Body.Bytes())
			if resp.Code != tc.code {
				t.Fatalf("got %d %s, want %s", w.Code, w.Body, tc.code)
			}
			if tc.field != "" && resp.Fields[tc.field] == "" {
				t.Errorf("fields = %v, want %s", resp.Fields, tc.field)
			}
		})
	}
}

func TestCreateScheduleLimit(t *testing.T) {
	db := testDB(t)
	owner := seedUser(t, db, "pro@example.com", "", models.UserRoleUser, time.Now())
	grantPro(t, db, owner)
	r := scheduleRouter(db, owner, 1)

	createSchedule(t, r, weeklyBody)
	w := send(t, r, http.MethodPost, "/schedules", weeklyBody)
	if w.Code != http.StatusConflict || errorCode(t, w.Body.Bytes()) != httpx.CodeScheduleLimit {
		t.Errorf("got %d %s, want 409 SCHEDULE_LIMIT_REACHED", w.Code, w.Body)
	}
}

func TestPauseResumeAndBlock(t *testing.T) {
	db := testDB(t)
	owner := seedUser(t, db, "pro@example.com", "", models.UserRoleUser, time.Now())
	grantPro(t, db, owner)
	r := scheduleRouter(db, owner, 10)
	s := createSchedule(t, r, weeklyBody)
	path := "/schedules/" + s.ID.String()

	paused := decode[scheduleResponse](t, send(t, r, http.MethodPatch, path, `{"enabled":false}`).Body.Bytes())
	if paused.Enabled || paused.NextRunAt != nil {
		t.Errorf("paused = %+v, want disabled without a next run", paused)
	}

	// Administrator blokuje; użytkownik nie może wznowić.
	block := send(t, r, http.MethodPut, "/admin"+path+"/block", `{"reason":"Cel prosił o wstrzymanie"}`)
	if blocked := decode[scheduleResponse](t, block.Body.Bytes()); !blocked.Blocked || blocked.BlockReason == "" || blocked.UserEmail == nil {
		t.Fatalf("block = %d %s, want a blocked schedule with a reason and its owner", block.Code, block.Body)
	}
	w := send(t, r, http.MethodPatch, path, `{"enabled":true}`)
	if w.Code != http.StatusConflict || errorCode(t, w.Body.Bytes()) != httpx.CodeScheduleBlocked {
		t.Errorf("resume while blocked: %d %s, want 409 SCHEDULE_BLOCKED", w.Code, w.Body)
	}

	if w := send(t, r, http.MethodDelete, "/admin"+path+"/block", ""); w.Code != http.StatusOK {
		t.Fatalf("unblock: %d %s", w.Code, w.Body)
	}
	resumed := decode[scheduleResponse](t, send(t, r, http.MethodPatch, path, `{"enabled":true}`).Body.Bytes())
	if !resumed.Enabled || resumed.Blocked || resumed.NextRunAt == nil {
		t.Errorf("resumed = %+v, want an active schedule with a next run", resumed)
	}
}

func TestUpdateAndDeleteOnlyOwnSchedules(t *testing.T) {
	db := testDB(t)
	owner := seedUser(t, db, "pro@example.com", "", models.UserRoleUser, time.Now())
	other := seedUser(t, db, "other@example.com", "", models.UserRoleUser, time.Now())
	grantPro(t, db, owner)
	grantPro(t, db, other)
	s := createSchedule(t, scheduleRouter(db, owner, 10), weeklyBody)
	path := "/schedules/" + s.ID.String()

	stranger := scheduleRouter(db, other, 10)
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		body := map[string]string{http.MethodPut: weeklyBody, http.MethodPatch: `{"enabled":false}`}[method]
		if w := send(t, stranger, method, path, body); w.Code != http.StatusNotFound {
			t.Errorf("%s by another account: %d, want 404", method, w.Code)
		}
	}

	mine := scheduleRouter(db, owner, 10)
	monthly := `{"target_url":"https://example.org","tests":["hsts"],"frequency":"monthly","time":"06:00","month_day":3,"weekday":2,"timezone":"UTC"}`
	updated := decode[scheduleResponse](t, send(t, mine, http.MethodPut, path, monthly).Body.Bytes())
	if updated.Frequency != "monthly" || updated.Weekday != nil || updated.MonthDay == nil || *updated.MonthDay != 3 || updated.TargetURL != "https://example.org" {
		t.Errorf("updated = %+v, want the monthly definition", updated)
	}

	if w := send(t, mine, http.MethodDelete, path, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := send(t, mine, http.MethodDelete, path, ""); w.Code != http.StatusNotFound {
		t.Errorf("second delete: %d, want 404", w.Code)
	}
}

func TestCalendarAndScheduledScans(t *testing.T) {
	db := testDB(t)
	owner := seedUser(t, db, "pro@example.com", "", models.UserRoleUser, time.Now())
	grantPro(t, db, owner)
	r := scheduleRouter(db, owner, 10)
	s := createSchedule(t, r, `{"target_url":"https://example.com","tests":["https"],"frequency":"daily","time":"09:00","timezone":"UTC"}`)

	// Dwa skany z harmonogramu wczoraj i przedwczoraj, jeden z ręki.
	for _, daysAgo := range []int{1, 2} {
		id := newID(t)
		seedScan(t, db, &models.PremiumScan{
			ID: id, UserID: owner, TargetURL: "https://example.com", Status: models.ScanStatusCompleted,
			CreatedAt: scheduleClock.AddDate(0, 0, -daysAgo).Add(2 * time.Hour), ScheduleID: &s.ID,
		})
	}
	seedUserScan(t, db, owner, "https://example.com", models.ScanStatusCompleted, 5)

	from := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	w := get(t, r, fmt.Sprintf("/schedules/calendar?from=%s&to=%s", url.QueryEscape(from.Format(time.RFC3339)), url.QueryEscape(to.Format(time.RFC3339Nano))))
	calendar := decode[struct {
		Planned []struct {
			At time.Time `json:"at"`
		} `json:"planned"`
		Runs []struct {
			ScanID uuid.UUID `json:"scan_id"`
		} `json:"runs"`
	}](t, w.Body.Bytes())
	// Od teraz (16.11, 07:00) do 21.11: 09:00 w dniach 16–20.
	if len(calendar.Planned) != 5 || len(calendar.Runs) != 2 {
		t.Errorf("calendar = %d planned, %d runs; want 5 and 2 (%s)", len(calendar.Planned), len(calendar.Runs), w.Body)
	}

	if w := get(t, r, "/schedules/calendar?from=2026-11-01T00:00:00Z&to=2027-01-01T00:00:00Z"); w.Code != http.StatusBadRequest {
		t.Errorf("two-month range: %d, want 400", w.Code)
	}

	list := decode[scheduleResponse](t, mustFind(t, get(t, r, "/schedules").Body.Bytes()))
	if list.Runs != 2 || list.LastScan == nil {
		t.Errorf("runs = %d, last_scan = %v; want 2 and the latest run", list.Runs, list.LastScan)
	}

	page := listUserScans(t, userScansRouter(db, owner), "?schedule="+s.ID.String())
	if page.Total != 2 {
		t.Errorf("scans of the schedule: %d, want 2", page.Total)
	}
}

// mustFind wyciąga pierwszy harmonogram z listy.
func mustFind(t *testing.T, body []byte) []byte {
	t.Helper()
	list := decode[struct {
		Schedules []json.RawMessage `json:"schedules"`
	}](t, body)
	if len(list.Schedules) == 0 {
		t.Fatalf("no schedules in %s", body)
	}
	return list.Schedules[0]
}

func TestAdminListSchedules(t *testing.T) {
	db := testDB(t)
	alice := seedUser(t, db, "alice@example.com", "", models.UserRoleUser, time.Now())
	bob := seedUser(t, db, "bob@example.com", "", models.UserRoleUser, time.Now())
	grantPro(t, db, alice)
	grantPro(t, db, bob)
	createSchedule(t, scheduleRouter(db, alice, 10), weeklyBody)
	paused := createSchedule(t, scheduleRouter(db, bob, 10), `{"target_url":"https://bob.example","tests":["https"],"frequency":"daily","time":"08:00","timezone":"UTC"}`)
	send(t, scheduleRouter(db, bob, 10), http.MethodPatch, "/schedules/"+paused.ID.String(), `{"enabled":false}`)

	admin := scheduleRouter(db, alice, 10)
	type page struct {
		Schedules []scheduleResponse `json:"schedules"`
		Total     int                `json:"total"`
	}
	for query, want := range map[string]int{"": 2, "?q=BOB@": 1, "?q=bob.example": 1, "?state=paused": 1, "?state=blocked": 0, "?user_id=" + alice.String(): 1} {
		got := decode[page](t, get(t, admin, "/admin/schedules"+query).Body.Bytes())
		if got.Total != want || len(got.Schedules) != want {
			t.Errorf("%q: total %d, rows %d; want %d", query, got.Total, len(got.Schedules), want)
		}
	}
}

func TestDeletingTheAccountDeletesSchedules(t *testing.T) {
	db := testDB(t)
	owner := seedUser(t, db, "pro@example.com", "", models.UserRoleUser, time.Now())
	grantPro(t, db, owner)
	createSchedule(t, scheduleRouter(db, owner, 10), weeklyBody)

	if err := deleteUserCascade(db, owner); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&models.Schedule{}).Count(&count)
	if count != 0 {
		t.Errorf("%d schedules outlived the account", count)
	}
}
