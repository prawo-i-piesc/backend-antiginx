package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	scanExchange   = "main_exchange"
	scanRoutingKey = "scan_key"
)

// Darmowy skan nie wymaga konta, więc jedyną rzeczą, która odróżnia
// użytkownika od skryptu robiącego z nas proxy do skanowania, jest adres.
const (
	freeScansPerIP = 10
	freeScanWindow = time.Hour
)

const maxTargetURLLength = 2048

type ScanHandler struct {
	amqpChannel *amqp.Channel
	db          *gorm.DB
	limiter     *auth.RateLimiter
}

func NewScanHandler(ch *amqp.Channel, db *gorm.DB) *ScanHandler {
	return &ScanHandler{
		amqpChannel: ch,
		db:          db,
		limiter:     auth.NewRateLimiter(),
	}
}

type CreateScanRequest struct {
	TargetURL string `json:"target_url" binding:"required,max=2048"`
}

type PremiumScanRequest struct {
	TargetURL        string   `json:"target_url" binding:"required,max=2048"`
	Tests            []string `json:"tests" binding:"required,min=1"`
	AntiBotDetection bool     `json:"anti_bot_detection"`
}

type CommandParameter struct {
	Name      string   `json:"Name"`
	Arguments []string `json:"Arguments"`
}

type ScanTaskPayload struct {
	Target     string             `json:"Target"`
	Parameters []CommandParameter `json:"Parameters"`
}

type EngineTestResult struct {
	Name        string      `json:"Name"`
	ThreatLevel string      `json:"ThreatLevel"`
	Metadata    interface{} `json:"Metadata"`
	Description string      `json:"Description"`
}

type ResultType int

const (
	Message ResultType = iota
	Success
)

type RequestInfo struct {
	Message string `json:"Message"`
	Code    int    `json:"Code"`
}

type AsyncResultRequest struct {
	Target      string           `json:"target"`
	TestID      string           `json:"testId"`
	Result      EngineTestResult `json:"result"`
	EndFlag     bool             `json:"endFlag"`
	ResultType  ResultType       `json:"resultType"`
	ProcessInfo RequestInfo      `json:"message"`
}

