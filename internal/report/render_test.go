package report

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/prawo-i-piesc/backend/internal/models"
)

// hostile udaje wynik, którego treść kontroluje skanowana strona.
func hostileDoc() Document {
	return Build(testSource(
		result("Content Security Policy Analysis", "High", false,
			"CSP allows ![x](https://evil.example/x.png) <img src=x onerror=alert(1)> [click](javascript:alert(1))",
			`{"note":"`+"```"+` <script>alert(1)</script>"}`),
		result("1. Fake heading", "Low", false, "- not a list\n# not a heading\n    not code", `{}`),
		result("HTTPS Protocol Verification", "None", true, "Zażółć gęślą jaźń", `{}`),
	), testNow)
}

func render(t *testing.T, doc Document, f Format) []byte {
	t.Helper()
	return renderIn(t, doc, f, LangEN)
}

func renderIn(t *testing.T, doc Document, f Format, lang Lang) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, doc, f, lang); err != nil {
		t.Fatalf("Render(%s, %s): %v", f, lang, err)
	}
	return buf.Bytes()
}

func TestParseFormat(t *testing.T) {
	for raw, want := range map[string]Format{
		"": FormatPDF, "pdf": FormatPDF, "HTML": FormatHTML, "md": FormatMarkdown, "markdown": FormatMarkdown, "sarif": FormatSARIF,
	} {
		if got, ok := ParseFormat(raw); !ok || got != want {
			t.Errorf("ParseFormat(%q) = %q, %v", raw, got, ok)
		}
	}
	if _, ok := ParseFormat("docx"); ok {
		t.Error("ParseFormat przyjął docx")
	}
}

func TestEveryFormatRendersEmptyScanInEveryLanguage(t *testing.T) {
	doc := Build(testSource(), testNow)
	for _, lang := range Langs {
		for _, f := range Formats {
			if out := renderIn(t, doc, f, lang); len(out) == 0 {
				t.Errorf("%s/%s: pusty wynik", lang, f)
			}
		}
	}
}

func TestParseLang(t *testing.T) {
	for raw, want := range map[string]Lang{"": LangEN, "en": LangEN, "EN-gb": LangEN, "pl": LangPL, "pl-PL": LangPL, "pl_PL": LangPL} {
		if got, ok := ParseLang(raw); !ok || got != want {
			t.Errorf("ParseLang(%q) = %q, %v", raw, got, ok)
		}
	}
	if _, ok := ParseLang("de"); ok {
		t.Error("ParseLang przyjął de")
	}
}

// Brakujące tłumaczenie wyszłoby w raporcie jako pusta etykieta.
func TestEveryLanguageHasEveryLabel(t *testing.T) {
	for _, lang := range Langs {
		l := LabelsFor(lang)
		if l.Lang != lang {
			t.Errorf("LabelsFor(%s) zwraca %s", lang, l.Lang)
		}
		v := reflect.ValueOf(*l)
		for i := range v.NumField() {
			field := v.Type().Field(i)
			if field.Name == "EngineNote" && lang == LangEN {
				continue
			}
			switch value := v.Field(i); value.Kind() {
			case reflect.String:
				if value.String() == "" {
					t.Errorf("%s: pusta etykieta %s", lang, field.Name)
				}
			case reflect.Array:
				for j := range value.Len() {
					if value.Index(j).String() == "" {
						t.Errorf("%s: pusta etykieta %s[%d]", lang, field.Name, j)
					}
				}
			case reflect.Map:
				for _, status := range []string{models.ScanStatusPending, models.ScanStatusRunning, models.ScanStatusCompleted, models.ScanStatusFailed} {
					if value.MapIndex(reflect.ValueOf(status)).String() == "" {
						t.Errorf("%s: brak statusu %s", lang, status)
					}
				}
			}
		}
	}
}

func TestPolishReportTranslatesLabelsButKeepsEngineText(t *testing.T) {
	doc := hostileDoc()

	html := string(renderIn(t, doc, FormatHTML, LangPL))
	for _, want := range []string{`<html lang="pl">`, "Raport bezpieczeństwa AntiGinx", "Podsumowanie", "Wysoka", "Niezaliczony", "Zakończony", "Skan z konta", `<p lang="en">Zażółć gęślą jaźń</p>`} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML(pl) nie zawiera %q", want)
		}
	}
	for _, unwanted := range []string{"Overall assessment", "Findings", ">Passed<"} {
		if strings.Contains(html, unwanted) {
			t.Errorf("HTML(pl) zawiera angielskie %q", unwanted)
		}
	}

	md := string(renderIn(t, doc, FormatMarkdown, LangPL))
	for _, want := range []string{"# Raport bezpieczeństwa AntiGinx", "**Ogólna ocena:** Wysokie ryzyko", "- Ważność: **Wysoka**", "Content Security Policy Analysis", "_Nazwy testów i ich opisy pochodzą z silnika"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown(pl) nie zawiera %q", want)
		}
	}

	en := string(renderIn(t, doc, FormatHTML, LangEN))
	if !strings.Contains(en, `<html lang="en">`) || strings.Contains(en, "silnika") {
		t.Error("HTML(en) ma zły lang albo polską notkę o silniku")
	}

	// SARIF czyta maszyna: ten sam niezależnie od języka.
	if !bytes.Equal(renderIn(t, doc, FormatSARIF, LangPL), renderIn(t, doc, FormatSARIF, LangEN)) {
		t.Error("SARIF zależy od języka")
	}
}

