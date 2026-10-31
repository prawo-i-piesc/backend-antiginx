package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func reportRouter(h *ScanHandler, userID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/freescans/:id/report", h.HandleFreeScanReport)
	r.GET("/scans/:id/report", func(c *gin.Context) {
		c.Set("userID", userID.String())
		c.Next()
	}, h.HandlePremiumScanReport)
	return r
}

func seedPremiumScanWithResults(t *testing.T, db *gorm.DB, owner uuid.UUID, status string) uuid.UUID {
	t.Helper()
	id := newID(t)
	done := time.Now()
	seedScan(t, db, &models.PremiumScan{ID: id, UserID: owner, TargetURL: "https://example.com", Status: status, CreatedAt: done.Add(-time.Minute), CompletedAt: &done})
	seedScan(t, db, &models.ScanResult{ScanID: id, TestName: "HSTS Header Analysis", Severity: "Medium", Passed: false, Message: "HSTS missing", Metadata: datatypes.JSON(`{}`)})
	seedScan(t, db, &models.ScanResult{ScanID: id, TestName: "HTTPS Protocol Verification", Severity: "None", Passed: true, Message: "ok", Metadata: datatypes.JSON(`{}`)})
	return id
}

func TestPremiumReportIsServedToTheOwnerInEveryFormat(t *testing.T) {
	db := testDB(t)
	owner := newID(t)
	scanID := seedPremiumScanWithResults(t, db, owner, models.ScanStatusCompleted)
	r := reportRouter(NewScanHandler(nil, db), owner)

	for _, tc := range []struct {
		query, contentType, ext string
		body                    []byte
	}{
		{"", "application/pdf", "pdf", []byte("%PDF-")},
		{"?format=pdf", "application/pdf", "pdf", []byte("%PDF-")},
		{"?format=html", "text/html; charset=utf-8", "html", []byte("<!doctype html>")},
		{"?format=md", "text/markdown; charset=utf-8", "md", []byte("# AntiGinx security report")},
		{"?format=sarif", "application/sarif+json", "sarif", []byte("{")},
	} {
		t.Run("format"+tc.query, func(t *testing.T) {
			w := get(t, r, "/scans/"+scanID.String()+"/report"+tc.query)
			if w.Code != http.StatusOK {
				t.Fatalf("got %d %s", w.Code, w.Body)
			}
			if got := w.Header().Get("Content-Type"); got != tc.contentType {
				t.Errorf("Content-Type = %q, want %q", got, tc.contentType)
			}
			wantDisposition := `attachment; filename=antiginx-report-` + scanID.String() + "." + tc.ext
			if got := w.Header().Get("Content-Disposition"); got != wantDisposition {
				t.Errorf("Content-Disposition = %q, want %q", got, wantDisposition)
			}
			if got := w.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if !bytes.HasPrefix(w.Body.Bytes(), tc.body) {
				t.Errorf("treść zaczyna się od %q, want %q", w.Body.Bytes()[:min(w.Body.Len(), 20)], tc.body)
			}
		})
	}
}

