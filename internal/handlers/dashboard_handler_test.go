package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/billing"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"github.com/prawo-i-piesc/backend/internal/stats"
	"gorm.io/gorm"
)

func dashboardRouter(db *gorm.DB, userID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewDashboardHandler(db)
	admin := NewAdminHandler(db, auth.NewSessionService(db, time.Hour), billing.New(db, nil, "", ""))

	r := gin.New()
	users := r.Group("/users", func(c *gin.Context) {
		c.Set("userID", userID.String())
		c.Next()
	})
	users.GET("/stats/summary", h.HandleSummary)
	users.GET("/stats/risk", h.HandleRiskTrend)
	users.GET("/stats/severity", h.HandleSeverity)
	users.GET("/stats/tests", h.HandleTests)
	users.GET("/stats/domains", h.HandleDomains)
	users.GET("/preferences/:key", h.HandleGetPreference)
	users.PUT("/preferences/:key", h.HandlePutPreference)
	r.GET("/admin/stats/tests", admin.HandleTestStats)
	return r
}

type seededResult struct {
	test     string
	severity string
	passed   bool
}

// seedPremium zakłada zakończony (albo inny) skan premium z wynikami.
func seedPremium(t *testing.T, db *gorm.DB, owner uuid.UUID, target, status string, at time.Time, results ...seededResult) uuid.UUID {
	t.Helper()
	id := newID(t)
	seedScan(t, db, &models.PremiumScan{ID: id, UserID: owner, TargetURL: target, Status: status, CreatedAt: at})
	for _, r := range results {
		seedScan(t, db, &models.ScanResult{ScanID: id, TestName: r.test, Severity: r.severity, Passed: r.passed})
	}
	return id
}

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("JSON (%s): %v", body, err)
	}
	return value
}

// seedStats zasiewa skany Jana w dwóch domenach, jeden stary skan poza oknem,
// jeden w toku i skan innego konta, który nie może się nigdzie wliczyć.
func seedStats(t *testing.T, db *gorm.DB) (jan uuid.UUID, scans map[string]uuid.UUID) {
	t.Helper()
	now := time.Now()
	jan = seedUser(t, db, "jan@example.com", "Jan", models.UserRoleUser, now)
	ola := seedUser(t, db, "ola@example.com", "Ola", models.UserRoleUser, now)

	scans = map[string]uuid.UUID{
		"shop-risky": seedPremium(t, db, jan, "https://shop.example", models.ScanStatusCompleted, now.Add(-72*time.Hour),
			seededResult{"csp", "CRITICAL", false}, seededResult{"hsts", "HIGH", false}, seededResult{"https", "INFO", true}),
		"shop-clean": seedPremium(t, db, jan, "https://shop.example", models.ScanStatusCompleted, now.Add(-24*time.Hour),
			seededResult{"csp", "CRITICAL", true}, seededResult{"hsts", "HIGH", true}, seededResult{"https", "INFO", true}),
		"blog": seedPremium(t, db, jan, "https://blog.example", models.ScanStatusCompleted, now.Add(-48*time.Hour),
			seededResult{"xframe", "MEDIUM", false}, seededResult{"serv-h-a", "NONE", false}, seededResult{"csp", "CRITICAL", true}),
		"old": seedPremium(t, db, jan, "https://old.example", models.ScanStatusCompleted, now.AddDate(0, 0, -200),
			seededResult{"csp", "CRITICAL", false}),
	}
	seedPremium(t, db, jan, "https://shop.example", models.ScanStatusRunning, now)
	seedPremium(t, db, ola, "https://ola.example", models.ScanStatusCompleted, now.Add(-time.Hour),
		seededResult{"csp", "CRITICAL", false}, seededResult{"cookie-sec", "LOW", false})
	return jan, scans
}

