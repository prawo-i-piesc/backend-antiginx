package results

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	engineMessageTestName = "Engine/Execution Error"
	antiBotHint           = "Perhaps you can use antibot detection to prevent this from happening."
	insertBatchSize       = 100
)

// Store zapisuje paczkę wiadomości jedną transakcją: wiersze wyników i
// przejścia statusów skanów albo wchodzą razem, albo wcale.
type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

func (s *Store) SaveBatch(ctx context.Context, batch []message) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		known, err := knownScans(tx, batch)
		if err != nil {
			return err
		}

		var rows []models.ScanResult
		var touched, finished []uuid.UUID
		for _, msg := range batch {
			if !known[msg.scanID] {
				log.Printf("results: wynik dla nieznanego skanu %s, pomijam", msg.scanID)
				continue
			}
			touched = append(touched, msg.scanID)
			// Wiadomość końcowa silnika ma resultType Message, więc endFlag
			// musi być sprawdzony przed typem, inaczej trafiłaby do wyników
			// jako błąd wykonania.
			if msg.EndFlag {
				finished = append(finished, msg.scanID)
				continue
			}
			rows = append(rows, toRow(msg))
		}

		if len(rows) > 0 {
			if err := tx.CreateInBatches(rows, insertBatchSize).Error; err != nil {
				return err
			}
		}

		now := time.Now()
		for _, model := range []any{&models.Scan{}, &models.PremiumScan{}} {
			if len(touched) > 0 {
				err := tx.Model(model).
					Where("id IN ? AND status = ?", touched, models.ScanStatusPending).
					Updates(map[string]any{"status": models.ScanStatusRunning, "started_at": &now}).Error
				if err != nil {
					return err
				}
			}
			if len(finished) > 0 {
				err := tx.Model(model).
					Where("id IN ? AND status = ?", finished, models.ScanStatusRunning).
					Updates(map[string]any{"status": models.ScanStatusCompleted, "completed_at": &now}).Error
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// knownScans zwraca ID skanów z paczki, które istnieją w scans albo
// premium_scans. Wynik dla skanu usuniętego w międzyczasie nie ma gdzie trafić.
func knownScans(tx *gorm.DB, batch []message) (map[uuid.UUID]bool, error) {
	ids := make([]uuid.UUID, 0, len(batch))
	for _, msg := range batch {
		ids = append(ids, msg.scanID)
	}

	known := make(map[uuid.UUID]bool, len(ids))
	for _, model := range []any{&models.Scan{}, &models.PremiumScan{}} {
		var found []uuid.UUID
		if err := tx.Model(model).Where("id IN ?", ids).Pluck("id", &found).Error; err != nil {
			return nil, err
		}
		for _, id := range found {
			known[id] = true
		}
	}
	return known, nil
}

func toRow(msg message) models.ScanResult {
	test := msg.Result.TestResult

	if msg.Result.ResultType == Message {
		name := test.Name
		if name == "" {
			name = engineMessageTestName
		}
		info := msg.Result.ScanInfo
		return models.ScanResult{
			ScanID:   msg.scanID,
			TestName: name,
			Severity: "Info",
			Passed:   true,
			Notice:   true,
			Message:  fmt.Sprintf("%s (Code: %d) - %s", info.Message, info.Code, antiBotHint),
			Metadata: datatypes.JSON(`{}`),
		}
	}

	metadata := datatypes.JSON(test.Metadata)
	if len(metadata) == 0 || string(metadata) == "null" {
		metadata = datatypes.JSON(`{}`)
	}
	return models.ScanResult{
		ScanID:   msg.scanID,
		TestName: test.Name,
		Severity: test.ThreatLevel,
		Passed:   test.ThreatLevel == "None" || test.ThreatLevel == "Info",
		Message:  test.Description,
		Metadata: metadata,
	}
}

// MarkLegacyNotices oznacza komunikaty silnika zapisane, zanim wyniki miały
// kolumnę notice. Rozpoznaje je po podpowiedzi, którą toRow dokleja do każdego
// takiego komunikatu i do żadnego wyniku testu. Wywoływane raz, zaraz po
// dodaniu kolumny.
func MarkLegacyNotices(db *gorm.DB) error {
	return db.Model(&models.ScanResult{}).
		Where("notice = ? AND message LIKE ?", false, "% - "+antiBotHint).
		Update("notice", true).Error
}
