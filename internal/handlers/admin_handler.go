package handlers

import (
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/billing"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

type AdminHandler struct {
	db       *gorm.DB
	sessions *auth.SessionService
	billing  *billing.Service
}

type DashboardScan struct {
	ID        string    `json:"id"`
	TargetURL string    `json:"target_url"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	Type      string    `json:"type"` // "free" lub "premium"
}

func NewAdminHandler(db *gorm.DB, sessions *auth.SessionService, billingService *billing.Service) *AdminHandler {
	return &AdminHandler{
		db:       db,
		sessions: sessions,
		billing:  billingService,
	}
}

// HandleGetDatabaseInfo zrzuca całą tabelę bez stronicowania. Zostaje tylko do
// czasu, aż panel admina przejdzie na /api/admin/users i /api/admin/scans.
//
// Deprecated: użyj GET /api/admin/users i GET /api/admin/scans.
func (h *AdminHandler) HandleGetDatabaseInfo(c *gin.Context) {
	c.Header("Deprecation", "true")

	var (
		rows any
		err  error
	)

	switch c.Query("table") {
	case "users":
		var users []models.User
		err = h.db.Find(&users).Error
		rows = users
	case "scans":
		var scans []models.Scan
		err = h.db.Preload("Results").Find(&scans).Error
		rows = scans
	case "premium_scans":
		var premiumScans []models.PremiumScan
		err = h.db.Preload("Results").Find(&premiumScans).Error
		rows = premiumScans
	default:
		httpx.FailWithFields(c, httpx.CodeValidationFailed, map[string]string{"table": "oneof:users scans premium_scans"})
		return
	}

	if err != nil {
		log.Printf("AdminDatabase: nie udało się pobrać tabeli %q: %v", c.Query("table"), err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (h *AdminHandler) HandleGetDashboardWidgets(c *gin.Context) {
	var totalUsers int64
	var totalScans int64
	var totalPremiumScans int64
	var detectedThreats int64

	h.db.Model(&models.User{}).Count(&totalUsers)

	h.db.Model(&models.Scan{}).Count(&totalScans)
	h.db.Model(&models.PremiumScan{}).Count(&totalPremiumScans)
	allTimeScans := totalScans + totalPremiumScans

	h.db.Model(&models.ScanResult{}).Where("passed = ?", false).Count(&detectedThreats)

	var recentFree []models.Scan
	h.db.Where("status != ?", models.ScanStatusPending).Order("created_at desc").Limit(4).Find(&recentFree)

	var recentPremium []models.PremiumScan
	h.db.Where("status != ?", models.ScanStatusPending).Order("created_at desc").Limit(4).Find(&recentPremium)

	var combinedScans []DashboardScan
	for _, s := range recentFree {
		combinedScans = append(combinedScans, DashboardScan{
			ID:        s.ID.String(),
			TargetURL: s.TargetURL,
			Status:    s.Status,
			CreatedAt: s.CreatedAt,
			Type:      "free",
		})
	}
	for _, s := range recentPremium {
		combinedScans = append(combinedScans, DashboardScan{
			ID:        s.ID.String(),
			TargetURL: s.TargetURL,
			Status:    s.Status,
			CreatedAt: s.CreatedAt,
			Type:      "premium",
		})
	}

	sort.Slice(combinedScans, func(i, j int) bool {
		return combinedScans[i].CreatedAt.After(combinedScans[j].CreatedAt)
	})

	if len(combinedScans) > 4 {
		combinedScans = combinedScans[:4]
	}

	c.JSON(http.StatusOK, gin.H{
		"total_users":      totalUsers,
		"all_time_scans":   allTimeScans,
		"detected_threats": detectedThreats,
		"recent_scans":     combinedScans,
	})
}
