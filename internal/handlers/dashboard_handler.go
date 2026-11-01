package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"github.com/prawo-i-piesc/backend/internal/stats"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DashboardHandler obsługuje dane wykresów i widgetów pulpitu oraz zapis
// preferencji konta, takich jak układ widgetów.
type DashboardHandler struct {
	db *gorm.DB
}

func NewDashboardHandler(db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{db: db}
}

const (
	statsDaysDefault = 90
	statsTestsLimit  = 10
	// Układ widgetów to kilkanaście krótkich wpisów; 16 KiB z zapasem.
	preferenceMaxBytes = 16 << 10
)

// preferenceKeys to jedyne klucze, które konto może zapisać.
var preferenceKeys = map[string]bool{
	"dashboard.widgets": true,
	"admin.widgets":     true,
}

type statsQuery struct {
	Days   int    `form:"days" binding:"omitempty,min=1,max=365"`
	Domain string `form:"domain" binding:"max=2048"`
	Limit  int    `form:"limit" binding:"omitempty,min=1,max=50"`
}

// bindStats czyta okno czasu, domenę i limit, z wartościami domyślnymi.
func bindStats(c *gin.Context) (statsQuery, bool) {
	var query statsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httpx.FailValidation(c, err)
		return query, false
	}
	if query.Days == 0 {
		query.Days = statsDaysDefault
	}
	if query.Limit == 0 {
		query.Limit = statsTestsLimit
	}
	query.Domain = strings.TrimSpace(query.Domain)
	return query, true
}

func (q statsQuery) filter(owner uuid.UUID) stats.Filter {
	return stats.Filter{
		Owner:  &owner,
		Since:  time.Now().AddDate(0, 0, -q.Days),
		Domain: q.Domain,
	}
}

func (h *DashboardHandler) HandleSummary(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	query, ok := bindStats(c)
	if !ok {
		return
	}

	statuses, err := stats.ScanStatuses(h.db, userID, query.Domain)
	if err != nil {
		failStats(c, "summary", err)
		return
	}
	points, err := stats.RiskTrend(h.db, query.filter(userID))
	if err != nil {
		failStats(c, "summary", err)
		return
	}

	domains := map[string]bool{}
	tests, failed, riskSum := 0, 0, 0.0
	for _, point := range points {
		domains[point.TargetURL] = true
		tests += point.Tests
		failed += point.Failed
		riskSum += point.ExactRisk
	}

	// Null zamiast zera, gdy w oknie nie ma żadnego skanu: zero ryzyka to
	// wynik, a brak skanów to brak wyniku.
	var average, latest *int
	if len(points) > 0 {
		avg := int(math.Round(riskSum / float64(len(points))))
		last := points[len(points)-1].Risk
		average, latest = &avg, &last
	}

	c.JSON(http.StatusOK, gin.H{
		"days":    query.Days,
		"scans":   statuses,
		"domains": len(domains),
		"tests":   gin.H{"run": tests, "failed": failed},
		"risk":    gin.H{"average": average, "latest": latest},
	})
}

func (h *DashboardHandler) HandleRiskTrend(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	query, ok := bindStats(c)
	if !ok {
		return
	}

	points, err := stats.RiskTrend(h.db, query.filter(userID))
	if err != nil {
		failStats(c, "risk", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"days": query.Days, "points": points})
}

func (h *DashboardHandler) HandleSeverity(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	query, ok := bindStats(c)
	if !ok {
		return
	}

	breakdown, err := stats.Severity(h.db, query.filter(userID))
	if err != nil {
		failStats(c, "severity", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"days": query.Days, "failed": breakdown.Failed, "passed": breakdown.Passed})
}

func (h *DashboardHandler) HandleTests(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	query, ok := bindStats(c)
	if !ok {
		return
	}

	tests, err := stats.TopFailedTests(h.db, query.filter(userID), query.Limit)
	if err != nil {
		failStats(c, "tests", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"days": query.Days, "tests": tests})
}

func (h *DashboardHandler) HandleDomains(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	// Bez okna czasu: lista do wyboru domeny ma pokazać wszystko, co konto skanowało.
	domains, err := stats.Domains(h.db, stats.Filter{Owner: &userID})
	if err != nil {
		failStats(c, "domains", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"domains": domains})
}