func TestStatsRiskTrendWeighsFailuresBySeverity(t *testing.T) {
	db := testDB(t)
	jan, scans := seedStats(t, db)
	r := dashboardRouter(db, jan)

	w := get(t, r, "/users/stats/risk")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	body := decode[struct {
		Days   int                `json:"days"`
		Points []stats.TrendPoint `json:"points"`
	}](t, w.Body.Bytes())

	if body.Days != 90 || len(body.Points) != 3 {
		t.Fatalf("days=%d points=%d, want 90 i trzy skany z okna (bez starego, bez skanu w toku i cudzego)", body.Days, len(body.Points))
	}
	want := []struct {
		id     uuid.UUID
		risk   int
		failed int
	}{
		{scans["shop-risky"], 88, 2}, // (100 + 75) / 2
		{scans["blog"], 28, 2},       // (50 + 5) / 2, NONE waży jak INFO
		{scans["shop-clean"], 0, 0},
	}
	for i, point := range body.Points {
		if point.ScanID != want[i].id || point.Risk != want[i].risk || point.Failed != want[i].failed || point.Tests != 3 {
			t.Errorf("punkt %d = %+v, want id=%s risk=%d failed=%d tests=3", i, point, want[i].id, want[i].risk, want[i].failed)
		}
	}

	w = get(t, r, "/users/stats/risk?domain=https://shop.example&days=365")
	body = decode[struct {
		Days   int                `json:"days"`
		Points []stats.TrendPoint `json:"points"`
	}](t, w.Body.Bytes())
	if len(body.Points) != 2 || body.Points[0].ScanID != scans["shop-risky"] {
		t.Errorf("filtr domeny = %+v, want dwa skany sklepu od najstarszego", body.Points)
	}

	if w := get(t, r, "/users/stats/risk?days=400"); w.Code != http.StatusBadRequest {
		t.Errorf("days=400: %d, want 400", w.Code)
	}
}

func TestStatsSeverityAndTopFailedTests(t *testing.T) {
	db := testDB(t)
	jan, _ := seedStats(t, db)
	r := dashboardRouter(db, jan)

	w := get(t, r, "/users/stats/severity")
	severity := decode[struct {
		Failed map[string]int `json:"failed"`
		Passed int            `json:"passed"`
	}](t, w.Body.Bytes())
	wantFailed := map[string]int{"CRITICAL": 1, "HIGH": 1, "MEDIUM": 1, "LOW": 0, "INFO": 1}
	for level, n := range wantFailed {
		if severity.Failed[level] != n {
			t.Errorf("failed[%s] = %d, want %d (całość: %v)", level, severity.Failed[level], n, severity.Failed)
		}
	}
	if severity.Passed != 5 {
		t.Errorf("passed = %d, want 5", severity.Passed)
	}

	w = get(t, r, "/users/stats/tests?limit=2")
	tests := decode[struct {
		Tests []stats.TestFailures `json:"tests"`
	}](t, w.Body.Bytes()).Tests
	if len(tests) != 2 || tests[0].TestName != "csp" || tests[0].Failures != 1 || tests[0].Runs != 3 {
		t.Errorf("testy = %+v, want najpierw csp: 1 porażka na 3 uruchomienia, limit 2", tests)
	}

	w = get(t, r, "/users/stats/tests?domain=https://shop.example")
	tests = decode[struct {
		Tests []stats.TestFailures `json:"tests"`
	}](t, w.Body.Bytes()).Tests
	for _, test := range tests {
		if test.TestName == "xframe" {
			t.Errorf("filtr domeny przepuścił test z innej domeny: %+v", tests)
		}
	}
}

func TestStatsSummaryAndDomains(t *testing.T) {
	db := testDB(t)
	jan, _ := seedStats(t, db)
	r := dashboardRouter(db, jan)

	w := get(t, r, "/users/stats/summary")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	summary := decode[struct {
		Scans   stats.StatusCounts `json:"scans"`
		Domains int                `json:"domains"`
		Tests   struct {
			Run    int `json:"run"`
			Failed int `json:"failed"`
		} `json:"tests"`
		Risk struct {
			Average *int `json:"average"`
			Latest  *int `json:"latest"`
		} `json:"risk"`
	}](t, w.Body.Bytes())

	if summary.Scans.Total != 5 || summary.Scans.Running != 1 || summary.Scans.Completed != 4 {
		t.Errorf("statusy = %+v, want 5 razem: 4 zakończone i 1 w toku", summary.Scans)
	}
	if summary.Domains != 2 || summary.Tests.Run != 9 || summary.Tests.Failed != 4 {
		t.Errorf("domeny=%d testy=%+v, want 2 domeny w oknie i 9 testów, 4 nieudane", summary.Domains, summary.Tests)
	}
	if summary.Risk.Average == nil || *summary.Risk.Average != 39 || summary.Risk.Latest == nil || *summary.Risk.Latest != 0 {
		t.Errorf("ryzyko = %v/%v, want średnio 39 ((88+28+0)/3) i ostatnio 0", summary.Risk.Average, summary.Risk.Latest)
	}

	// Konto bez skanów: null, nie zero.
	empty := seedUser(t, db, "pusty@example.com", "Pusty", models.UserRoleUser, time.Now())
	w = get(t, dashboardRouter(db, empty), "/users/stats/summary")
	if !strings.Contains(w.Body.String(), `"average":null`) {
		t.Errorf("puste konto: %s, want risk.average null", w.Body)
	}

	w = get(t, r, "/users/stats/domains")
	domains := decode[struct {
		Domains []stats.Domain `json:"domains"`
	}](t, w.Body.Bytes()).Domains
	if len(domains) != 3 || domains[0].TargetURL != "https://shop.example" || domains[0].Scans != 2 {
		t.Errorf("domeny = %+v, want trzy, najpierw sklep z dwoma skanami", domains)
	}
}