func TestPremiumReportSARIFContainsTheScanResults(t *testing.T) {
	db := testDB(t)
	owner := newID(t)
	scanID := seedPremiumScanWithResults(t, db, owner, models.ScanStatusCompleted)

	w := get(t, reportRouter(NewScanHandler(nil, db), owner), "/scans/"+scanID.String()+"/report?format=sarif")

	var log struct {
		Runs []struct {
			Results []struct {
				RuleID string `json:"ruleId"`
				Kind   string `json:"kind"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &log); err != nil {
		t.Fatalf("SARIF nie jest JSON-em: %v", err)
	}
	got := log.Runs[0].Results
	if len(got) != 2 || got[0].RuleID != "hsts-header-analysis" || got[0].Kind != "fail" || got[1].Kind != "pass" {
		t.Errorf("results = %+v", got)
	}
}

func TestPremiumReportHidesOtherUsersScans(t *testing.T) {
	db := testDB(t)
	scanID := seedPremiumScanWithResults(t, db, newID(t), models.ScanStatusCompleted)

	w := get(t, reportRouter(NewScanHandler(nil, db), newID(t)), "/scans/"+scanID.String()+"/report")
	if w.Code != http.StatusNotFound || errorCode(t, w.Body.Bytes()) != httpx.CodeScanNotFound {
		t.Errorf("cudzy skan: got %d %s, want 404 SCAN_NOT_FOUND", w.Code, w.Body)
	}
}

func TestPremiumReportDoesNotServeAFreeScanByID(t *testing.T) {
	db := testDB(t)
	freeID := newID(t)
	seedScan(t, db, &models.Scan{ID: freeID, TargetURL: "https://example.com", Status: models.ScanStatusCompleted})

	w := get(t, reportRouter(NewScanHandler(nil, db), newID(t)), "/scans/"+freeID.String()+"/report")
	if w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", w.Code)
	}
}

func TestReportWaitsForTheScanToFinish(t *testing.T) {
	db := testDB(t)
	owner := newID(t)
	r := reportRouter(NewScanHandler(nil, db), owner)

	for _, status := range []string{models.ScanStatusPending, models.ScanStatusRunning} {
		t.Run(status, func(t *testing.T) {
			scanID := seedPremiumScanWithResults(t, db, owner, status)
			w := get(t, r, "/scans/"+scanID.String()+"/report")
			if w.Code != http.StatusConflict || errorCode(t, w.Body.Bytes()) != httpx.CodeScanNotFinished {
				t.Errorf("got %d %s, want 409 SCAN_NOT_FINISHED", w.Code, w.Body)
			}
		})
	}

	// Nieudany skan jest skończony: raport pokazuje, co zdążyło przyjść.
	failed := seedPremiumScanWithResults(t, db, owner, models.ScanStatusFailed)
	if w := get(t, r, "/scans/"+failed.String()+"/report?format=md"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "| Status | Failed |") {
		t.Errorf("FAILED: got %d", w.Code)
	}
}

func TestReportRejectsBadInput(t *testing.T) {
	db := testDB(t)
	owner := newID(t)
	scanID := seedPremiumScanWithResults(t, db, owner, models.ScanStatusCompleted)
	r := reportRouter(NewScanHandler(nil, db), owner)

	w := get(t, r, "/scans/"+scanID.String()+"/report?format=docx")
	var resp httpx.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("odpowiedź nie jest JSON-em: %v", err)
	}
	if w.Code != http.StatusBadRequest || resp.Code != httpx.CodeValidationFailed || resp.Fields["format"] != "oneof:pdf html md sarif" {
		t.Errorf("zły format: got %d %+v", w.Code, resp)
	}

	if w := get(t, r, "/scans/nie-uuid/report"); w.Code != http.StatusBadRequest {
		t.Errorf("zły UUID: got %d, want 400", w.Code)
	}

	// Oba złe parametry wracają w jednej odpowiedzi.
	w = get(t, r, "/scans/"+scanID.String()+"/report?format=docx&lang=de")
	resp = httpx.ErrorResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("odpowiedź nie jest JSON-em: %v", err)
	}
	if w.Code != http.StatusBadRequest || resp.Fields["format"] == "" || resp.Fields["lang"] != "oneof:en pl" {
		t.Errorf("zły format i język: got %d %+v", w.Code, resp)
	}
}

func TestReportFollowsTheRequestedLanguage(t *testing.T) {
	db := testDB(t)
	owner := newID(t)
	scanID := seedPremiumScanWithResults(t, db, owner, models.ScanStatusCompleted)
	r := reportRouter(NewScanHandler(nil, db), owner)

	for lang, want := range map[string]string{
		"":      "# AntiGinx security report",
		"en":    "# AntiGinx security report",
		"pl":    "# Raport bezpieczeństwa AntiGinx",
		"pl-PL": "| Status | Zakończony |",
	} {
		w := get(t, r, "/scans/"+scanID.String()+"/report?format=md&lang="+lang)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), want) {
			t.Errorf("lang=%q: got %d, want %q in body", lang, w.Code, want)
		}
	}
}

func TestHTMLReportGetsItsOwnCSP(t *testing.T) {
	db := testDB(t)
	owner := newID(t)
	scanID := seedPremiumScanWithResults(t, db, owner, models.ScanStatusCompleted)

	w := get(t, reportRouter(NewScanHandler(nil, db), owner), "/scans/"+scanID.String()+"/report?format=html")
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "style-src 'unsafe-inline'") || strings.Contains(csp, "script-src") {
		t.Errorf("CSP = %q", csp)
	}
}

func TestFreeScanReportIsPublic(t *testing.T) {
	db := testDB(t)
	freeID := newID(t)
	seedScan(t, db, &models.Scan{ID: freeID, TargetURL: "https://example.com", Status: models.ScanStatusCompleted})
	seedScan(t, db, &models.ScanResult{ScanID: freeID, TestName: "HSTS Header Analysis", Severity: "Medium", Message: "HSTS missing", Metadata: datatypes.JSON(`{}`)})
	r := reportRouter(NewScanHandler(nil, db), uuid.Nil)

	w := get(t, r, "/freescans/"+freeID.String()+"/report?format=md")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Free scan") || !strings.Contains(w.Body.String(), "HSTS missing") {
		t.Errorf("got %d %s", w.Code, w.Body)
	}

	if w := get(t, r, "/freescans/"+uuid.NewString()+"/report"); w.Code != http.StatusNotFound {
		t.Errorf("nieistniejący skan: got %d, want 404", w.Code)
	}
}
