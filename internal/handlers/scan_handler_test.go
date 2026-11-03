package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

func scanRouter(h *ScanHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/freescans", h.HandleScanSubmission)
	r.GET("/freescans/:id", h.HandleGetScan)
	return r
}

func seedScan(t *testing.T, db *gorm.DB, record any) {
	t.Helper()
	if err := db.Create(record).Error; err != nil {
		t.Fatalf("zasianie skanu: %v", err)
	}
}

func errorCode(t *testing.T, body []byte) httpx.Code {
	t.Helper()
	var resp httpx.ErrorResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("odpowiedź nie jest JSON-em (%s): %v", body, err)
	}
	return resp.Code
}

func TestFreeScanRejectsInternalTargetsBeforeTouchingTheDatabase(t *testing.T) {
	db := testDB(t)
	r := scanRouter(NewScanHandler(nil, db, false))

	for _, target := range []string{"http://127.0.0.1", "http://10.0.0.1", "not a url", "http://rabbitmq"} {
		t.Run(target, func(t *testing.T) {
			w := post(t, r, "/freescans", fmt.Sprintf(`{"target_url":%q}`, target))
			if w.Code != http.StatusBadRequest || errorCode(t, w.Body.Bytes()) != httpx.CodeValidationFailed {
				t.Errorf("got %d %s, want 400 VALIDATION_FAILED", w.Code, w.Body)
			}
		})
	}

	var count int64
	db.Model(&models.Scan{}).Count(&count)
	if count != 0 {
		t.Errorf("zapisano %d skanów dla odrzuconych adresów", count)
	}
}

func TestGetScanErrorsFollowTheContract(t *testing.T) {
	r := scanRouter(NewScanHandler(nil, testDB(t), false))

	for _, tc := range []struct {
		path   string
		status int
		code   httpx.Code
	}{
		{"/freescans/nie-uuid", http.StatusBadRequest, httpx.CodeValidationFailed},
		{"/freescans/" + uuid.NewString(), http.StatusNotFound, httpx.CodeScanNotFound},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := get(t, r, tc.path)
			if w.Code != tc.status || errorCode(t, w.Body.Bytes()) != tc.code {
				t.Errorf("got %d %s, want %d %s", w.Code, w.Body, tc.status, tc.code)
			}
		})
	}
}

func send(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func get(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	return send(t, r, http.MethodGet, path, "")
}

// Lista silnika z --tests w App/parser/config/paramregistry.go. Identyfikator,
// którego silnik nie zna, wywraca całe zadanie, więc skan kończy się FAILED bez
// wyników. Ten test nie widzi repozytorium silnika: pilnuje listy przed
// przypadkową zmianą, a przy zmianie w silniku trzeba go poprawić razem z nią.
var engineTests = []string{
	"https", "hsts", "serv-h-a", "csp", "cookie-sec", "js-obf", "xframe", "permissions-policy",
	"x-content-type-options", "referrer-policy", "ssl-cert", "cross-origin-x", "sitemap", "robots-txt",
	"dir-listing", "sensitive-files", "phishing-url", "bot-protection", "dns-reputation", "urlhaus",
	"favicon-origin", "cache-control",
}

func TestCatalogueMatchesTheEngine(t *testing.T) {
	got := slices.Sorted(slices.Values(AvailableTestsList))
	want := slices.Sorted(slices.Values(engineTests))
	if !slices.Equal(got, want) {
		t.Errorf("katalog testów = %v\nwant %v", got, want)
	}
}

func TestFreeScanLeavesOutProTests(t *testing.T) {
	for _, test := range []string{"dir-listing", "sensitive-files"} {
		if slices.Contains(FreeScanTests, test) {
			t.Errorf("anonimowy skan zleca test z planu Pro: %s", test)
		}
		if !proTests[test] || !AllowedPremiumTests[test] {
			t.Errorf("%s: pro = %v, można zlecić = %v, want oba true", test, proTests[test], AllowedPremiumTests[test])
		}
	}
	if want := len(AvailableTestsList) - 2; len(FreeScanTests) != want {
		t.Errorf("darmowy skan ma %d testów, want %d", len(FreeScanTests), want)
	}
}

func premiumScanRouter(db *gorm.DB, userID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/scans", func(c *gin.Context) {
		c.Set("userID", userID.String())
		c.Next()
	}, NewScanHandler(nil, db, true).HandlePremiumScanSubmission)
	return r
}

func TestProTestsNeedThePlan(t *testing.T) {
	db := testDB(t)
	free := seedUser(t, db, "free@example.com", "Free", models.UserRoleUser, time.Now())
	lapsed := seedUser(t, db, "lapsed@example.com", "Lapsed", models.UserRoleUser, time.Now())
	pro := seedUser(t, db, "pro@example.com", "Pro", models.UserRoleUser, time.Now())
	seedScan(t, db, &models.Subscription{UserID: lapsed, StripeCustomerID: "cus_lapsed", Status: "canceled"})
	seedScan(t, db, &models.Subscription{UserID: pro, StripeCustomerID: "cus_pro", Status: "active"})

	body := `{"target_url":"https://example.com","tests":["https","sensitive-files"]}`
	for name, user := range map[string]uuid.UUID{"bez subskrypcji": free, "po anulowaniu": lapsed} {
		t.Run(name, func(t *testing.T) {
			w := send(t, premiumScanRouter(db, user), http.MethodPost, "/scans", body)
			if w.Code != http.StatusForbidden || errorCode(t, w.Body.Bytes()) != httpx.CodeProRequired {
				t.Errorf("got %d %s, want 403 PRO_REQUIRED", w.Code, w.Body)
			}
		})
	}
	var queued int64
	db.Model(&models.PremiumScan{}).Count(&queued)
	if queued != 0 {
		t.Errorf("odrzucone zlecenia zapisały %d skanów, want 0", queued)
	}

	h := NewScanHandler(nil, db, true)
	for _, tc := range []struct {
		name  string
		user  uuid.UUID
		tests []string
		want  bool
	}{
		{"Pro, test Pro", pro, []string{"https", "dir-listing"}, true},
		{"Free, test Pro", free, []string{"https", "dir-listing"}, false},
		{"Free, bez testów Pro", free, []string{"https", "robots-txt"}, true},
	} {
		got, err := h.mayRunTests(tc.user, tc.tests)
		if err != nil || got != tc.want {
			t.Errorf("%s: mayRunTests = %v, %v, want %v", tc.name, got, err, tc.want)
		}
	}
}

func TestCatalogueNamesTheProTests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/tests", NewScanHandler(nil, nil, true).HandleAvailableScans)

	groups := decode[[]TestCategoryGroup](t, get(t, r, "/tests").Body.Bytes())
	var named []string
	for _, group := range groups {
		named = append(named, group.ProTests...)
		for _, test := range group.ProTests {
			if !slices.Contains(group.Tests, test) {
				t.Errorf("%s w pro_tests, ale nie w tests grupy %q", test, group.CategoryName)
			}
		}
	}
	if got := slices.Sorted(slices.Values(named)); !slices.Equal(got, []string{"dir-listing", "sensitive-files"}) {
		t.Errorf("pro_tests = %v", got)
	}
}