func TestHTMLEscapesScanContent(t *testing.T) {
	out := string(render(t, hostileDoc(), FormatHTML))

	for _, bad := range []string{"<img src=x", "<script>alert"} {
		if strings.Contains(out, bad) {
			t.Errorf("HTML zawiera nieescapowane %q", bad)
		}
	}
	for _, want := range []string{"&lt;img src=x onerror=alert(1)&gt;", "Zażółć gęślą jaźń", "<style>"} {
		if !strings.Contains(out, want) {
			t.Errorf("HTML nie zawiera %q", want)
		}
	}
	if strings.Contains(out, "<script") || strings.Contains(out, "src=\"http") || strings.Contains(out, "href=\"http") {
		t.Error("raport HTML ładuje coś z zewnątrz albo ma skrypt")
	}
}

func TestMarkdownEscapesScanContent(t *testing.T) {
	out := string(render(t, hostileDoc(), FormatMarkdown))

	if regexp.MustCompile(`[^\\]<img`).MatchString(out) {
		t.Error("Markdown zawiera nieescapowany <img")
	}
	for _, bad := range []string{"![x]", "[click]", "\n- not a list", "\n# not a heading", "\n    not code", "### 1. Fake"} {
		if strings.Contains(out, bad) {
			t.Errorf("Markdown zawiera nieescapowane %q", bad)
		}
	}
	for _, want := range []string{`\!\[x\]`, `\<img`, `### 1\. Fake heading`, `\- not a list`, `\# not a heading`, "Zażółć gęślą jaźń"} {
		if !strings.Contains(out, want) {
			t.Errorf("Markdown nie zawiera %q", want)
		}
	}
	// Metadane z ``` w środku muszą siedzieć w dłuższym płocie.
	if !strings.Contains(out, "````json\n") {
		t.Error("blok metadanych nie dostał płotu dłuższego od treści")
	}
}

func TestCodeFenceOutgrowsContent(t *testing.T) {
	for content, want := range map[string]string{
		"plain": "```", "a ` b": "```", "x ``` y": "````", "`````": "``````",
	} {
		if got := codeFence(content); got != want {
			t.Errorf("codeFence(%q) = %q, want %q", content, got, want)
		}
	}
}

func TestPDFIsAValidDocument(t *testing.T) {
	out := render(t, hostileDoc(), FormatPDF)

	if !bytes.HasPrefix(out, []byte("%PDF-")) || !bytes.Contains(out[len(out)-32:], []byte("%%EOF")) {
		t.Errorf("to nie wygląda na PDF: %q…", out[:min(len(out), 16)])
	}
}

func TestPDFHandlesManyFindings(t *testing.T) {
	var results []models.ScanResult
	for i := range 80 {
		results = append(results, result("Test "+strings.Repeat("x", i%7), "Medium", i%2 == 0, strings.Repeat("long message ", 30), `{"k":"`+strings.Repeat("v", 400)+`"}`))
	}
	out := render(t, Build(testSource(results...), testNow), FormatPDF)
	if !bytes.Contains(out, []byte("/Count ")) {
		t.Error("PDF bez drzewa stron")
	}
}

func TestFilenameIsASCII(t *testing.T) {
	doc := Build(testSource(), testNow)
	if got := Filename(doc, FormatSARIF); got != "antiginx-report-019a3b6e-1111-7000-8000-000000000001.sarif" {
		t.Errorf("Filename = %q", got)
	}
}

