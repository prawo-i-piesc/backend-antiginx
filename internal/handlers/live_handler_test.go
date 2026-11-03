package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/live"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type liveEvent struct {
	Type    string              `json:"type"`
	Scan    json.RawMessage     `json:"scan"`
	Results []models.ScanResult `json:"results"`
	Status  string              `json:"status"`
}

func liveServer(t *testing.T, db *gorm.DB, hub *live.Hub, userID uuid.UUID) string {
	t.Helper()
	return liveServerWithExpiry(t, db, hub, userID, time.Now().Add(time.Hour))
}

func liveServerWithExpiry(t *testing.T, db *gorm.DB, hub *live.Hub, userID uuid.UUID, expires time.Time) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewLiveHandler(db, hub)

	r := gin.New()
	r.GET("/freescans/:id", h.HandleFreeScanLive)
	r.GET("/scans/:id", func(c *gin.Context) {
		c.Set("userID", userID.String())
		if !expires.IsZero() {
			c.Set(auth.TokenExpiryKey, expires)
		}
		c.Next()
	}, h.HandlePremiumScanLive)

	server := httptest.NewServer(r)
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func dialLive(t *testing.T, url string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, url, nil)
	if conn != nil {
		t.Cleanup(func() { _ = conn.CloseNow() })
	}
	return conn, resp, err
}

func readLive(t *testing.T, conn *websocket.Conn) liveEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var e liveEvent
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("nie JSON (%s): %v", data, err)
	}
	return e
}

func closeCode(t *testing.T, conn *websocket.Conn) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

