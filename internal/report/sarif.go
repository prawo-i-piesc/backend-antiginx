package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"

	"github.com/prawo-i-piesc/backend/internal/models"
)

// Wersja i schemat SARIF, w których zapisujemy wyniki (OASIS SARIF 2.1.0).
const (
	sarifVersion = "2.1.0"
	sarifSchema  = "https://json.schemastore.org/sarif-2.1.0.json"

	toolName = "AntiGinx"
	toolURI  = "https://github.com/prawo-i-piesc/engine-antiginx"

	// fingerprintKey nazywa nasz odcisk wyniku; wersja w nazwie pozwala
	// kiedyś zmienić sposób liczenia bez mieszania starych odcisków z nowymi.
	fingerprintKey = "antiginx/v1"
)

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool              sarifTool              `json:"tool"`
	AutomationDetails sarifAutomationDetails `json:"automationDetails"`
	Invocations       []sarifInvocation      `json:"invocations"`
	Results           []sarifResult          `json:"results"`
	Properties        sarifRunProperties     `json:"properties"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID                   string             `json:"id"`
	Name                 string             `json:"name"`
	ShortDescription     sarifMessage       `json:"shortDescription"`
	DefaultConfiguration sarifConfiguration `json:"defaultConfiguration"`
	Properties           sarifRuleProps     `json:"properties"`
}

type sarifConfiguration struct {
	Level string `json:"level"`
}

type sarifRuleProps struct {
	Tags []string `json:"tags"`
	// SecuritySeverity to wynik w skali CVSS, po którym GitHub code scanning
	// przypisuje alertowi poziom; podany jako string, tak jak tego oczekuje.
	SecuritySeverity string `json:"security-severity,omitempty"`
}

// sarifAutomationDetails nie ma pola guid: schemat SARIF dopuszcza w nim
// tylko UUID w wersjach 1–5, a skany mają UUIDv7. ID skanu niesie id.
type sarifAutomationDetails struct {
	ID string `json:"id"`
}

type sarifInvocation struct {
	ExecutionSuccessful bool   `json:"executionSuccessful"`
	StartTimeUTC        string `json:"startTimeUtc,omitempty"`
	EndTimeUTC          string `json:"endTimeUtc,omitempty"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	RuleIndex           int               `json:"ruleIndex"`
	Kind                string            `json:"kind"`
	Level               string            `json:"level"`
	Message             sarifMessage      `json:"message"`
	Locations           []sarifLocation   `json:"locations"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Properties          sarifResultProps  `json:"properties"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifResultProps struct {
	Severity string          `json:"severity"`
	Passed   bool            `json:"passed"`
	TestName string          `json:"testName"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type sarifRunProperties struct {
	ScanID      string       `json:"scanId"`
	ScanType    string       `json:"scanType"`
	Target      string       `json:"target"`
	Status      string       `json:"status"`
	SubmittedAt string       `json:"submittedAt"`
	GeneratedAt string       `json:"generatedAt"`
	Summary     sarifSummary `json:"summary"`
}

type sarifSummary struct {
	Total      int            `json:"total"`
	Passed     int            `json:"passed"`
	Failed     int            `json:"failed"`
	Risk       string         `json:"risk"`
	BySeverity map[string]int `json:"failedBySeverity"`
}

// RenderSARIF zapisuje raport jako log SARIF 2.1.0 z jednym przebiegiem.
//
// Każdy test to reguła, a każdy wynik to result: zaliczony ma kind "pass" i
// level "none", niezaliczony kind "fail" z poziomem wyprowadzonym z severity.
// Lokalizacją wyniku jest adres skanowanej strony, bo skan nie dotyczy plików.
func RenderSARIF(w io.Writer, doc Document) error {
	run := sarifRun{
		Tool: sarifTool{Driver: sarifDriver{
			Name:           toolName,
			InformationURI: toolURI,
			Rules:          []sarifRule{},
		}},
		AutomationDetails: sarifAutomationDetails{
			ID: "antiginx/" + doc.Scan.Kind + "/" + doc.Scan.ID.String(),
		},
		Invocations: []sarifInvocation{{
			ExecutionSuccessful: doc.Scan.Status == models.ScanStatusCompleted,
			StartTimeUTC:        sarifTimePtr(doc.Scan.StartedAt),
			EndTimeUTC:          sarifTimePtr(doc.Scan.CompletedAt),
		}},
		Results:    make([]sarifResult, 0, len(doc.Findings)),
		Properties: runProperties(doc),
	}

	ruleIndex := make(map[string]int)
	for _, f := range doc.Findings {
		idx, ok := ruleIndex[f.RuleID]
		if !ok {
			idx = len(run.Tool.Driver.Rules)
			ruleIndex[f.RuleID] = idx
			run.Tool.Driver.Rules = append(run.Tool.Driver.Rules, sarifRule{
				ID:                   f.RuleID,
				Name:                 f.TestName,
				ShortDescription:     sarifMessage{Text: f.TestName},
				DefaultConfiguration: sarifConfiguration{Level: "none"},
				Properties:           sarifRuleProps{Tags: []string{"security"}},
			})
		}
		// Findings są posortowane od najpoważniejszych, więc pierwszy
		// niezaliczony wynik reguły wyznacza jej domyślny poziom.
		rule := &run.Tool.Driver.Rules[idx]
		if !f.Passed && rule.Properties.SecuritySeverity == "" {
			rule.DefaultConfiguration.Level = sarifLevel(f.Severity)
			rule.Properties.SecuritySeverity = securitySeverity(f.Severity)
		}

		run.Results = append(run.Results, sarifResult{
			RuleID:    f.RuleID,
			RuleIndex: idx,
			Kind:      sarifKind(f),
			Level:     resultLevel(f),
			Message:   sarifMessage{Text: messageText(f)},
			Locations: []sarifLocation{{PhysicalLocation: sarifPhysicalLocation{
				ArtifactLocation: sarifArtifactLocation{URI: doc.Scan.TargetURL},
			}}},
			PartialFingerprints: map[string]string{fingerprintKey: fingerprint(doc.Scan.TargetURL, f.RuleID)},
			Properties: sarifResultProps{
				Severity: f.Severity.String(),
				Passed:   f.Passed,
				TestName: f.TestName,
				Metadata: f.Metadata,
			},
		})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(sarifLog{Schema: sarifSchema, Version: sarifVersion, Runs: []sarifRun{run}})
}

func runProperties(doc Document) sarifRunProperties {
	bySeverity := make(map[string]int, len(doc.Summary.BySeverity))
	for _, c := range doc.Summary.BySeverity {
		bySeverity[c.Severity.String()] = c.Count
	}
	return sarifRunProperties{
		ScanID:      doc.Scan.ID.String(),
		ScanType:    doc.Scan.Kind,
		Target:      doc.Scan.TargetURL,
		Status:      doc.Scan.Status,
		SubmittedAt: sarifTime(doc.Scan.CreatedAt),
		GeneratedAt: sarifTime(doc.GeneratedAt),
		Summary: sarifSummary{
			Total:      doc.Summary.Total,
			Passed:     doc.Summary.Passed,
			Failed:     doc.Summary.Failed,
			Risk:       doc.Summary.Risk.String(),
			BySeverity: bySeverity,
		},
	}
}

func sarifKind(f Finding) string {
	if f.Passed {
		return "pass"
	}
	return "fail"
}

// resultLevel pilnuje reguły SARIF: wynik o kind innym niż "fail" musi mieć
// level "none".
func resultLevel(f Finding) string {
	if f.Passed {
		return "none"
	}
	return sarifLevel(f.Severity)
}

func sarifLevel(s Severity) string {
	switch s {
	case SeverityCritical, SeverityHigh:
		return "error"
	case SeverityMedium:
		return "warning"
	}
	return "note"
}

// securitySeverity umieszcza każdy poziom w środku odpowiadającego mu
// przedziału CVSS, tak jak przelicza go GitHub.
func securitySeverity(s Severity) string {
	switch s {
	case SeverityCritical:
		return "9.5"
	case SeverityHigh:
		return "8.0"
	case SeverityMedium:
		return "5.5"
	case SeverityLow:
		return "2.0"
	}
	return ""
}

func messageText(f Finding) string {
	if f.Message != "" {
		return f.Message
	}
	return f.TestName + ": " + labelsEN.ResultOf(f)
}

// fingerprint pozwala narzędziom rozpoznać ten sam problem w kolejnych
// skanach tego samego celu.
func fingerprint(target, ruleID string) string {
	sum := sha256.Sum256([]byte(target + "\x00" + ruleID))
	return hex.EncodeToString(sum[:16])
}

func sarifTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func sarifTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return sarifTime(*t)
}