func TestSARIFFollowsTheSpecification(t *testing.T) {
	doc := Build(testSource(
		result("SSL Certificate Security Analysis", "Critical", false, "expired", `{"days":-3}`),
		result("HSTS Header Analysis", "Medium", false, "", `{}`),
		result("Cookie Security Analysis", "Low", false, "lacks SameSite", `{}`),
		result("Cookie Security Analysis", "Low", false, "lacks Secure", `{}`),
		result("HTTPS Protocol Verification", "None", true, "ok", `{}`),
	), testNow)

	var log struct {
		Schema  string `json:"$schema"`
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name  string `json:"name"`
					Rules []struct {
						ID                   string `json:"id"`
						DefaultConfiguration struct {
							Level string `json:"level"`
						} `json:"defaultConfiguration"`
						Properties map[string]any `json:"properties"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			AutomationDetails map[string]string `json:"automationDetails"`
			Invocations       []struct {
				ExecutionSuccessful bool   `json:"executionSuccessful"`
				StartTimeUTC        string `json:"startTimeUtc"`
				EndTimeUTC          string `json:"endTimeUtc"`
			} `json:"invocations"`
			Results []struct {
				RuleID    string `json:"ruleId"`
				RuleIndex int    `json:"ruleIndex"`
				Kind      string `json:"kind"`
				Level     string `json:"level"`
				Message   struct {
					Text string `json:"text"`
				} `json:"message"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
					} `json:"physicalLocation"`
				} `json:"locations"`
				PartialFingerprints map[string]string `json:"partialFingerprints"`
				Properties          map[string]any    `json:"properties"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(render(t, doc, FormatSARIF), &log); err != nil {
		t.Fatalf("SARIF nie jest JSON-em: %v", err)
	}

	if log.Version != "2.1.0" || !strings.Contains(log.Schema, "sarif-2.1.0") || len(log.Runs) != 1 {
		t.Fatalf("nagłówek SARIF: version %q, schema %q, runs %d", log.Version, log.Schema, len(log.Runs))
	}
	run := log.Runs[0]
	if run.Tool.Driver.Name != "AntiGinx" || run.AutomationDetails["id"] != "antiginx/premium/"+doc.Scan.ID.String() {
		t.Errorf("driver %q, automationDetails %v", run.Tool.Driver.Name, run.AutomationDetails)
	}
	// Schemat SARIF dopuszcza w guid tylko UUID v1–v5, a skany mają UUIDv7.
	if _, ok := run.AutomationDetails["guid"]; ok {
		t.Error("automationDetails.guid z UUIDv7 łamie schemat SARIF")
	}
	inv := run.Invocations[0]
	if !inv.ExecutionSuccessful || inv.StartTimeUTC != "2026-10-31T12:00:00Z" || inv.EndTimeUTC != "2026-10-31T12:00:47Z" {
		t.Errorf("invocation = %+v", inv)
	}

	rules := run.Tool.Driver.Rules
	if len(rules) != 4 {
		t.Fatalf("reguł %d, want 4 (cookie dwa razy, ale jedna reguła)", len(rules))
	}
	if len(run.Results) != 5 {
		t.Fatalf("wyników %d, want 5", len(run.Results))
	}

	wantLevel := map[string]string{
		"ssl-certificate-security-analysis": "error",
		"hsts-header-analysis":              "warning",
		"cookie-security-analysis":          "note",
		"https-protocol-verification":       "none",
	}
	for _, r := range run.Results {
		if rules[r.RuleIndex].ID != r.RuleID {
			t.Errorf("%s: ruleIndex %d wskazuje na %s", r.RuleID, r.RuleIndex, rules[r.RuleIndex].ID)
		}
		if r.Level != wantLevel[r.RuleID] {
			t.Errorf("%s: level %q, want %q", r.RuleID, r.Level, wantLevel[r.RuleID])
		}
		// SARIF §3.27.9: wynik o kind innym niż fail musi mieć level none.
		if r.Kind != "fail" && r.Level != "none" {
			t.Errorf("%s: kind %q z level %q", r.RuleID, r.Kind, r.Level)
		}
		if r.Message.Text == "" {
			t.Errorf("%s: pusty message.text", r.RuleID)
		}
		if len(r.Locations) != 1 || r.Locations[0].PhysicalLocation.ArtifactLocation.URI != "https://example.com/" {
			t.Errorf("%s: locations = %+v", r.RuleID, r.Locations)
		}
		if r.PartialFingerprints[fingerprintKey] == "" {
			t.Errorf("%s: brak odcisku", r.RuleID)
		}
	}

	for _, rule := range rules {
		sev, _ := rule.Properties["security-severity"].(string)
		switch rule.ID {
		case "ssl-certificate-security-analysis":
			if sev != "9.5" || rule.DefaultConfiguration.Level != "error" {
				t.Errorf("ssl: security-severity %q, level %q", sev, rule.DefaultConfiguration.Level)
			}
		case "https-protocol-verification":
			if sev != "" || rule.DefaultConfiguration.Level != "none" {
				t.Errorf("https: security-severity %q, level %q", sev, rule.DefaultConfiguration.Level)
			}
		}
	}

	if meta, ok := run.Results[0].Properties["metadata"].(map[string]any); !ok || meta["days"] != float64(-3) {
		t.Errorf("metadata pierwszego wyniku = %v", run.Results[0].Properties["metadata"])
	}
}
