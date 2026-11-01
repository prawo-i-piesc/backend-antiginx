package handlers

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/models"
	"github.com/prawo-i-piesc/backend/internal/monitoring"
	"gorm.io/gorm"
)

const (
	systemProbeTimeout   = 2 * time.Second
	systemMetricsTimeout = 5 * time.Second
)

// Stany sond w odpowiedzi. degraded: usługa odpowiada, ale nie robi tego, do
// czego jest potrzebna (Prometheus żyje, a zapytania padają). disabled: usługa,
// której ten serwer nie ma skonfigurowanej, a nie awaria.
const (
	checkUp       = "up"
	checkDown     = "down"
	checkDegraded = "degraded"
	checkDisabled = "disabled"
)

// SystemHandler to widok systemu dla panelu admina: sondy zależności, kolejka
// skanów i obciążenie serwera z Prometheusa.
type SystemHandler struct {
	db         *gorm.DB
	probes     []monitoring.Probe
	prometheus *monitoring.Prometheus
	grafanaURL string
}

// NewSystemHandler przyjmuje nil zamiast Prometheusa, gdy PROMETHEUS_URL nie
// jest ustawione.
func NewSystemHandler(db *gorm.DB, probes []monitoring.Probe, prometheus *monitoring.Prometheus, grafanaURL string) *SystemHandler {
	return &SystemHandler{db: db, probes: probes, prometheus: prometheus, grafanaURL: grafanaURL}
}

type SystemCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// Czas odpowiedzi sondy; null dla wyłączonej i dla sondy bez I/O.
	LatencyMS *int64 `json:"latency_ms"`
}

type SystemScans struct {
	Pending int64 `json:"pending"`
	Running int64 `json:"running"`
}

// HandleSystem zbiera wszystko w jednym żądaniu, bo panel odświeża widok co
// kilka sekund. Sondy i Prometheus idą równolegle; awaria Prometheusa nie
// psuje odpowiedzi, tylko zostawia metrics jako null.
func (h *SystemHandler) HandleSystem(c *gin.Context) {
	ctx := c.Request.Context()
	now := time.Now()

	checks := make([]SystemCheck, len(h.probes)+1)
	var wg sync.WaitGroup
	for i, probe := range h.probes {
		wg.Go(func() {
			checks[i] = runCheck(ctx, probe)
		})
	}

	var metrics *monitoring.Metrics
	prometheusAt := len(h.probes)
	if h.prometheus == nil {
		checks[prometheusAt] = SystemCheck{Name: "prometheus", Status: checkDisabled}
	} else {
		wg.Go(func() {
			// Osobna sonda gotowości odróżnia „nie ma Prometheusa” od „Prometheus
			// jest, ale zapytania padają” (np. po zmianie nazw metryk).
			check := runCheck(ctx, monitoring.Probe{Name: "prometheus", Check: h.prometheus.Ready})
			if check.Status == checkUp {
				collectCtx, cancel := context.WithTimeout(ctx, systemMetricsTimeout)
				defer cancel()
				collected, err := h.prometheus.Collect(collectCtx, now)
				if err != nil {
					log.Printf("System: zapytania do Prometheusa nie powiodły się: %v", err)
					check.Status = checkDegraded
				} else {
					metrics = collected
				}
			}
			checks[prometheusAt] = check
		})
	}

	var scans *SystemScans
	wg.Go(func() {
		scans = h.countScans(ctx)
	})
	wg.Wait()

	if ctx.Err() != nil {
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"checked_at":  now.UTC(),
		"checks":      checks,
		"scans":       scans,
		"metrics":     metrics,
		"grafana_url": h.grafanaURL,
	})
}

func runCheck(ctx context.Context, probe monitoring.Probe) SystemCheck {
	probeCtx, cancel := context.WithTimeout(ctx, systemProbeTimeout)
	defer cancel()

	start := time.Now()
	err := probe.Check(probeCtx)
	result := SystemCheck{Name: probe.Name, Status: checkUp}
	if !probe.Local {
		latency := time.Since(start).Milliseconds()
		result.LatencyMS = &latency
	}
	if err != nil {
		log.Printf("System: %s nie odpowiada: %v", probe.Name, err)
		result.Status = checkDown
	}
	return result
}

// countScans liczy skany darmowe i premium razem: oba rodzaje czekają w tej
// samej kolejce silnika. Jedno zapytanie na tabelę, z tym samym limitem czasu
// co sondy, żeby zawieszona baza nie zawiesiła widoku, który ma ją zgłosić.
// Przy błędzie zwraca nil: brak liczby, a nie fałszywe zero.
func (h *SystemHandler) countScans(ctx context.Context) *SystemScans {
	ctx, cancel := context.WithTimeout(ctx, systemProbeTimeout)
	defer cancel()

	var scans SystemScans
	for _, model := range []any{&models.Scan{}, &models.PremiumScan{}} {
		var rows []struct {
			Status string
			Count  int64
		}
		err := h.db.WithContext(ctx).Model(model).
			Select("status, count(*) AS count").
			Where("status IN ?", []string{models.ScanStatusPending, models.ScanStatusRunning}).
			Group("status").
			Scan(&rows).Error
		if err != nil {
			log.Printf("System: nie udało się policzyć kolejki skanów: %v", err)
			return nil
		}
		for _, row := range rows {
			switch row.Status {
			case models.ScanStatusPending:
				scans.Pending += row.Count
			case models.ScanStatusRunning:
				scans.Running += row.Count
			}
		}
	}
	return &scans
}
