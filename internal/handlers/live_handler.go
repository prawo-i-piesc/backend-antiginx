package handlers

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/live"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

// Nowe połączenia z jednego adresu. Klient wraca po zerwaniu z narastającą
// przerwą, więc limit dotyka tylko kogoś, kto łączy się w pętli.
const (
	liveHandshakesPerIP = 120
	liveHandshakeWindow = time.Minute
)

// LiveHandler otwiera podgląd skanu na żywo.
type LiveHandler struct {
	db      *gorm.DB
	hub     *live.Hub
	limiter *auth.RateLimiter
}

func NewLiveHandler(db *gorm.DB, hub *live.Hub) *LiveHandler {
	return &LiveHandler{db: db, hub: hub, limiter: auth.NewRateLimiter()}
}

// HandleFreeScanLive to podgląd darmowego skanu: dostęp daje znajomość ID, tak
// samo jak w GET /api/freescans/:id.
func (h *LiveHandler) HandleFreeScanLive(c *gin.Context) {
	scanID, ok := scanIDParam(c)
	if !ok {
		return
	}
	h.serve(c, "ip:"+c.ClientIP(), live.Stream{
		Hub:    h.hub,
		ScanID: scanID,
		Source: liveSource{db: h.db},
	})
}

// HandlePremiumScanLive to podgląd skanu z konta: tylko właściciel, ten sam 404
// dla cudzego skanu i dla takiego, którego nie ma.
func (h *LiveHandler) HandlePremiumScanLive(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	scanID, ok := scanIDParam(c)
	if !ok {
		return
	}
	// Bez końca ważności połączenie przeżyłoby token, więc to nie jest wartość opcjonalna.
	expires := c.GetTime(auth.TokenExpiryKey)
	if expires.IsZero() {
		log.Printf("Live: brak końca ważności tokenu w kontekście — trasa poza RequireAccessCookie")
		httpx.Fail(c, httpx.CodeSessionExpired)
		return
	}
	h.serve(c, "user:"+userID.String(), live.Stream{
		Hub:            h.hub,
		ScanID:         scanID,
		Source:         liveSource{db: h.db, owner: &userID},
		SessionExpires: expires,
	})
}

// serve odrzuca, co się da, zwykłą odpowiedzią HTTP, zanim połączenie stanie się
// WebSocketem. Przeglądarka nie pokaże skryptowi jej treści, ale front i tak
// wraca wtedy do odpytywania REST, które poda właściwy błąd.
func (h *LiveHandler) serve(c *gin.Context, client string, stream live.Stream) {
	if allowed, retryAfter := h.limiter.Allow("live-ip:"+c.ClientIP(), liveHandshakesPerIP, liveHandshakeWindow); !allowed {
		httpx.FailRetryAfter(c, httpx.CodeRateLimited, retryAfter)
		return
	}

	if _, err := stream.Source.Scan(c.Request.Context(), stream.ScanID); err != nil {
		if errors.Is(err, live.ErrScanGone) {
			httpx.Fail(c, httpx.CodeScanNotFound)
			return
		}
		log.Printf("Live: nie udało się odczytać skanu %s: %v", stream.ScanID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	release, ok := h.hub.Acquire(client)
	if !ok {
		httpx.Fail(c, httpx.CodeRateLimited)
		return
	}
	defer release()

	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		// Origin sprawdza już RequireOrigin na grupie tras. Accept porównałby go
		// z Host, a ten za proxy nie musi być adresem, który widzi przeglądarka.
		InsecureSkipVerify: true,
	})
	if err != nil {
		// Accept sam wysłał odpowiedź z błędem, np. gdy to nie był handshake.
		log.Printf("Live: nie udało się otworzyć połączenia ze skanem %s: %v", stream.ScanID, err)
		c.Abort()
		return
	}
	defer func() { _ = conn.CloseNow() }()

	stream.Serve(c.Request.Context(), conn)
}

// liveSource czyta skany darmowe albo, z ustawionym owner, skany tego konta.
type liveSource struct {
	db    *gorm.DB
	owner *uuid.UUID
}

func (s liveSource) Scan(ctx context.Context, id uuid.UUID) (live.Scan, error) {
	db := s.db.WithContext(ctx)
	var query *gorm.DB
	if s.owner == nil {
		query = db.Model(&models.Scan{}).Where("id = ?", id)
	} else {
		query = db.Model(&models.PremiumScan{}).Where("id = ? AND user_id = ?", id, *s.owner)
	}

	var scan live.Scan
	result := query.Select("id", "target_url", "created_at", "tests", "status", "started_at", "completed_at").Limit(1).Scan(&scan)
	if result.Error != nil {
		return live.Scan{}, result.Error
	}
	if result.RowsAffected == 0 {
		return live.Scan{}, live.ErrScanGone
	}
	return scan, nil
}

func (s liveSource) Results(ctx context.Context, id uuid.UUID, except []uint) ([]models.ScanResult, error) {
	query := s.db.WithContext(ctx).Where("scan_id = ?", id)
	if len(except) > 0 {
		query = query.Where("id NOT IN ?", except)
	}
	var results []models.ScanResult
	if err := query.Order("id").Find(&results).Error; err != nil {
		return nil, err
	}
	return results, nil
}
