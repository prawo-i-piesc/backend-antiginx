package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

type AdminHandler struct {
	db *gorm.DB
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
