package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

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

func TestFreeScanLeavesOutActiveProbes(t *testing.T) {
	for _, probe := range []string{"dir-listing", "sensitive-files"} {
		if slices.Contains(FreeScanTests, probe) {
			t.Errorf("anonimowy skan zleca aktywny test %s", probe)
		}
		if !AllowedPremiumTests[probe] {
			t.Errorf("skan po zalogowaniu nie może zlecić %s", probe)
		}
	}
	if want := len(AvailableTestsList) - 2; len(FreeScanTests) != want {
		t.Errorf("darmowy skan ma %d testów, want %d", len(FreeScanTests), want)
	}
}
