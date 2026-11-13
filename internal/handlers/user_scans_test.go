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

func userScansRouter(db *gorm.DB, userID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/users/scans", func(c *gin.Context) {
		c.Set("userID", userID.String())
		c.Next()
	}, NewScanHandler(nil, db, false).HandleUserScans)
	return r
}

type userScansPage struct {
	Scans []struct {
		ID        uuid.UUID `json:"id"`
		TargetURL string    `json:"target_url"`
		Status    string    `json:"status"`
		Tests     []string  `json:"tests"`
		Answered  int       `json:"answered"`
		Risk      *int      `json:"risk"`
		Results   any       `json:"results"`
	} `json:"scans"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func listUserScans(t *testing.T, r *gin.Engine, query string) userScansPage {
	t.Helper()
	w := get(t, r, "/users/scans"+query)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /users/scans%s: %d %s", query, w.Code, w.Body)
	}
	var page userScansPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

// seedUserScan zapisuje skan konta z wynikami; created mówi, ile minut temu.
func seedUserScan(t *testing.T, db *gorm.DB, owner uuid.UUID, target, status string, created int, results ...models.ScanResult) uuid.UUID {
	t.Helper()
	id := newID(t)
	seedScan(t, db, &models.PremiumScan{
		ID: id, UserID: owner, TargetURL: target, Status: status,
		CreatedAt: time.Now().Add(-time.Duration(created) * time.Minute),
		Tests:     models.PlannedTests([]string{"https", "hsts", "csp"}),
	})
	for _, result := range results {
		result.ScanID = id
		seedScan(t, db, &result)
	}
	return id
}

func TestUserScansArePagedNewestFirstWithATotal(t *testing.T) {
	db := testDB(t)
	owner, other := newID(t), newID(t)
	for i := range 7 {
		seedUserScan(t, db, owner, fmt.Sprintf("https://site%d.example.com", i), models.ScanStatusCompleted, i)
	}
	seedUserScan(t, db, other, "https://someone-else.example.com", models.ScanStatusCompleted, 0)
	r := userScansRouter(db, owner)

	first := listUserScans(t, r, "?limit=3")
	if first.Total != 7 || first.Limit != 3 || len(first.Scans) != 3 || first.Scans[0].TargetURL != "https://site0.example.com" {
		t.Fatalf("first page = %+v", first)
	}
	last := listUserScans(t, r, "?limit=3&offset=6")
	if len(last.Scans) != 1 || last.Scans[0].TargetURL != "https://site6.example.com" {
		t.Fatalf("last page = %+v", last)
	}
	if all := listUserScans(t, r, ""); all.Limit != listPageDefault || all.Total != 7 {
		t.Errorf("default page = limit %d total %d", all.Limit, all.Total)
	}
	if first.Scans[0].Results != nil {
		t.Errorf("rows carry results: %v", first.Scans[0].Results)
	}
}

func TestUserScansFilterByStatusAndAddress(t *testing.T) {
	db := testDB(t)
	owner := newID(t)
	seedUserScan(t, db, owner, "https://shop.example.com", models.ScanStatusPending, 1)
	seedUserScan(t, db, owner, "https://shop.example.com/cart", models.ScanStatusRunning, 2)
	seedUserScan(t, db, owner, "https://blog.example.org", models.ScanStatusCompleted, 3)
	seedUserScan(t, db, owner, "https://100%_off.example.net", models.ScanStatusFailed, 4)
	r := userScansRouter(db, owner)

	for query, want := range map[string]int{
		"?status=active":             2,
		"?status=COMPLETED":          1,
		"?status=FAILED":             1,
		"?q=SHOP":                    2,
		"?q=shop&status=active":      2,
		"?q=shop&status=COMPLETED":   0,
		"?q=" + url.QueryEscape("%"): 1,
		"?q=" + url.QueryEscape("_"): 1,
	} {
		if page := listUserScans(t, r, query); page.Total != want || len(page.Scans) != want {
			t.Errorf("%s: total %d, %d rows; want %d", query, page.Total, len(page.Scans), want)
		}
	}

	for _, query := range []string{"?status=PENDING", "?status=bogus", "?limit=101", "?offset=-1"} {
		w := get(t, r, "/users/scans"+query)
		if w.Code != http.StatusBadRequest || errorCode(t, w.Body.Bytes()) != httpx.CodeValidationFailed {
			t.Errorf("%s: %d %s", query, w.Code, w.Body)
		}
	}
}

func TestUserScansCarryProgressAndRisk(t *testing.T) {
	db := testDB(t)
	owner := newID(t)
	running := seedUserScan(t, db, owner, "https://running.example.com", models.ScanStatusRunning, 1,
		models.ScanResult{TestName: "HTTPS Protocol Verification", Severity: "None", Passed: true},
		// Komunikat silnika o przerwanym teście też jest odpowiedzią tego testu.
		models.ScanResult{TestName: "HSTS Header Analysis", Severity: "Info", Passed: true, Notice: true},
	)
	risky := seedUserScan(t, db, owner, "https://risky.example.com", models.ScanStatusCompleted, 2,
		models.ScanResult{TestName: "HTTPS Protocol Verification", Severity: "High", Passed: false},
		models.ScanResult{TestName: "CSP Analysis", Severity: "Low", Passed: false},
		models.ScanResult{TestName: "HSTS Header Analysis", Severity: "None", Passed: true},
	)
	blocked := seedUserScan(t, db, owner, "https://blocked.example.com", models.ScanStatusCompleted, 3,
		models.ScanResult{TestName: "HTTPS Protocol Verification", Severity: "Info", Passed: true, Notice: true},
	)
	empty := seedUserScan(t, db, owner, "https://pending.example.com", models.ScanStatusPending, 4)

	rows := map[uuid.UUID]int{}
	page := listUserScans(t, userScansRouter(db, owner), "")
	for i, scan := range page.Scans {
		rows[scan.ID] = i
	}
	at := func(id uuid.UUID) (int, *int) {
		scan := page.Scans[rows[id]]
		return scan.Answered, scan.Risk
	}

	if answered, risk := at(running); answered != 2 || risk == nil || *risk != 0 {
		t.Errorf("running: answered %d, risk %v; want 2 and 0", answered, risk)
	}
	if answered, risk := at(risky); answered != 3 || risk == nil || *risk == 0 {
		t.Errorf("risky: answered %d, risk %v; want 3 and a risk above 0", answered, risk)
	}
	// Same komunikaty silnika: skan niczego nie sprawdził, więc nie ma ryzyka, a nie „0”.
	if answered, risk := at(blocked); answered != 1 || risk != nil {
		t.Errorf("blocked: answered %d, risk %v; want 1 and none", answered, risk)
	}
	if answered, risk := at(empty); answered != 0 || risk != nil {
		t.Errorf("pending: answered %d, risk %v; want 0 and none", answered, risk)
	}
	if tests := page.Scans[rows[empty]].Tests; len(tests) != 3 {
		t.Errorf("planned tests = %v", tests)
	}
}
