package api

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/monitoring"
)

const probeTimeout = 2 * time.Second

// healthz to sonda liveness: odpowiada, dopóki proces obsługuje HTTP. Celowo
// nie dotyka bazy ani brokera — zerwane połączenie z Postgresem nie jest
// powodem, żeby kubelet restartował pod.
func healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// readyz to sonda readiness: 503, gdy którakolwiek zależność nie odpowiada,
// żeby Service przestał kierować ruch do poda, który i tak by go nie obsłużył.
func readyz(probes []monitoring.Probe) gin.HandlerFunc {
	return func(c *gin.Context) {
		status := http.StatusOK
		report := make(gin.H, len(probes))

		for _, probe := range probes {
			ctx, cancel := context.WithTimeout(c.Request.Context(), probeTimeout)
			err := probe.Check(ctx)
			cancel()

			if err != nil {
				log.Printf("readyz: %s nie odpowiada: %v", probe.Name, err)
				report[probe.Name] = "down"
				status = http.StatusServiceUnavailable
				continue
			}
			report[probe.Name] = "up"
		}

		c.JSON(status, report)
	}
}