func TestPreferencesAreKeptPerAccount(t *testing.T) {
	db := testDB(t)
	jan := seedUser(t, db, "jan@example.com", "Jan", models.UserRoleUser, time.Now())
	ola := seedUser(t, db, "ola@example.com", "Ola", models.UserRoleUser, time.Now())
	r := dashboardRouter(db, jan)

	if w := get(t, r, "/users/preferences/dashboard.widgets"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"value":null`) {
		t.Fatalf("przed zapisem: %d %s, want 200 z value null", w.Code, w.Body)
	}

	layout := `[{"id":"riskTrend","type":"riskTrend","size":"2x2"}]`
	if w := send(t, r, http.MethodPut, "/users/preferences/dashboard.widgets", `{"value":`+layout+`}`); w.Code != http.StatusOK {
		t.Fatalf("zapis: %d %s", w.Code, w.Body)
	}
	second := `[{"id":"totalScans","type":"totalScans","size":"1x1"}]`
	send(t, r, http.MethodPut, "/users/preferences/dashboard.widgets", `{"value":`+second+`}`)

	w := get(t, r, "/users/preferences/dashboard.widgets")
	if !strings.Contains(w.Body.String(), `"totalScans"`) || strings.Contains(w.Body.String(), "riskTrend") {
		t.Errorf("odczyt = %s, want ostatnio zapisany układ", w.Body)
	}
	if w := get(t, dashboardRouter(db, ola), "/users/preferences/dashboard.widgets"); !strings.Contains(w.Body.String(), `"value":null`) {
		t.Errorf("inne konto widzi cudzy układ: %s", w.Body)
	}

	for name, req := range map[string]struct{ path, body string }{
		"nieznany klucz":  {"/users/preferences/theme", `{"value":"dark"}`},
		"null":            {"/users/preferences/dashboard.widgets", `{"value":null}`},
		"bez wartości":    {"/users/preferences/dashboard.widgets", `{}`},
		"za duża wartość": {"/users/preferences/dashboard.widgets", `{"value":"` + strings.Repeat("x", 17000) + `"}`},
	} {
		t.Run(name, func(t *testing.T) {
			w := send(t, r, http.MethodPut, req.path, req.body)
			if w.Code != http.StatusBadRequest || errorCode(t, w.Body.Bytes()) != httpx.CodeValidationFailed {
				t.Errorf("got %d %s, want 400 VALIDATION_FAILED", w.Code, w.Body)
			}
		})
	}
}

func TestAdminTestStatsCoverTheWholePlatform(t *testing.T) {
	db := testDB(t)
	jan, _ := seedStats(t, db)
	freeID := newID(t)
	seedScan(t, db, &models.Scan{ID: freeID, TargetURL: "https://free.example", Status: models.ScanStatusCompleted, CreatedAt: time.Now()})
	seedScan(t, db, &models.ScanResult{ScanID: freeID, TestName: "cookie-sec", Severity: "LOW", Passed: false})

	w := get(t, dashboardRouter(db, jan), "/admin/stats/tests")
	tests := decode[struct {
		Tests []stats.TestFailures `json:"tests"`
	}](t, w.Body.Bytes()).Tests

	failures := map[string]int{}
	for _, test := range tests {
		failures[test.TestName] = test.Failures
	}
	// csp: Jan (shop-risky) + Ola; cookie-sec: Ola + skan darmowy.
	if failures["csp"] != 2 || failures["cookie-sec"] != 2 {
		t.Errorf("porażki = %v, want csp=2 i cookie-sec=2 z kont i skanów darmowych", failures)
	}
}
