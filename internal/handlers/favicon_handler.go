package handlers

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/favicons"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

// faviconCSP wyłącza w ikonie wszystko poza stylami. Dotyczy SVG otwartego
// wprost pod tym adresem: bez sandbox jego skrypty działałyby w originie
// aplikacji. W <img> przeglądarka i tak niczego w SVG nie wykonuje.
const faviconCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// FaviconHandler serwuje ikony witryn po hashu.
type FaviconHandler struct {
	db *gorm.DB
}

func NewFaviconHandler(db *gorm.DB) *FaviconHandler {
	return &FaviconHandler{db: db}
}

// HandleGet zwraca ikonę o danym hashu. Pod jednym adresem jest zawsze ta sama
// treść, więc odpowiedź może leżeć w pamięci podręcznej rok, a przeglądarka,
// która ją ma, dostaje 304 bez zaglądania do bazy. Trasa jest publiczna, bo
// ikony pokazuje też darmowy skan bez konta.
func (h *FaviconHandler) HandleGet(c *gin.Context) {
	hash := c.Param("hash")
	if !favicons.ValidHash(hash) {
		httpx.Fail(c, httpx.CodeNotFound)
		return
	}

	etag := `"` + hash + `"`
	if etagMatches(c.GetHeader("If-None-Match"), etag) {
		faviconHeaders(c, etag)
		c.Status(http.StatusNotModified)
		return
	}

	var icon models.Favicon
	err := h.db.WithContext(c.Request.Context()).Select("content_type", "data").Where("hash = ?", hash).Take(&icon).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httpx.Fail(c, httpx.CodeNotFound)
		return
	}
	if err != nil {
		log.Printf("Favicon: nie udało się odczytać ikony %s: %v", hash, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	faviconHeaders(c, etag)
	c.Data(http.StatusOK, icon.ContentType, icon.Data)
}

func faviconHeaders(c *gin.Context, etag string) {
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("ETag", etag)
	c.Header("Content-Security-Policy", faviconCSP)
	c.Header("Cross-Origin-Resource-Policy", "same-origin")
}

func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == etag || candidate == "*" {
			return true
		}
	}
	return false
}