func failStats(c *gin.Context, what string, err error) {
	log.Printf("Stats %s: nie udało się policzyć: %v", what, err)
	httpx.Fail(c, httpx.CodeInternal)
}

// preferenceKey odrzuca klucz spoza listy; true, gdy można iść dalej.
func preferenceKey(c *gin.Context) (string, bool) {
	key := c.Param("key")
	if !preferenceKeys[key] {
		httpx.FailWithFields(c, httpx.CodeValidationFailed, map[string]string{"key": "unknown"})
		return "", false
	}
	return key, true
}

// HandleGetPreference zwraca zapisaną wartość albo null, gdy konto jej
// jeszcze nie zapisało: dla pulpitu to po prostu układ domyślny.
func (h *DashboardHandler) HandleGetPreference(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	key, ok := preferenceKey(c)
	if !ok {
		return
	}

	var preference models.UserPreference
	err := h.db.Where("user_id = ? AND key = ?", userID, key).First(&preference).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusOK, gin.H{"key": key, "value": nil})
		return
	}
	if err != nil {
		log.Printf("Preference: nie udało się odczytać %q konta %s: %v", key, userID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"key": key, "value": json.RawMessage(preference.Value)})
}

type preferenceRequest struct {
	Value json.RawMessage `json:"value" binding:"required"`
}

func (h *DashboardHandler) HandlePutPreference(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	key, ok := preferenceKey(c)
	if !ok {
		return
	}

	var req preferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}
	value, reason := normalizePreference(req.Value)
	if reason != "" {
		httpx.FailWithFields(c, httpx.CodeValidationFailed, map[string]string{"value": reason})
		return
	}

	preference := models.UserPreference{UserID: userID, Key: key, Value: value, UpdatedAt: time.Now()}
	err := h.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&preference).Error
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		// Konto usunięte, a token jeszcze ważny: jak przy każdym innym
		// odwołaniu do nieistniejącego konta, sesja się skończyła.
		httpx.Fail(c, httpx.CodeSessionExpired)
		return
	}
	if err != nil {
		log.Printf("Preference: nie udało się zapisać %q konta %s: %v", key, userID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"key": key, "value": json.RawMessage(value)})
}

// HandleDeletePreference usuwa zapisaną wartość, więc konto wraca do wartości
// domyślnej — także do domyślnego układu, który zmieni się w przyszłości,
// czego zapisanie obecnych domyślnych by nie dało. Brak wiersza to też 204.
func (h *DashboardHandler) HandleDeletePreference(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	key, ok := preferenceKey(c)
	if !ok {
		return
	}
	if err := h.db.Where("user_id = ? AND key = ?", userID, key).Delete(&models.UserPreference{}).Error; err != nil {
		log.Printf("Preference: nie udało się usunąć %q konta %s: %v", key, userID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	c.Status(http.StatusNoContent)
}

// normalizePreference sprawdza i przepisuje wartość preferencji tak, jak ją
// zapisze Postgres: JSONB odrzuca \u0000 i samotne surogaty, które Go
// przepuszcza, więc zamiast 500 z bazy jest 400. Każdy obsługiwany klucz to
// układ widgetów, czyli lista. Limit liczy się po przepisaniu, bo tyle trafi
// do bazy i z powrotem do przeglądarki.
func normalizePreference(raw json.RawMessage) ([]byte, string) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, "invalid"
	}
	if value == nil {
		return nil, "required"
	}
	if _, ok := value.([]any); !ok {
		return nil, "invalid"
	}
	if containsNUL(value) {
		return nil, "invalid"
	}
	// Unmarshal zamienił samotne surogaty na U+FFFD; Marshal zapisze poprawny UTF-8.
	normalized, err := json.Marshal(value)
	if err != nil {
		return nil, "invalid"
	}
	if len(normalized) > preferenceMaxBytes {
		return nil, "max:" + strconv.Itoa(preferenceMaxBytes)
	}
	return normalized, ""
}

func containsNUL(value any) bool {
	switch v := value.(type) {
	case string:
		return strings.ContainsRune(v, 0)
	case []any:
		for _, item := range v {
			if containsNUL(item) {
				return true
			}
		}
	case map[string]any:
		for key, item := range v {
			if strings.ContainsRune(key, 0) || containsNUL(item) {
				return true
			}
		}
	}
	return false
}
