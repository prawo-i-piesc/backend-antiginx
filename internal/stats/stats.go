// Package stats liczy dane do wykresów i widgetów pulpitu: trend ryzyka,
// rozkład severity, najczęściej oblewane testy i podsumowanie skanów.
//
// Wszystko liczy baza w zapytaniach agregujących, ograniczonych do skanów
// jednego konta (albo całej platformy dla admina) i do okna czasu, więc
// żaden widok nie ściąga wyników skanów, żeby policzyć je u siebie.
package stats

import (
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// riskWeight to waga nieudanego testu według severity. Ten sam model liczył
// do tej pory front (calculateThreatLevel w app/lib/api.ts); trzymamy go w
// jednym miejscu, żeby #133 zmieniło go tylko tutaj.
const riskWeight = `CASE upper(r.severity)
	WHEN 'CRITICAL' THEN 100 WHEN 'HIGH' THEN 75 WHEN 'MEDIUM' THEN 50 WHEN 'LOW' THEN 25
	ELSE 5 END`

// failedResult jest prawdziwe tylko dla istniejącego, nieudanego wyniku: przy
// LEFT JOIN skan bez wyników daje wiersz z r.* równym NULL.
const failedResult = `r.id IS NOT NULL AND NOT r.passed`

// Filter wybiera skany, z których liczymy. Owner nil oznacza całą platformę:
// skany darmowe i premium razem. Since zero oznacza bez ograniczenia czasu.
type Filter struct {
	Owner  *uuid.UUID
	Since  time.Time
	Domain string
}

// completedScans to podzapytanie z zakończonymi skanami pasującymi do filtra,
// z kolumnami id, target_url i created_at, pod aliasem s.
func completedScans(f Filter) (string, []any) {
	where := []string{"status = 'COMPLETED'"}
	var args []any
	if !f.Since.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, f.Since)
	}
	if f.Domain != "" {
		where = append(where, "target_url = ?")
		args = append(args, f.Domain)
	}
	cond := strings.Join(where, " AND ")

	if f.Owner != nil {
		return "(SELECT id, target_url, created_at FROM premium_scans WHERE user_id = ? AND " + cond + ") AS s",
			append([]any{*f.Owner}, args...)
	}
	return "(SELECT id, target_url, created_at FROM scans WHERE " + cond +
			" UNION ALL SELECT id, target_url, created_at FROM premium_scans WHERE " + cond + ") AS s",
		append(append([]any{}, args...), args...)
}

// TrendPoint to jeden zakończony skan na osi czasu.
type TrendPoint struct {
	ScanID    uuid.UUID `json:"scan_id"`
	TargetURL string    `json:"target_url"`
	CreatedAt time.Time `json:"created_at"`
	Tests     int       `json:"tests"`
	Failed    int       `json:"failed"`
	// Risk to średnia waga nieudanych testów, 0–100; 0, gdy wszystkie przeszły.
	Risk int `json:"risk"`
}

// RiskTrend zwraca zakończone skany od najstarszego, każdy z ryzykiem.
func RiskTrend(db *gorm.DB, f Filter) ([]TrendPoint, error) {
	source, args := completedScans(f)
	var rows []struct {
		ScanID    uuid.UUID
		TargetURL string
		CreatedAt time.Time
		Tests     int
		Failed    int
		Risk      float64
	}
	err := db.Raw(`SELECT s.id AS scan_id, s.target_url, s.created_at,
			COUNT(r.id) AS tests,
			COALESCE(SUM(CASE WHEN `+failedResult+` THEN 1 ELSE 0 END), 0) AS failed,
			COALESCE(AVG(CASE WHEN `+failedResult+` THEN `+riskWeight+` END), 0) AS risk
		FROM `+source+`
		LEFT JOIN scan_results r ON r.scan_id = s.id
		GROUP BY s.id, s.target_url, s.created_at
		ORDER BY s.created_at, s.id`, args...).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	points := make([]TrendPoint, 0, len(rows))
	for _, row := range rows {
		points = append(points, TrendPoint{
			ScanID:    row.ScanID,
			TargetURL: row.TargetURL,
			CreatedAt: row.CreatedAt,
			Tests:     row.Tests,
			Failed:    row.Failed,
			Risk:      int(math.Min(math.Round(row.Risk), 100)),
		})
	}
	return points, nil
}

