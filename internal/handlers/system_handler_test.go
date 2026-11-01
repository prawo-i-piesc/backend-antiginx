package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/models"
	"github.com/prawo-i-piesc/backend/internal/monitoring"
)

type systemResponse struct {
	Checks     []SystemCheck       `json:"checks"`
	Scans      *SystemScans        `json:"scans"`
	Metrics    *monitoring.Metrics `json:"metrics"`
	GrafanaURL string              `json:"grafana_url"`
}

func systemRouter(h *SystemHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/system", h.HandleSystem)
	return r
}

func checkStatus(checks []SystemCheck, name string) string {
	for _, check := range checks {
		if check.Name == name {
			return check.Status
		}
	}
	return "missing"
}

func TestSystemReportsProbesAndTheScanQueue(t *testing.T) {
	db := testDB(t)
	owner := newID(t)
	now := time.Now()
	seedScan(t, db, &models.Scan{ID: newID(t), TargetURL: "https://a.example", Status: models.ScanStatusPending, CreatedAt: now})
	seedScan(t, db, &models.Scan{ID: newID(t), TargetURL: "https://b.example", Status: models.ScanStatusRunning, CreatedAt: now})
	seedScan(t, db, &models.PremiumScan{ID: newID(t), UserID: owner, TargetURL: "https://c.example", Status: models.ScanStatusPending, CreatedAt: now})
	seedScan(t, db, &models.PremiumScan{ID: newID(t), UserID: owner, TargetURL: "https://d.example", Status: models.ScanStatusCompleted, CreatedAt: now})

	probes := []monitoring.Probe{
		{Name: "db", Check: func(context.Context) error { return nil }},
		{Name: "rabbitmq", Local: true, Check: func(context.Context) error { return errors.New("kanał zamknięty") }},
	}
	w := get(t, systemRouter(NewSystemHandler(db, probes, nil, "https://grafana.example")), "/admin/system")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	body := decode[systemResponse](t, w.Body.Bytes())

	if checkStatus(body.Checks, "db") != "up" || checkStatus(body.Checks, "rabbitmq") != "down" {
		t.Errorf("sondy = %+v, want db up i rabbitmq down", body.Checks)
	}
	for _, check := range body.Checks {
		// Sonda w pamięci i wyłączony Prometheus nie mają czasu odpowiedzi.
		if hasLatency := check.LatencyMS != nil; hasLatency != (check.Name == "db") {
			t.Errorf("%s: latency_ms = %v", check.Name, check.LatencyMS)
		}
	}
	// Bez PROMETHEUS_URL to brak konfiguracji, a nie awaria.
	if checkStatus(body.Checks, "prometheus") != "disabled" || body.Metrics != nil {
		t.Errorf("prometheus = %s, metrics = %+v, want disabled i null", checkStatus(body.Checks, "prometheus"), body.Metrics)
	}
	if body.Scans == nil || *body.Scans != (SystemScans{Pending: 2, Running: 1}) {
		t.Errorf("kolejka = %+v, want 2 oczekujące (darmowy i premium) i 1 trwający", body.Scans)
	}
	if body.GrafanaURL != "https://grafana.example" {
		t.Errorf("grafana_url = %q", body.GrafanaURL)
	}
}

func TestSystemWithPrometheusReturnsMetrics(t *testing.T) {
	db := testDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/-/ready":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/query":
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"instance":"n1:9100"},"value":[1,"42"]}]}}`))
		default:
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1,"42"]]}]}}`))
		}
	}))
	defer server.Close()

	w := get(t, systemRouter(NewSystemHandler(db, nil, monitoring.NewPrometheus(server.URL), "")), "/admin/system")
	body := decode[systemResponse](t, w.Body.Bytes())

	if checkStatus(body.Checks, "prometheus") != "up" {
		t.Errorf("prometheus = %s, want up", checkStatus(body.Checks, "prometheus"))
	}
	if body.Metrics == nil || len(body.Metrics.Nodes) != 1 || len(body.Metrics.History.CPU) != 1 {
		t.Fatalf("metrics = %+v, want jeden węzeł i historię", body.Metrics)
	}
}

func TestSystemTellsFailingQueriesFromAnOutage(t *testing.T) {
	db := testDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"unknown metric"}`))
	}))
	defer server.Close()

	w := get(t, systemRouter(NewSystemHandler(db, nil, monitoring.NewPrometheus(server.URL), "")), "/admin/system")
	body := decode[systemResponse](t, w.Body.Bytes())
	if checkStatus(body.Checks, "prometheus") != "degraded" || body.Metrics != nil {
		t.Errorf("prometheus = %s, metrics = %+v, want degraded i null", checkStatus(body.Checks, "prometheus"), body.Metrics)
	}
}

func TestSystemSurvivesPrometheusOutage(t *testing.T) {
	db := testDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	w := get(t, systemRouter(NewSystemHandler(db, nil, monitoring.NewPrometheus(server.URL), "")), "/admin/system")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 z Prometheusem oznaczonym jako down", w.Code)
	}
	body := decode[systemResponse](t, w.Body.Bytes())
	if checkStatus(body.Checks, "prometheus") != "down" || body.Metrics != nil {
		t.Errorf("prometheus = %s, metrics = %+v, want down i null", checkStatus(body.Checks, "prometheus"), body.Metrics)
	}
}