type UserDashboardScan struct {
	ID        string    `json:"id"`
	TargetURL string    `json:"target_url"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	Type      string    `json:"type"`
}

type TestCategoryGroup struct {
	CategoryName string   `json:"category"`
	Tests        []string `json:"tests"`
}

var CategorizedTests = []TestCategoryGroup{
	{
		CategoryName: "SSL/TLS & Encryption",
		Tests:        []string{"https", "hsts", "ssl-cert"},
	},
	{
		CategoryName: "Security Headers",
		Tests:        []string{"csp", "xframe", "permissions-policy", "x-content-type-options", "referrer-policy", "cross-origin-x"},
	},
	{
		CategoryName: "Privacy & Session Management",
		Tests:        []string{"cookie-sec"},
	},
	{
		CategoryName: "Reconnaissance & Server Information",
		Tests:        []string{"serv-h-a", "sitemap", "bot-protection"},
	},
	{
		CategoryName: "Vulnerabilities & Code Analysis",
		Tests:        []string{"js-obf", "phishing-url", "dns-reputation", "favicon-origin", "credential-form"},
	},
}

var AvailableTestsList = func() []string {
	var list []string
	for _, group := range CategorizedTests {
		list = append(list, group.Tests...)
	}
	return list
}()

var AllowedPremiumTests = func() map[string]bool {
	m := make(map[string]bool)
	for _, test := range AvailableTestsList {
		m[test] = true
	}
	return m
}()

func (h *ScanHandler) HandleHealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"message": "Running...",
	})
}

func (h *ScanHandler) HandleScanSubmission(c *gin.Context) {
	var req CreateScanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	target, ok := validTarget(c, req.TargetURL)
	if !ok {
		return
	}

	if allowed, retryAfter := h.limiter.Allow("freescan-ip:"+c.ClientIP(), freeScansPerIP, freeScanWindow); !allowed {
		httpx.FailRetryAfter(c, httpx.CodeRateLimited, retryAfter)
		return
	}

	newScanID, err := uuid.NewV7()
	if err != nil {
		log.Printf("FreeScan: nie udało się wygenerować UUIDv7: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	newScan := models.Scan{
		ID:        newScanID,
		TargetURL: target,
		Status:    models.ScanStatusPending,
		CreatedAt: time.Now(),
	}

	if err := h.db.Create(&newScan).Error; err != nil {
		log.Printf("FreeScan: nie udało się zapisać skanu: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	if !h.queueScan(c, &models.Scan{}, newScan.ID, newScan.TargetURL, AvailableTestsList, false) {
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"scanId": newScan.ID.String(),
		"status": newScan.Status,
	})
}

func (h *ScanHandler) HandlePremiumScanSubmission(c *gin.Context) {
	var req PremiumScanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	target, ok := validTarget(c, req.TargetURL)
	if !ok {
		return
	}

	var validTests []string
	for _, t := range req.Tests {
		if AllowedPremiumTests[t] {
			validTests = append(validTests, t)
		}
	}
	if len(validTests) == 0 {
		httpx.Fail(c, httpx.CodeNoValidTests)
		return
	}

	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	newScanID, err := uuid.NewV7()
	if err != nil {
		log.Printf("PremiumScan: nie udało się wygenerować UUIDv7: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	newScan := models.PremiumScan{
		ID:        newScanID,
		UserID:    userID,
		TargetURL: target,
		Status:    models.ScanStatusPending,
		CreatedAt: time.Now(),
	}

	if err := h.db.Create(&newScan).Error; err != nil {
		log.Printf("PremiumScan: nie udało się zapisać skanu: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	if !h.queueScan(c, &models.PremiumScan{}, newScan.ID, newScan.TargetURL, validTests, req.AntiBotDetection) {
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"scanId": newScan.ID.String(),
		"status": newScan.Status,
	})
}

// validTarget odrzuca target_url, którego nie wolno skanować, i zwraca adres
// w postaci przekazywanej silnikowi.
func validTarget(c *gin.Context, raw string) (string, bool) {
	target, reason := normalizeTarget(raw)
	if reason != "" {
		httpx.FailWithFields(c, httpx.CodeValidationFailed, map[string]string{"target_url": reason})
		return "", false
	}
	if len(target) > maxTargetURLLength {
		httpx.FailWithFields(c, httpx.CodeValidationFailed, map[string]string{"target_url": "max:2048"})
		return "", false
	}
	return target, true
}

// queueScan publikuje zadanie dla silnika. Gdy kolejka nie przyjmie
// wiadomości, skan dostaje FAILED, żeby nie wisiał wiecznie jako PENDING.
func (h *ScanHandler) queueScan(c *gin.Context, model any, scanID uuid.UUID, target string, tests []string, antiBot bool) bool {
	task := ScanTaskPayload{
		Target: target,
		Parameters: []CommandParameter{
			{Name: "--tests", Arguments: tests},
			{Name: "--taskId", Arguments: []string{scanID.String()}},
		},
	}
	if antiBot {
		task.Parameters = append(task.Parameters, CommandParameter{
			Name:      "--antiBotDetection",
			Arguments: []string{},
		})
	}

	if err := h.publishTask(c.Request.Context(), task); err != nil {
		log.Printf("Scan %s: nie udało się opublikować zadania: %v", scanID, err)
		if err := h.db.Model(model).Where("id = ?", scanID).Update("status", models.ScanStatusFailed).Error; err != nil {
			log.Printf("Scan %s: nie udało się oznaczyć jako FAILED: %v", scanID, err)
		}
		httpx.Fail(c, httpx.CodeQueueUnavailable)
		return false
	}
	return true
}

// publishTask wysyła zadanie przez main_exchange, tak jak oba rodzaje skanów:
// tylko ta ścieżka przechodzi przez topologię z retry_exchange i wait_queue.
func (h *ScanHandler) publishTask(ctx context.Context, task ScanTaskPayload) error {
	body, err := json.Marshal(task)
	if err != nil {
		return err
	}

	return h.amqpChannel.PublishWithContext(ctx,
		scanExchange,
		scanRoutingKey,
		false,
		false,
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			ContentType:  "application/json",
			Body:         body,
		})
}

// scanRef to wynik jednego zapytania rozstrzygającego, do której tabeli
// należy skan i w jakim jest stanie.
type scanRef struct {
	Kind   string
	Status string
}

func (r scanRef) model() any {
	if r.Kind == "premium" {
		return &models.PremiumScan{}
	}
	return &models.Scan{}
}

func findScan(db *gorm.DB, id uuid.UUID) (*scanRef, error) {
	var refs []scanRef
	err := db.Raw(
		`SELECT 'free' AS kind, status FROM scans WHERE id = ?
		 UNION ALL
		 SELECT 'premium' AS kind, status FROM premium_scans WHERE id = ?`,
		id, id,
	).Scan(&refs).Error
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, nil
	}
	return &refs[0], nil
}

func (h *ScanHandler) HandleResultSubmission(c *gin.Context) {
	var req AsyncResultRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	if gin.IsDebugging() {
		log.Printf("results: payload %+v", req)
	}

	scanID, err := uuid.Parse(req.TestID)
	if err != nil {
		httpx.FailWithFields(c, httpx.CodeValidationFailed, map[string]string{"testId": "invalid"})
		return
	}

	scan, err := findScan(h.db, scanID)
	if err != nil {
		log.Printf("results: nie udało się odczytać skanu %s: %v", scanID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	if scan == nil {
		httpx.Fail(c, httpx.CodeScanNotFound)
		return
	}

	switch {
	case !req.EndFlag && req.ResultType == Message:
		testName := req.Result.Name
		if testName == "" {
			testName = "Engine/Execution Error"
		}

		result := models.ScanResult{
			ScanID:   scanID,
			TestName: testName,
			Severity: "Info",
			Passed:   true,
			Message:  fmt.Sprintf("%s (Code: %d) - %s", req.ProcessInfo.Message, req.ProcessInfo.Code, "Perhaps you can use antibot detection to prevent this from happening."),
			Metadata: datatypes.JSON([]byte(`{}`)),
		}

		if err := h.saveResult(scan, scanID, &result); err != nil {
			log.Printf("results: nie udało się zapisać komunikatu silnika dla %s: %v", scanID, err)
			httpx.Fail(c, httpx.CodeInternal)
			return
		}

		log.Printf("results: test przerwany albo zablokowany dla %s: %s", scanID, req.ProcessInfo.Message)
		c.JSON(http.StatusOK, gin.H{"message": "Crash result logged successfully"})

	case req.Result.Name == "":
		now := time.Now()
		err := h.db.Model(scan.model()).
			Where("id = ?", scanID).
			Updates(map[string]interface{}{
				"status":       models.ScanStatusCompleted,
				"completed_at": &now,
			}).Error
		if err != nil {
			log.Printf("results: nie udało się domknąć skanu %s: %v", scanID, err)
			httpx.Fail(c, httpx.CodeInternal)
			return
		}

		log.Printf("results: skan %s zakończony (%s)", scanID, scan.Kind)
		c.JSON(http.StatusOK, gin.H{"message": "Scan completed"})

	default:
		metaJSON, err := json.Marshal(req.Result.Metadata)
		if err != nil {
			metaJSON = []byte(`{}`)
		}

		result := models.ScanResult{
			ScanID:   scanID,
			TestName: req.Result.Name,
			Severity: req.Result.ThreatLevel,
			Passed:   req.Result.ThreatLevel == "None" || req.Result.ThreatLevel == "Info",
			Message:  req.Result.Description,
			Metadata: datatypes.JSON(metaJSON),
		}

		if err := h.saveResult(scan, scanID, &result); err != nil {
			log.Printf("results: nie udało się zapisać wyniku dla %s: %v", scanID, err)
			httpx.Fail(c, httpx.CodeInternal)
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Result received"})
	}
}

// saveResult zapisuje wynik i przy pierwszym z nich przestawia skan na
// RUNNING. Skan, który już ruszył, kosztuje jeden INSERT bez transakcji.
func (h *ScanHandler) saveResult(scan *scanRef, scanID uuid.UUID, result *models.ScanResult) error {
	if scan.Status != models.ScanStatusPending {
		return h.db.Create(result).Error
	}

	return h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(result).Error; err != nil {
			return err
		}

		now := time.Now()
		return tx.Model(scan.model()).
			Where("id = ? AND status = ?", scanID, models.ScanStatusPending).
			Updates(map[string]interface{}{
				"status":     models.ScanStatusRunning,
				"started_at": &now,
			}).Error
	})
}

func (h *ScanHandler) HandleGetScan(c *gin.Context) {
	scanID, ok := scanIDParam(c)
	if !ok {
		return
	}

	var scan models.Scan
	if err := h.db.Preload("Results").First(&scan, "id = ?", scanID).Error; err != nil {
		failScanLookup(c, err)
		return
	}

	c.JSON(http.StatusOK, scan)
}

func (h *ScanHandler) HandlePremiumGetScan(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	scanID, ok := scanIDParam(c)
	if !ok {
		return
	}

	var scan models.PremiumScan
	if err := h.db.Preload("Results").First(&scan, "id = ? AND user_id = ?", scanID, userID).Error; err != nil {
		failScanLookup(c, err)
		return
	}

	c.JSON(http.StatusOK, scan)
}

func scanIDParam(c *gin.Context) (uuid.UUID, bool) {
	scanID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.FailWithFields(c, httpx.CodeValidationFailed, map[string]string{"id": "invalid"})
		return uuid.Nil, false
	}
	return scanID, true
}

func failScanLookup(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httpx.Fail(c, httpx.CodeScanNotFound)
		return
	}
	log.Printf("Scan: nie udało się pobrać skanu: %v", err)
	httpx.Fail(c, httpx.CodeInternal)
}

func (h *ScanHandler) HandleUserScans(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	var scans []models.PremiumScan
	if err := h.db.Preload("Results").Where("user_id = ?", userID).Find(&scans).Error; err != nil {
		log.Printf("UserScans: nie udało się pobrać skanów: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.JSON(http.StatusOK, scans)
}

func (h *ScanHandler) HandleUserDashboardWidgets(c *gin.Context) {
	userID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	var totalScans int64
	var detectedThreats int64
	var safeSites int64
	var recentScans []models.PremiumScan

	h.db.Model(&models.PremiumScan{}).Where("user_id = ?", userID).Count(&totalScans)

	h.db.Model(&models.ScanResult{}).
		Joins("JOIN premium_scans ON premium_scans.id = scan_results.scan_id").
		Where("premium_scans.user_id = ? AND scan_results.passed = ?", userID, false).
		Count(&detectedThreats)

	subQueryMaxTime := h.db.Model(&models.PremiumScan{}).
		Select("MAX(created_at)").
		Where("target_url = premium_scans.target_url AND user_id = premium_scans.user_id AND status = ?", models.ScanStatusCompleted)

	subQueryThreats := h.db.Model(&models.ScanResult{}).
		Select("1").
		Where("scan_id = premium_scans.id AND passed = ?", false)

	h.db.Model(&models.PremiumScan{}).
		Where("user_id = ? AND status = ?", userID, models.ScanStatusCompleted).
		Where("created_at = (?)", subQueryMaxTime).
		Where("NOT EXISTS (?)", subQueryThreats).
		Distinct("target_url").
		Count(&safeSites)

	result := h.db.Where("user_id = ? AND status != ?", userID, models.ScanStatusPending).
		Order("created_at desc").
		Limit(4).
		Find(&recentScans)

	if result.Error != nil {
		log.Printf("UserWidgets: nie udało się pobrać najnowszych skanów: %v", result.Error)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	mappedRecentScans := make([]UserDashboardScan, 0)
	for _, s := range recentScans {
		mappedRecentScans = append(mappedRecentScans, UserDashboardScan{
			ID:        s.ID.String(),
			TargetURL: s.TargetURL,
			Status:    s.Status,
			CreatedAt: s.CreatedAt,
			Type:      "premium",
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"total_scans":      totalScans,
		"detected_threats": detectedThreats,
		"safe_sites":       safeSites,
		"recent_scans":     mappedRecentScans,
	})
}
