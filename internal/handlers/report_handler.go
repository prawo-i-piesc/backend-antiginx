package handlers

import (
	"bytes"
	"log"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"github.com/prawo-i-piesc/backend/internal/report"
	"gorm.io/gorm"
)

// reportCSP pozwala raportowi HTML na jego własne style, nic poza tym.
// Globalne nagłówki API mają default-src 'none', co wyłączyłoby style,
// gdyby ktoś otworzył adres raportu bezpośrednio w przeglądarce.
const reportCSP = "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

var (
	reportFormatsReason = oneOf(report.Formats)
	reportLangsReason   = oneOf(report.Langs)
)

func oneOf[T ~string](values []T) string {
	names := make([]string, len(values))
	for i, v := range values {
		names[i] = string(v)
	}
	return "oneof:" + strings.Join(names, " ")
}

// HandleFreeScanReport oddaje raport z darmowego skanu. Darmowe skany nie
// mają właściciela i są publiczne po UUID, więc raport też.
func (h *ScanHandler) HandleFreeScanReport(c *gin.Context) {
	scanID, ok := scanIDParam(c)
	if !ok {
		return
	}
	opts, ok := reportParams(c)
	if !ok {
		return
	}

	var scan models.Scan
	if err := h.db.Preload("Results", orderResults).First(&scan, "id = ?", scanID).Error; err != nil {
		failScanLookup(c, err)
		return
	}

	sendReport(c, report.FromScan(&scan), opts)
}

// HandlePremiumScanReport oddaje raport ze skanu konta wyłącznie jego
// właścicielowi. Cudzy skan dostaje 404 jak nieistniejący, żeby nie
// zdradzać, że taki UUID w ogóle jest.
func (h *ScanHandler) HandlePremiumScanReport(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	scanID, ok := scanIDParam(c)
	if !ok {
		return
	}
	opts, ok := reportParams(c)
	if !ok {
		return
	}

	var scan models.PremiumScan
	if err := h.db.Preload("Results", orderResults).First(&scan, "id = ? AND user_id = ?", scanID, userID).Error; err != nil {
		failScanLookup(c, err)
		return
	}

	sendReport(c, report.FromPremiumScan(&scan), opts)
}

func orderResults(db *gorm.DB) *gorm.DB {
	return db.Order("id")
}

type reportOptions struct {
	format report.Format
	lang   report.Lang
}

// reportParams czyta ?format= i ?lang=. Oba błędy zgłasza naraz, żeby klient
// nie poprawiał zapytania na raty.
func reportParams(c *gin.Context) (reportOptions, bool) {
	format, formatOK := report.ParseFormat(c.Query("format"))
	lang, langOK := report.ParseLang(c.Query("lang"))
	if formatOK && langOK {
		return reportOptions{format: format, lang: lang}, true
	}

	fields := map[string]string{}
	if !formatOK {
		fields["format"] = reportFormatsReason
	}
	if !langOK {
		fields["lang"] = reportLangsReason
	}
	httpx.FailWithFields(c, httpx.CodeValidationFailed, fields)
	return reportOptions{}, false
}

// sendReport renderuje raport do bufora i dopiero potem wysyła, żeby błąd
// renderowania skończył się zwykłą odpowiedzią 500, a nie uciętym plikiem.
func sendReport(c *gin.Context, src report.Source, opts reportOptions) {
	format := opts.format
	// Skan w toku dałby raport z częścią wyników, który wygląda na pełny.
	if src.Status == models.ScanStatusPending || src.Status == models.ScanStatusRunning {
		httpx.Fail(c, httpx.CodeScanNotFinished)
		return
	}

	doc := report.Build(src, time.Now())
	var buf bytes.Buffer
	if err := report.Render(&buf, doc, format, opts.lang); err != nil {
		log.Printf("Report %s: nie udało się wyrenderować %s: %v", src.ID, format, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": report.Filename(doc, format)}))
	c.Header("Cache-Control", "no-store")
	if format == report.FormatHTML {
		c.Header("Content-Security-Policy", reportCSP)
	}
	c.Data(http.StatusOK, format.ContentType(), buf.Bytes())
}
