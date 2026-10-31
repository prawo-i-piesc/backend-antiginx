// Package report składa raport ze skanu i renderuje go do PDF, HTML,
// Markdown oraz SARIF.
//
// Raport powstaje w dwóch krokach: Build zamienia skan z bazy w Document,
// niezależny od formatu model z gotowym podsumowaniem i uporządkowanymi
// wynikami, a Render zapisuje ten sam Document w wybranym formacie. Dzięki
// temu wszystkie formaty pokazują dokładnie te same liczby.
package report

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
)

// Severity porządkuje poziomy zagrożenia silnika; większa wartość to
// poważniejszy wynik. Nazwy i kolejność są te same co ThreatLevel w silniku.
type Severity int

const (
	SeverityNone Severity = iota
	SeverityInfo
	SeverityLow
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

var severityNames = [...]string{"None", "Info", "Low", "Medium", "High", "Critical"}

func (s Severity) String() string {
	if s < SeverityNone || s > SeverityCritical {
		return severityNames[SeverityInfo]
	}
	return severityNames[s]
}

// ParseSeverity czyta severity zapisane w scan_results. Nieznana wartość
// staje się Info: raport ma ją pokazać, ale nie może jej zawyżyć.
func ParseSeverity(raw string) Severity {
	for i, name := range severityNames {
		if strings.EqualFold(strings.TrimSpace(raw), name) {
			return Severity(i)
		}
	}
	return SeverityInfo
}

// Rodzaje skanów, tak jak nazywa je API.
const (
	KindFree    = "free"
	KindPremium = "premium"
)

// maxDetailsBytes ogranicza metadane jednego wyniku w PDF, HTML i Markdown.
// Pełne metadane trafiają tylko do SARIF, gdzie czyta je maszyna.
const maxDetailsBytes = 8 << 10

// Source to skan w postaci wspólnej dla darmowych i premium.
type Source struct {
	ID          uuid.UUID
	Kind        string
	TargetURL   string
	Status      string
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	Results     []models.ScanResult
}

func FromScan(s *models.Scan) Source {
	return Source{
		ID: s.ID, Kind: KindFree, TargetURL: s.TargetURL, Status: s.Status,
		CreatedAt: s.CreatedAt, StartedAt: s.StartedAt, CompletedAt: s.CompletedAt,
		Results: s.Results,
	}
}

func FromPremiumScan(s *models.PremiumScan) Source {
	return Source{
		ID: s.ID, Kind: KindPremium, TargetURL: s.TargetURL, Status: s.Status,
		CreatedAt: s.CreatedAt, StartedAt: s.StartedAt, CompletedAt: s.CompletedAt,
		Results: s.Results,
	}
}

// Document to raport niezależny od formatu.
type Document struct {
	GeneratedAt time.Time
	Scan        ScanInfo
	Summary     Summary
	// Findings są posortowane: najpierw niezaliczone od najpoważniejszych,
	// potem zaliczone, w obrębie grupy po nazwie testu.
	Findings []Finding
}

type ScanInfo struct {
	ID          uuid.UUID
	Kind        string
	TargetURL   string
	Status      string
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
}

// Duration to czas od startu do końca skanu; zero, gdy któregoś brakuje.
func (s ScanInfo) Duration() time.Duration {
	if s.StartedAt == nil || s.CompletedAt == nil || s.CompletedAt.Before(*s.StartedAt) {
		return 0
	}
	return s.CompletedAt.Sub(*s.StartedAt)
}

type Summary struct {
	Total  int
	Passed int
	Failed int
	// Risk to najpoważniejsze severity wśród niezaliczonych testów albo None,
	// gdy wszystkie przeszły.
	Risk Severity
	// BySeverity liczy niezaliczone wyniki, od Critical do Low.
	BySeverity []SeverityCount
}

type SeverityCount struct {
	Severity Severity
	Count    int
}

type Finding struct {
	// RuleID to stabilny identyfikator testu wyprowadzony z jego nazwy.
	RuleID   string
	TestName string
	Severity Severity
	Passed   bool
	Message  string
	// Details to sformatowane metadane, puste dla `{}`, przycięte do
	// maxDetailsBytes. Metadata to oryginał dla SARIF.
	Details          string
	DetailsTruncated bool
	Metadata         json.RawMessage
}

// Build składa Document ze skanu. now jest parametrem, żeby raport dało się
// odtworzyć bajt w bajt w testach.
func Build(src Source, now time.Time) Document {
	doc := Document{
		GeneratedAt: now.UTC(),
		Scan: ScanInfo{
			ID: src.ID, Kind: src.Kind, TargetURL: src.TargetURL, Status: src.Status,
			CreatedAt: src.CreatedAt, StartedAt: src.StartedAt, CompletedAt: src.CompletedAt,
		},
	}

	// Kolejka wyników dostarcza co najmniej raz, więc ten sam wynik może
	// leżeć w bazie dwa razy; w raporcie liczymy go raz.
	seen := make(map[string]bool, len(src.Results))
	for _, r := range src.Results {
		f := newFinding(r)
		key := strings.Join([]string{f.TestName, f.Severity.String(), boolKey(f.Passed), f.Message, string(f.Metadata)}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		doc.Findings = append(doc.Findings, f)
	}

	slices.SortStableFunc(doc.Findings, func(a, b Finding) int {
		if a.Passed != b.Passed {
			if !a.Passed {
				return -1
			}
			return 1
		}
		if a.Severity != b.Severity {
			return int(b.Severity) - int(a.Severity)
		}
		return strings.Compare(a.TestName, b.TestName)
	})

	doc.Summary = summarize(doc.Findings)
	return doc
}

func newFinding(r models.ScanResult) Finding {
	f := Finding{
		RuleID:   RuleID(r.TestName),
		TestName: r.TestName,
		Severity: ParseSeverity(r.Severity),
		Passed:   r.Passed,
		Message:  strings.TrimSpace(r.Message),
	}
	if f.TestName == "" {
		f.TestName = "Unnamed test"
	}

	raw := bytes.TrimSpace(r.Metadata)
	if len(raw) == 0 || !json.Valid(raw) {
		return f
	}
	f.Metadata = json.RawMessage(raw)

	if isEmptyJSON(raw) {
		return f
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return f
	}
	f.Details, f.DetailsTruncated = truncate(pretty.String(), maxDetailsBytes)
	return f
}

func summarize(findings []Finding) Summary {
	s := Summary{Total: len(findings)}
	counts := make(map[Severity]int)
	for _, f := range findings {
		if f.Passed {
			s.Passed++
			continue
		}
		s.Failed++
		counts[f.Severity]++
		if f.Severity > s.Risk {
			s.Risk = f.Severity
		}
	}
	for sev := SeverityCritical; sev >= SeverityLow; sev-- {
		s.BySeverity = append(s.BySeverity, SeverityCount{Severity: sev, Count: counts[sev]})
	}
	return s
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// RuleID zamienia nazwę testu w identyfikator reguły, np. "HSTS Header
// Analysis" w "hsts-header-analysis". Silnik wysyła w wynikach nazwę, a nie ID
// testu, a SARIF potrzebuje identyfikatora, który nie zmienia się między skanami.
func RuleID(testName string) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(testName), "-"), "-")
	if slug == "" {
		return "unnamed-test"
	}
	return slug
}

func isEmptyJSON(raw []byte) bool {
	switch string(raw) {
	case "{}", "[]", "null", `""`:
		return true
	}
	return false
}

// truncate przycina s do limitu bajtów, nie rozcinając znaku UTF-8.
func truncate(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

func boolKey(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