// SeverityLevels to severity w kolejności od najgroźniejszej; NONE liczymy
// jako INFO, tak jak front.
var SeverityLevels = []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "INFO"}

// SeverityBreakdown to liczba nieudanych testów na severity i liczba udanych.
type SeverityBreakdown struct {
	Failed map[string]int `json:"failed"`
	Passed int            `json:"passed"`
}

func Severity(db *gorm.DB, f Filter) (SeverityBreakdown, error) {
	source, args := completedScans(f)
	var rows []struct {
		Severity string
		Passed   bool
		Count    int
	}
	err := db.Raw(`SELECT upper(r.severity) AS severity, r.passed AS passed, COUNT(*) AS count
		FROM `+source+`
		JOIN scan_results r ON r.scan_id = s.id
		GROUP BY upper(r.severity), r.passed`, args...).Scan(&rows).Error
	if err != nil {
		return SeverityBreakdown{}, err
	}

	breakdown := SeverityBreakdown{Failed: map[string]int{}}
	for _, level := range SeverityLevels {
		breakdown.Failed[level] = 0
	}
	for _, row := range rows {
		if row.Passed {
			breakdown.Passed += row.Count
			continue
		}
		level := row.Severity
		if _, known := breakdown.Failed[level]; !known {
			level = "INFO"
		}
		breakdown.Failed[level] += row.Count
	}
	return breakdown, nil
}

// TestFailures to jeden test z liczbą uruchomień i porażek.
type TestFailures struct {
	TestName string `json:"test_name"`
	Runs     int    `json:"runs"`
	Failures int    `json:"failures"`
}

// TopFailedTests zwraca testy, które choć raz nie przeszły, od najczęściej
// oblewanego.
func TopFailedTests(db *gorm.DB, f Filter, limit int) ([]TestFailures, error) {
	source, args := completedScans(f)
	tests := []TestFailures{}
	err := db.Raw(`SELECT r.test_name, COUNT(*) AS runs,
			SUM(CASE WHEN r.passed THEN 0 ELSE 1 END) AS failures
		FROM `+source+`
		JOIN scan_results r ON r.scan_id = s.id
		GROUP BY r.test_name
		HAVING SUM(CASE WHEN r.passed THEN 0 ELSE 1 END) > 0
		ORDER BY failures DESC, runs DESC, r.test_name
		LIMIT ?`, append(args, limit)...).Scan(&tests).Error
	return tests, err
}

// Domain to adres, który konto skanowało, z liczbą zakończonych skanów.
type Domain struct {
	TargetURL string `json:"target_url"`
	Scans     int    `json:"scans"`
}

// Domains zwraca skanowane adresy, od ostatnio skanowanego.
func Domains(db *gorm.DB, f Filter) ([]Domain, error) {
	source, args := completedScans(f)
	domains := []Domain{}
	err := db.Raw(`SELECT s.target_url, COUNT(*) AS scans
		FROM `+source+`
		GROUP BY s.target_url
		ORDER BY MAX(s.created_at) DESC, s.target_url`, args...).Scan(&domains).Error
	return domains, err
}

// StatusCounts to liczba skanów konta w każdym statusie, bez okna czasu.
type StatusCounts struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

func ScanStatuses(db *gorm.DB, owner uuid.UUID) (StatusCounts, error) {
	var rows []struct {
		Status string
		Count  int
	}
	if err := db.Raw(`SELECT status, COUNT(*) AS count FROM premium_scans WHERE user_id = ? GROUP BY status`, owner).
		Scan(&rows).Error; err != nil {
		return StatusCounts{}, err
	}

	var counts StatusCounts
	for _, row := range rows {
		counts.Total += row.Count
		switch row.Status {
		case "PENDING":
			counts.Pending += row.Count
		case "RUNNING":
			counts.Running += row.Count
		case "COMPLETED":
			counts.Completed += row.Count
		case "FAILED":
			counts.Failed += row.Count
		}
	}
	return counts, nil
}
