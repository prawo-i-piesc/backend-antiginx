package report

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/datatypes"
)

var (
	testStart = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	testEnd   = testStart.Add(47 * time.Second)
	testNow   = testEnd.Add(time.Hour)
)

func result(name, severity string, passed bool, message, metadata string) models.ScanResult {
	return models.ScanResult{TestName: name, Severity: severity, Passed: passed, Message: message, Metadata: datatypes.JSON(metadata)}
}

func testSource(results ...models.ScanResult) Source {
	return Source{
		ID:          uuid.MustParse("019a3b6e-1111-7000-8000-000000000001"),
		Kind:        KindPremium,
		TargetURL:   "https://example.com/",
		Status:      models.ScanStatusCompleted,
		CreatedAt:   testStart.Add(-time.Second),
		StartedAt:   &testStart,
		CompletedAt: &testEnd,
		Results:     results,
	}
}

func TestParseSeverity(t *testing.T) {
	for raw, want := range map[string]Severity{
		"None": SeverityNone, "info": SeverityInfo, "LOW": SeverityLow, " Medium ": SeverityMedium,
		"High": SeverityHigh, "Critical": SeverityCritical,
		// Nieznane wartości nie mogą zawyżyć oceny skanu.
		"": SeverityInfo, "Catastrophic": SeverityInfo,
	} {
		if got := ParseSeverity(raw); got != want {
			t.Errorf("ParseSeverity(%q) = %s, want %s", raw, got, want)
		}
	}
}

func TestRuleIDIsAStableSlug(t *testing.T) {
	for name, want := range map[string]string{
		"HSTS Header Analysis":                            "hsts-header-analysis",
		"X-Frame-Options & CSP Frame Protection Analysis": "x-frame-options-csp-frame-protection-analysis",
		"Engine/Execution Error":                          "engine-execution-error",
		"  ":                                              "unnamed-test",
	} {
		if got := RuleID(name); got != want {
			t.Errorf("RuleID(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestBuildOrdersFailuresBySeverityThenPasses(t *testing.T) {
	doc := Build(testSource(
		result("HTTPS Protocol Verification", "None", true, "ok", `{}`),
		result("Cookie Security Analysis", "Low", false, "", `{}`),
		result("SSL Certificate Security Analysis", "Critical", false, "", `{}`),
		result("Bot Protection Block Assessment", "Info", true, "", `{}`),
		result("Content Security Policy Analysis", "High", false, "", `{}`),
		result("CSP Duplicate Name Ordering", "High", false, "", `{}`),
	), testNow)

	var got []string
	for _, f := range doc.Findings {
		got = append(got, f.TestName)
	}
	want := []string{
		"SSL Certificate Security Analysis",
		"CSP Duplicate Name Ordering",
		"Content Security Policy Analysis",
		"Cookie Security Analysis",
		"Bot Protection Block Assessment",
		"HTTPS Protocol Verification",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("kolejność wyników:\n got %v\nwant %v", got, want)
	}
}

func TestBuildSummarizesFailuresAndRisk(t *testing.T) {
	doc := Build(testSource(
		result("a", "High", false, "", `{}`),
		result("b", "Medium", false, "", `{}`),
		result("c", "Medium", false, "", `{}`),
		result("d", "None", true, "", `{}`),
	), testNow)

	s := doc.Summary
	if s.Total != 4 || s.Passed != 1 || s.Failed != 3 || s.Risk != SeverityHigh {
		t.Errorf("summary = %+v", s)
	}
	counts := map[Severity]int{}
	for _, c := range s.BySeverity {
		counts[c.Severity] = c.Count
	}
	if counts[SeverityHigh] != 1 || counts[SeverityMedium] != 2 || counts[SeverityCritical] != 0 || len(s.BySeverity) != 4 {
		t.Errorf("BySeverity = %+v", s.BySeverity)
	}
	if got := LabelsFor(LangEN).Risk(s); got != "High risk" {
		t.Errorf("Risk(en) = %q", got)
	}
	if got := LabelsFor(LangPL).Risk(s); got != "Wysokie ryzyko" {
		t.Errorf("Risk(pl) = %q", got)
	}
}

func TestBuildCountsDuplicateDeliveriesOnce(t *testing.T) {
	dup := result("Cookie Security Analysis", "Low", false, "lacks SameSite", `{"name":"sid"}`)
	doc := Build(testSource(dup, dup, result("Cookie Security Analysis", "Low", false, "lacks Secure", `{"name":"sid"}`)), testNow)

	if len(doc.Findings) != 2 || doc.Summary.Failed != 2 {
		t.Errorf("findings = %d, failed = %d, want 2 i 2", len(doc.Findings), doc.Summary.Failed)
	}
}

func TestBuildFormatsMetadata(t *testing.T) {
	doc := Build(testSource(
		result("empty", "Info", true, "", `{}`),
		result("null", "Info", true, "", `null`),
		result("broken", "Info", true, "", `{nie json`),
		result("data", "Info", true, "", `{"a":1}`),
	), testNow)

	for _, f := range doc.Findings {
		switch f.TestName {
		case "empty", "null", "broken":
			if f.Details != "" {
				t.Errorf("%s: Details = %q, want puste", f.TestName, f.Details)
			}
		case "data":
			if f.Details != "{\n  \"a\": 1\n}" || string(f.Metadata) != `{"a":1}` {
				t.Errorf("data: Details = %q, Metadata = %s", f.Details, f.Metadata)
			}
		}
	}
}

func TestBuildTruncatesLargeMetadataOnRuneBoundary(t *testing.T) {
	big := `{"v":"` + strings.Repeat("ż", maxDetailsBytes) + `"}`
	doc := Build(testSource(result("big", "Info", true, "", big)), testNow)

	f := doc.Findings[0]
	if !f.DetailsTruncated || len(f.Details) > maxDetailsBytes || !utf8.ValidString(f.Details) {
		t.Errorf("truncated = %v, len = %d, valid UTF-8 = %v", f.DetailsTruncated, len(f.Details), utf8.ValidString(f.Details))
	}
	if string(f.Metadata) != big {
		t.Error("Metadata dla SARIF zostało przycięte")
	}
}

func TestScanDuration(t *testing.T) {
	info := Build(testSource(), testNow).Scan
	if info.Duration() != 47*time.Second {
		t.Errorf("Duration = %s", info.Duration())
	}
	info.CompletedAt = nil
	if info.Duration() != 0 {
		t.Errorf("Duration bez końca = %s, want 0", info.Duration())
	}
}
