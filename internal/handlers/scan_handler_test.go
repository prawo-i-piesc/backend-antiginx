package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	r.POST("/results", h.HandleResultSubmission)
	r.GET("/freescans/:id", h.HandleGetScan)
	return r
}

func resultBody(scanID uuid.UUID, name string, endFlag bool, resultType ResultType) string {
	return fmt.Sprintf(
		`{"target":"https://example.com","testId":%q,"result":{"Name":%q,"ThreatLevel":"High","Metadata":{"k":"v"},"Description":"opis"},"endFlag":%t,"resultType":%d,"message":{"Message":"blocked","Code":403}}`,
		scanID, name, endFlag, resultType,
	)
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

func TestResultMovesFreeScanToRunningAndCompletes(t *testing.T) {
	db := testDB(t)
	r := scanRouter(NewScanHandler(nil, db))

	scanID := newID(t)
	seedScan(t, db, &models.Scan{ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusPending, CreatedAt: time.Now()})

	for i, name := range []string{"https", "hsts"} {
		if w := post(t, r, "/results", resultBody(scanID, name, false, Success)); w.Code != http.StatusOK {
			t.Fatalf("wynik %d: status = %d, body = %s", i, w.Code, w.Body)
		}
	}

	var scan models.Scan
	if err := db.Preload("Results").First(&scan, "id = ?", scanID).Error; err != nil {
		t.Fatalf("odczyt skanu: %v", err)
	}
	if scan.Status != models.ScanStatusRunning || scan.StartedAt == nil {
		t.Errorf("status = %s, started_at = %v, want RUNNING z ustawionym started_at", scan.Status, scan.StartedAt)
	}
	if len(scan.Results) != 2 {
		t.Fatalf("zapisano %d wyników, want 2", len(scan.Results))
	}
	if scan.Results[0].Passed {
		t.Error("ThreatLevel High zapisany jako passed")
	}

	if w := post(t, r, "/results", resultBody(scanID, "", true, Success)); w.Code != http.StatusOK {
		t.Fatalf("domknięcie: status = %d, body = %s", w.Code, w.Body)
	}
	if err := db.First(&scan, "id = ?", scanID).Error; err != nil {
		t.Fatalf("odczyt skanu: %v", err)
	}
	if scan.Status != models.ScanStatusCompleted || scan.CompletedAt == nil {
		t.Errorf("status = %s, want COMPLETED z completed_at", scan.Status)
	}
}

func TestResultResolvesPremiumScans(t *testing.T) {
	db := testDB(t)
	r := scanRouter(NewScanHandler(nil, db))

	scanID := newID(t)
	seedScan(t, db, &models.PremiumScan{ID: scanID, UserID: newID(t), TargetURL: "https://example.com", Status: models.ScanStatusPending, CreatedAt: time.Now()})

	if w := post(t, r, "/results", resultBody(scanID, "", false, Message)); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}

	var scan models.PremiumScan
	if err := db.Preload("Results").First(&scan, "id = ?", scanID).Error; err != nil {
		t.Fatalf("odczyt skanu: %v", err)
	}
	if scan.Status != models.ScanStatusRunning {
		t.Errorf("status = %s, want RUNNING", scan.Status)
	}
	if len(scan.Results) != 1 || scan.Results[0].TestName != "Engine/Execution Error" {
		t.Errorf("results = %+v, want jeden komunikat silnika", scan.Results)
	}
}

func TestResultForUnknownScan(t *testing.T) {
	db := testDB(t)
	r := scanRouter(NewScanHandler(nil, db))

	w := post(t, r, "/results", resultBody(newID(t), "https", false, Success))
	if w.Code != http.StatusNotFound || errorCode(t, w.Body.Bytes()) != httpx.CodeScanNotFound {
		t.Errorf("got %d %s, want 404 SCAN_NOT_FOUND", w.Code, w.Body)
	}
}

func TestResultWithMalformedScanID(t *testing.T) {
	r := scanRouter(NewScanHandler(nil, testDB(t)))

	w := post(t, r, "/results", `{"testId":"nie-uuid","result":{"Name":"https"}}`)
	if w.Code != http.StatusBadRequest || errorCode(t, w.Body.Bytes()) != httpx.CodeValidationFailed {
		t.Errorf("got %d %s, want 400 VALIDATION_FAILED", w.Code, w.Body)
	}
}

func TestFreeScanRejectsInternalTargetsBeforeTouchingTheDatabase(t *testing.T) {
	db := testDB(t)
	r := scanRouter(NewScanHandler(nil, db))

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
	r := scanRouter(NewScanHandler(nil, testDB(t)))

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