func TestLiveFreeScanFromDatabaseToFinish(t *testing.T) {
	db := testDB(t)
	hub := live.NewHub()
	scanID := uuid.New()
	started := time.Now().Add(-time.Minute)
	seedScan(t, db, &models.Scan{
		ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusRunning,
		CreatedAt: started, StartedAt: &started, Tests: models.PlannedTests([]string{"https", "hsts"}),
	})
	seedScan(t, db, &models.ScanResult{ScanID: scanID, TestName: "https", Severity: "High", Metadata: datatypes.JSON(`{}`)})

	conn, _, err := dialLive(t, liveServer(t, db, hub, uuid.Nil)+"/freescans/"+scanID.String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	e := readLive(t, conn)
	if e.Type != "snapshot" {
		t.Fatalf("pierwsze zdarzenie = %+v", e)
	}
	var snapshot models.Scan
	if err := json.Unmarshal(e.Scan, &snapshot); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.ID != scanID || snapshot.TargetURL != "https://example.com" || snapshot.Status != models.ScanStatusRunning ||
		snapshot.StartedAt == nil || len(snapshot.Results) != 1 || !strings.Contains(string(snapshot.Tests), "hsts") {
		t.Errorf("snapshot ma mieć pola odpowiedzi REST: %s", e.Scan)
	}

	seedScan(t, db, &models.ScanResult{ScanID: scanID, TestName: "hsts", Severity: "None", Passed: true, Metadata: datatypes.JSON(`{}`)})
	hub.Notify(scanID)
	e = readLive(t, conn)
	if e.Type != "results" || len(e.Results) != 1 || e.Results[0].TestName != "hsts" {
		t.Fatalf("zdarzenie = %+v, want tylko nowy wynik", e)
	}

	if err := db.Model(&models.Scan{}).Where("id = ?", scanID).Updates(map[string]any{"status": models.ScanStatusCompleted, "completed_at": time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	hub.Notify(scanID)
	if e := readLive(t, conn); e.Type != "status" || e.Status != models.ScanStatusCompleted {
		t.Fatalf("zdarzenie = %+v, want status COMPLETED", e)
	}
	if code := closeCode(t, conn); code != websocket.StatusNormalClosure {
		t.Errorf("kod zamknięcia = %d, want 1000", code)
	}
}

func TestLiveReadsPremiumStateAndClosesFinishedScan(t *testing.T) {
	db := testDB(t)
	owner := seedAccount(t, db, "owner@example.com")
	scanID := uuid.New()
	now := time.Now()
	seedScan(t, db, &models.PremiumScan{ID: scanID, UserID: owner, TargetURL: "https://example.com", Status: models.ScanStatusCompleted, CreatedAt: now, StartedAt: &now, CompletedAt: &now})

	conn, _, err := dialLive(t, liveServer(t, db, live.NewHub(), owner)+"/scans/"+scanID.String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if e := readLive(t, conn); e.Type != "snapshot" {
		t.Fatalf("zdarzenie = %+v", e)
	}
	if code := closeCode(t, conn); code != websocket.StatusNormalClosure {
		t.Errorf("kod zamknięcia = %d, want 1000", code)
	}
}

func TestLiveRefusesBeforeUpgrade(t *testing.T) {
	db := testDB(t)
	owner := seedAccount(t, db, "owner@example.com")
	stranger := seedAccount(t, db, "stranger@example.com")
	scanID := uuid.New()
	seedScan(t, db, &models.PremiumScan{ID: scanID, UserID: owner, TargetURL: "https://example.com", Status: models.ScanStatusRunning, CreatedAt: time.Now()})

	tests := []struct {
		name string
		path string
		user uuid.UUID
		want int
	}{
		{"skan, którego nie ma", "/freescans/" + uuid.NewString(), uuid.Nil, http.StatusNotFound},
		{"nieprawidłowe ID", "/freescans/nie-uuid", uuid.Nil, http.StatusBadRequest},
		{"cudzy skan", "/scans/" + scanID.String(), stranger, http.StatusNotFound},
		// Skan z konta nie jest skanem darmowym: to osobna tabela i osobny dostęp.
		{"skan z konta przez darmową trasę", "/freescans/" + scanID.String(), uuid.Nil, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, resp, err := dialLive(t, liveServer(t, db, live.NewHub(), tt.user)+tt.path)
			if err == nil {
				t.Fatal("połączenie zostało przyjęte")
			}
			if resp == nil || resp.StatusCode != tt.want {
				t.Errorf("odpowiedź = %v, want %d", resp, tt.want)
			}
		})
	}
}

func TestLiveEnforcesConnectionLimitPerClient(t *testing.T) {
	db := testDB(t)
	scanID := uuid.New()
	seedScan(t, db, &models.Scan{ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusRunning, CreatedAt: time.Now()})
	url := liveServer(t, db, live.NewHub(), uuid.Nil) + "/freescans/" + scanID.String()

	for i := range 12 {
		conn, _, err := dialLive(t, url)
		if err != nil {
			t.Fatalf("połączenie %d: %v", i, err)
		}
		readLive(t, conn)
	}

	_, resp, err := dialLive(t, url)
	if err == nil {
		t.Fatal("przyjęto połączenie ponad limit")
	}
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("odpowiedź = %v, want 429", resp)
	}
}

func TestLiveRefusesPlainHTTPRequest(t *testing.T) {
	db := testDB(t)
	scanID := uuid.New()
	seedScan(t, db, &models.Scan{ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusRunning, CreatedAt: time.Now()})
	url := "http" + strings.TrimPrefix(liveServer(t, db, live.NewHub(), uuid.Nil), "ws") + "/freescans/" + scanID.String()

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 400 {
		t.Errorf("zwykły GET dostał %d, want błąd handshake'u", resp.StatusCode)
	}
}

func TestGetScanReturnsRequestedTests(t *testing.T) {
	db := testDB(t)
	r := scanRouter(NewScanHandler(nil, db, false))

	planned, legacy := uuid.New(), uuid.New()
	seedScan(t, db, &models.Scan{ID: planned, TargetURL: "https://example.com", Status: models.ScanStatusPending, CreatedAt: time.Now(), Tests: models.PlannedTests([]string{"https", "hsts"})})
	seedScan(t, db, &models.Scan{ID: legacy, TargetURL: "https://example.com", Status: models.ScanStatusCompleted, CreatedAt: time.Now()})

	read := func(id uuid.UUID) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/freescans/"+id.String(), nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET: %d %s", w.Code, w.Body)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	if tests, ok := read(planned)["tests"].([]any); !ok || len(tests) != 2 || tests[1] != "hsts" {
		t.Errorf("tests = %v, want [https hsts]", read(planned)["tests"])
	}
	if tests := read(legacy)["tests"]; tests != nil {
		t.Errorf("stary skan bez listy: tests = %v, want null", tests)
	}
}

func TestLiveDoesNotShowResultsOfAnotherScan(t *testing.T) {
	db := testDB(t)
	hub := live.NewHub()
	watched, other := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{watched, other} {
		seedScan(t, db, &models.Scan{ID: id, TargetURL: "https://example.com", Status: models.ScanStatusRunning, CreatedAt: time.Now()})
	}
	seedScan(t, db, &models.ScanResult{ScanID: other, TestName: "cudzy z początku", Severity: "High", Metadata: datatypes.JSON(`{}`)})

	conn, _, err := dialLive(t, liveServer(t, db, hub, uuid.Nil)+"/freescans/"+watched.String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if e := readLive(t, conn); strings.Contains(string(e.Scan), "cudzy") {
		t.Fatalf("pierwszy odczyt pokazał cudzy wynik: %s", e.Scan)
	}

	seedScan(t, db, &models.ScanResult{ScanID: other, TestName: "cudzy później", Severity: "High", Metadata: datatypes.JSON(`{}`)})
	seedScan(t, db, &models.ScanResult{ScanID: watched, TestName: "swój", Severity: "High", Metadata: datatypes.JSON(`{}`)})
	hub.Notify(watched)

	e := readLive(t, conn)
	if e.Type != "results" || len(e.Results) != 1 || e.Results[0].TestName != "swój" {
		t.Fatalf("zdarzenie = %+v, want tylko swój wynik", e)
	}
}

func TestLivePremiumStreamClosesWithTheToken(t *testing.T) {
	db := testDB(t)
	owner := seedAccount(t, db, "owner@example.com")
	scanID := uuid.New()
	seedScan(t, db, &models.PremiumScan{ID: scanID, UserID: owner, TargetURL: "https://example.com", Status: models.ScanStatusRunning, CreatedAt: time.Now()})

	url := liveServerWithExpiry(t, db, live.NewHub(), owner, time.Now().Add(100*time.Millisecond)) + "/scans/" + scanID.String()
	conn, _, err := dialLive(t, url)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	readLive(t, conn)
	if code := closeCode(t, conn); code != live.CloseSessionExpired {
		t.Errorf("kod zamknięcia = %d, want %d", code, live.CloseSessionExpired)
	}
}

func TestLivePremiumStreamRefusesWithoutTokenExpiry(t *testing.T) {
	db := testDB(t)
	owner := seedAccount(t, db, "owner@example.com")
	scanID := uuid.New()
	seedScan(t, db, &models.PremiumScan{ID: scanID, UserID: owner, TargetURL: "https://example.com", Status: models.ScanStatusRunning, CreatedAt: time.Now()})

	_, resp, err := dialLive(t, liveServerWithExpiry(t, db, live.NewHub(), owner, time.Time{})+"/scans/"+scanID.String())
	if err == nil {
		t.Fatal("połączenie bez końca ważności tokenu zostało przyjęte")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("odpowiedź = %v, want 401", resp)
	}
}

func TestLiveReleasesTheSlotWhenAConnectionEnds(t *testing.T) {
	db := testDB(t)
	scanID := uuid.New()
	seedScan(t, db, &models.Scan{ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusRunning, CreatedAt: time.Now()})
	url := liveServer(t, db, live.NewHub(), uuid.Nil) + "/freescans/" + scanID.String()

	var first *websocket.Conn
	for i := range 12 {
		conn, _, err := dialLive(t, url)
		if err != nil {
			t.Fatalf("połączenie %d: %v", i, err)
		}
		readLive(t, conn)
		if i == 0 {
			first = conn
		}
	}
	if err := first.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("Close: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, resp, err := dialLive(t, url)
		if err == nil {
			readLive(t, conn)
			return
		}
		if resp == nil || resp.StatusCode != http.StatusTooManyRequests || time.Now().After(deadline) {
			t.Fatalf("miejsce po zamkniętym połączeniu nie wróciło: %v, %v", resp, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLiveLimitsHandshakesPerAddress(t *testing.T) {
	db := testDB(t)
	scanID := uuid.New()
	seedScan(t, db, &models.Scan{ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusRunning, CreatedAt: time.Now()})
	url := "http" + strings.TrimPrefix(liveServer(t, db, live.NewHub(), uuid.Nil), "ws") + "/freescans/" + scanID.String()

	status := 0
	for range liveHandshakesPerIP + 1 {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		status = resp.StatusCode
		_ = resp.Body.Close()
	}
	if status != http.StatusTooManyRequests {
		t.Errorf("próba ponad limit dostała %d, want 429", status)
	}
}
