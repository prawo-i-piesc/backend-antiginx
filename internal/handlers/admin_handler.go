package handlers

import (
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

type AdminHandler struct {
	db *gorm.DB
}

type DashboardScan struct {
	ID        string    `json:"id"`
	TargetURL string    `json:"target_url"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	Type      string    `json:"type"` // "free" lub "premium"
}

func NewAdminHandler(db *gorm.DB) *AdminHandler {
	return &AdminHandler{
		db: db,
	}
}

func (h *AdminHandler) HandleGetDatabaseInfo(c *gin.Context) {
	table := c.Query("table")

	switch table {
	case "users":
		var users []models.User
		if err := h.db.Find(&users).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Błąd podczas pobierania użytkowników z bazy danych"})
			return
		}
		c.JSON(http.StatusOK, users)

	case "scans":
		var scans []models.Scan
		if err := h.db.Preload("Results").Find(&scans).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Błąd podczas pobierania darmowych skanów"})
			return
		}
		c.JSON(http.StatusOK, scans)

	case "premium_scans":
		var premiumScans []models.PremiumScan
		if err := h.db.Preload("Results").Find(&premiumScans).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Błąd podczas pobierania skanów premium"})
			return
		}
		c.JSON(http.StatusOK, premiumScans)

	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Nie podano prawidłowej nazwy tabeli. Dostępne opcje to: users, scans, premium_scans",
		})
	}
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
	h.db.Where("status != ?", "PENDING").Order("created_at desc").Limit(4).Find(&recentFree)

	var recentPremium []models.PremiumScan
	h.db.Where("status != ?", "PENDING").Order("created_at desc").Limit(4).Find(&recentPremium)

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
