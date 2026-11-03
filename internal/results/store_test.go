package results

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Discard,
	})
	if err != nil {
		t.Fatalf("otwarcie bazy testowej: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Scan{}, &models.PremiumScan{}, &models.ScanResult{}); err != nil {
		t.Fatalf("migracja bazy testowej: %v", err)
	}
	return db
}

func newID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return id
}

// body buduje wiadomość tak, jak publikuje ją daemon.
func body(scanID uuid.UUID, name, threat string, endFlag bool, resultType ResultType) []byte {
	return fmt.Appendf(nil,
		`{"target":"https://example.com","testId":%q,"endFlag":%t,"result":{"testResult":{"Name":%q,"Certainty":100,"ThreatLevel":%q,"Metadata":{"k":"v"},"Description":"opis"},"scanInfo":{"Message":"blocked","Code":403},"resultType":%d}}`,
		scanID, endFlag, name, threat, resultType,
	)
}

func decoded(t *testing.T, raw []byte) message {
	t.Helper()
	msg, err := decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return msg
}

func TestSaveBatchMovesFreeScanToRunningAndCompletes(t *testing.T) {
	db := testDB(t)
	store := NewStore(db, nil)
	scanID := newID(t)
	if err := db.Create(&models.Scan{ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusPending, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("zasianie skanu: %v", err)
	}

	err := store.SaveBatch(context.Background(), []message{
		decoded(t, body(scanID, "https", "High", false, Success)),
		decoded(t, body(scanID, "hsts", "None", false, Success)),
	})
	if err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	var scan models.Scan
	if err := db.Preload("Results").First(&scan, "id = ?", scanID).Error; err != nil {
		t.Fatalf("odczyt skanu: %v", err)
	}
	if scan.Status != models.ScanStatusRunning || scan.StartedAt == nil {
		t.Errorf("status = %s, started_at = %v, want RUNNING z ustawionym started_at", scan.Status, scan.StartedAt)
	}
	if len(scan.Results) != 2 {
		t.Fatalf("zapisano %d wyników, want 2", len(scan.Results))
	}
	byName := map[string]models.ScanResult{}
	for _, r := range scan.Results {
		byName[r.TestName] = r
	}
	if byName["https"].Passed || !byName["hsts"].Passed {
		t.Errorf("passed = %v/%v, want false dla High i true dla None", byName["https"].Passed, byName["hsts"].Passed)
	}
	if string(byName["https"].Metadata) != `{"k":"v"}` {
		t.Errorf("metadata = %s", byName["https"].Metadata)
	}

	// Wiadomość końcowa silnika ma resultType Message i pusty wynik.
	if err := store.SaveBatch(context.Background(), []message{decoded(t, body(scanID, "", "", true, Message))}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if err := db.Preload("Results").First(&scan, "id = ?", scanID).Error; err != nil {
		t.Fatalf("odczyt skanu: %v", err)
	}
	if scan.Status != models.ScanStatusCompleted || scan.CompletedAt == nil {
		t.Errorf("status = %s, want COMPLETED z completed_at", scan.Status)
	}
	if len(scan.Results) != 2 {
		t.Errorf("wiadomość końcowa zapisana jako wynik: %d wierszy", len(scan.Results))
	}
}

func TestSaveBatchRecordsEngineMessagesForPremiumScans(t *testing.T) {
	db := testDB(t)
	scanID := newID(t)
	if err := db.Create(&models.PremiumScan{ID: scanID, UserID: newID(t), TargetURL: "https://example.com", Status: models.ScanStatusPending, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("zasianie skanu: %v", err)
	}

	if err := NewStore(db, nil).SaveBatch(context.Background(), []message{decoded(t, body(scanID, "", "", false, Message))}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	var scan models.PremiumScan
	if err := db.Preload("Results").First(&scan, "id = ?", scanID).Error; err != nil {
		t.Fatalf("odczyt skanu: %v", err)
	}
	if scan.Status != models.ScanStatusRunning {
		t.Errorf("status = %s, want RUNNING", scan.Status)
	}
	if len(scan.Results) != 1 || scan.Results[0].TestName != engineMessageTestName {
		t.Fatalf("results = %+v, want jeden komunikat silnika", scan.Results)
	}
	if want := "blocked (Code: 403) - " + antiBotHint; scan.Results[0].Message != want {
		t.Errorf("message = %q, want %q", scan.Results[0].Message, want)
	}
	if !scan.Results[0].Notice {
		t.Error("komunikat silnika bez notice: statystyki policzyłyby go jako zdany test")
	}
}

func TestMarkLegacyNoticesFindsOnlyEngineMessages(t *testing.T) {
	db := testDB(t)
	scanID := uuid.New()
	legacy := models.ScanResult{ScanID: scanID, TestName: "HSTS", Severity: "Info", Passed: true, Message: "blocked (Code: 403) - " + antiBotHint}
	outcome := models.ScanResult{ScanID: scanID, TestName: "CSP", Severity: "None", Passed: true, Message: "Header present"}
	for _, row := range []*models.ScanResult{&legacy, &outcome} {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("zasianie: %v", err)
		}
	}

	if err := MarkLegacyNotices(db); err != nil {
		t.Fatalf("MarkLegacyNotices: %v", err)
	}
	var marked []string
	db.Model(&models.ScanResult{}).Where("notice = ?", true).Pluck("test_name", &marked)
	if len(marked) != 1 || marked[0] != "HSTS" {
		t.Errorf("oznaczone = %v, want tylko stary komunikat silnika", marked)
	}
}

func TestSaveBatchHandlesPendingScanFinishedInOneBatch(t *testing.T) {
	db := testDB(t)
	scanID := newID(t)
	if err := db.Create(&models.Scan{ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusPending, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("zasianie skanu: %v", err)
	}

	err := NewStore(db, nil).SaveBatch(context.Background(), []message{
		decoded(t, body(scanID, "https", "Info", false, Success)),
		decoded(t, body(scanID, "", "", true, Message)),
	})
	if err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	var scan models.Scan
	if err := db.First(&scan, "id = ?", scanID).Error; err != nil {
		t.Fatalf("odczyt skanu: %v", err)
	}
	if scan.Status != models.ScanStatusCompleted || scan.StartedAt == nil || scan.CompletedAt == nil {
		t.Errorf("status = %s, started_at = %v, completed_at = %v, want COMPLETED z oboma znacznikami", scan.Status, scan.StartedAt, scan.CompletedAt)
	}
}

func TestSaveBatchSkipsResultsAlreadyStored(t *testing.T) {
	db := testDB(t)
	store := NewStore(db, nil)
	scanID := newID(t)
	if err := db.Create(&models.Scan{ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusPending, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("zasianie skanu: %v", err)
	}

	// Ponowione zadanie: ten sam komplet w kolejnej paczce i dubel w tej samej.
	first := []message{
		decoded(t, body(scanID, "https", "High", false, Success)),
		decoded(t, body(scanID, "hsts", "None", false, Success)),
	}
	retry := []message{
		decoded(t, body(scanID, "https", "High", false, Success)),
		decoded(t, body(scanID, "hsts", "None", false, Success)),
		decoded(t, body(scanID, "hsts", "None", false, Success)),
	}
	for _, batch := range [][]message{first, retry} {
		if err := store.SaveBatch(context.Background(), batch); err != nil {
			t.Fatalf("SaveBatch: %v", err)
		}
	}

	var names []string
	if err := db.Model(&models.ScanResult{}).Where("scan_id = ?", scanID).Order("test_name").Pluck("test_name", &names).Error; err != nil {
		t.Fatalf("odczyt wyników: %v", err)
	}
	if len(names) != 2 || names[0] != "hsts" || names[1] != "https" {
		t.Errorf("wyniki = %v, want po jednym hsts i https", names)
	}
}

func TestDeleteDuplicateResultsPreparesUniqueIndex(t *testing.T) {
	db := testDB(t)
	// Tabela w stanie sprzed migracji: stary nieunikalny indeks i duble.
	if err := db.Migrator().DropIndex(&models.ScanResult{}, models.ScanResultsUniqueIndex); err != nil {
		t.Fatalf("zdjęcie indeksu: %v", err)
	}
	if err := db.Exec("CREATE INDEX " + legacyResultsIndex + " ON scan_results (scan_id, test_name)").Error; err != nil {
		t.Fatalf("stary indeks: %v", err)
	}
	scanID := newID(t)
	rows := []models.ScanResult{
		{ScanID: scanID, TestName: "https", Message: "pierwszy"},
		{ScanID: scanID, TestName: "https", Message: "dubel"},
		{ScanID: scanID, TestName: "hsts", Message: "jedyny"},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("zasianie: %v", err)
		}
	}

	if err := DeleteDuplicateResults(db); err != nil {
		t.Fatalf("DeleteDuplicateResults: %v", err)
	}
	if err := db.AutoMigrate(&models.ScanResult{}); err != nil {
		t.Fatalf("AutoMigrate po sprzątaniu: %v", err)
	}

	var kept []string
	db.Model(&models.ScanResult{}).Order("id").Pluck("message", &kept)
	if len(kept) != 2 || kept[0] != "pierwszy" || kept[1] != "jedyny" {
		t.Errorf("zostały = %v, want najstarszy https i hsts", kept)
	}
	m := db.Migrator()
	if !m.HasIndex(&models.ScanResult{}, models.ScanResultsUniqueIndex) || m.HasIndex(&models.ScanResult{}, legacyResultsIndex) {
		t.Error("po migracji powinien zostać tylko unikalny indeks")
	}
	// Drugie uruchomienie przy gotowym indeksie niczego nie rusza.
	if err := DeleteDuplicateResults(db); err != nil {
		t.Fatalf("DeleteDuplicateResults drugi raz: %v", err)
	}
}

func TestSaveBatchSkipsUnknownScans(t *testing.T) {
	db := testDB(t)
	known := newID(t)
	if err := db.Create(&models.Scan{ID: known, TargetURL: "https://example.com", Status: models.ScanStatusRunning, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("zasianie skanu: %v", err)
	}

	err := NewStore(db, nil).SaveBatch(context.Background(), []message{
		decoded(t, body(newID(t), "https", "High", false, Success)),
		decoded(t, body(known, "hsts", "High", false, Success)),
	})
	if err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	var rows []models.ScanResult
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("odczyt wyników: %v", err)
	}
	if len(rows) != 1 || rows[0].ScanID != known {
		t.Errorf("rows = %+v, want tylko wynik znanego skanu", rows)
	}
}

func TestSaveBatchDoesNotCompleteFailedScans(t *testing.T) {
	db := testDB(t)
	scanID := newID(t)
	if err := db.Create(&models.Scan{ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusFailed, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("zasianie skanu: %v", err)
	}

	if err := NewStore(db, nil).SaveBatch(context.Background(), []message{decoded(t, body(scanID, "", "", true, Message))}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	var scan models.Scan
	if err := db.First(&scan, "id = ?", scanID).Error; err != nil {
		t.Fatalf("odczyt skanu: %v", err)
	}
	if scan.Status != models.ScanStatusFailed {
		t.Errorf("status = %s, want FAILED", scan.Status)
	}
}

func TestToRowDefaultsMissingMetadata(t *testing.T) {
	msg := decoded(t, fmt.Appendf(nil, `{"testId":%q,"result":{"testResult":{"Name":"https","ThreatLevel":"Info","Metadata":null},"resultType":1}}`, newID(t)))
	if row := toRow(msg); string(row.Metadata) != `{}` {
		t.Errorf("metadata = %s, want {}", row.Metadata)
	}
}

func TestDecodeRejectsBadMessages(t *testing.T) {
	for name, raw := range map[string]string{
		"nie JSON":   `nie json`,
		"zły testId": `{"testId":"nie-uuid"}`,
		"brak ID":    `{"target":"https://example.com"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decode([]byte(raw)); err == nil {
				t.Error("decode przyjął niepoprawną wiadomość")
			}
		})
	}
}

func TestSaveBatchReportsChangedScansInsideTheTransaction(t *testing.T) {
	db := testDB(t)
	scanID := newID(t)
	if err := db.Create(&models.Scan{ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusPending, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("zasianie skanu: %v", err)
	}

	var reported []uuid.UUID
	var rowsSeen int64
	store := NewStore(db, func(tx *gorm.DB, ids []uuid.UUID) error {
		reported = ids
		// W tej samej transakcji: wiersze już tu są, choć jeszcze nie po commicie.
		return tx.Model(&models.ScanResult{}).Where("scan_id = ?", scanID).Count(&rowsSeen).Error
	})

	err := store.SaveBatch(context.Background(), []message{decoded(t, body(scanID, "https", "High", false, Success))})
	if err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if len(reported) == 0 || reported[0] != scanID {
		t.Errorf("zgłoszono %v, want %s", reported, scanID)
	}
	if rowsSeen != 1 {
		t.Errorf("w chwili zgłoszenia widać %d wierszy, want 1", rowsSeen)
	}
}

func TestSaveBatchRollsBackWhenReportingFails(t *testing.T) {
	db := testDB(t)
	scanID := newID(t)
	if err := db.Create(&models.Scan{ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusPending, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("zasianie skanu: %v", err)
	}

	store := NewStore(db, func(*gorm.DB, []uuid.UUID) error { return errors.New("sygnał nie poszedł") })
	if err := store.SaveBatch(context.Background(), []message{decoded(t, body(scanID, "https", "High", false, Success))}); err == nil {
		t.Fatal("SaveBatch powinien zwrócić błąd")
	}

	var rows int64
	db.Model(&models.ScanResult{}).Where("scan_id = ?", scanID).Count(&rows)
	if rows != 0 {
		t.Errorf("po cofnięciu zostało %d wierszy", rows)
	}
}

func TestMarkFailedReportsChangedScans(t *testing.T) {
	db := testDB(t)
	scanID := newID(t)
	if err := db.Create(&models.Scan{ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusRunning, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("zasianie skanu: %v", err)
	}

	var reported []uuid.UUID
	store := NewStore(db, func(_ *gorm.DB, ids []uuid.UUID) error {
		reported = ids
		return nil
	})
	if err := store.MarkFailed(context.Background(), []uuid.UUID{scanID}); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if len(reported) != 1 || reported[0] != scanID {
		t.Errorf("zgłoszono %v, want %s", reported, scanID)
	}
}
