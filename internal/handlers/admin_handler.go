package handlers

import (
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/billing"
	"github.com/prawo-i-piesc/backend/internal/models"
	"github.com/prawo-i-piesc/backend/internal/stats"
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

// newUsersDays to okno widgetu nowych rejestracji.
const newUsersDays = 7

// HandleTestStats zwraca najczęściej oblewane testy na całej platformie:
// w skanach darmowych i premium, bez względu na właściciela.
func (h *AdminHandler) HandleTestStats(c *gin.Context) {
	query, ok := bindStats(c)
	if !ok {
		return
	}

	tests, err := stats.TopFailedTests(h.db, stats.Filter{Since: time.Now().AddDate(0, 0, -query.Days)}, query.Limit)
	if err != nil {
		failStats(c, "admin tests", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"days": query.Days, "tests": tests})
}

func NewAdminHandler(db *gorm.DB, sessions *auth.SessionService, billingService *billing.Service) *AdminHandler {
	return &AdminHandler{
		db:       db,
		sessions: sessions,
		billing:  billingService,
	}
}

func (h *AdminHandler) HandleGetDashboardWidgets(c *gin.Context) {
	var totalUsers int64
	var newUsers int64
	var totalScans int64
	var totalPremiumScans int64
	var detectedThreats int64

	h.db.Model(&models.User{}).Count(&totalUsers)
	h.db.Model(&models.User{}).Where("created_at >= ?", time.Now().AddDate(0, 0, -newUsersDays)).Count(&newUsers)

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
		"new_users":        newUsers,
		"all_time_scans":   allTimeScans,
		"detected_threats": detectedThreats,
		"recent_scans":     combinedScans,
	})
}
