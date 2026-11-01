package stats

import (
	"math"
	"strings"
	"testing"
)

func findings(spec ...string) []Finding {
	out := make([]Finding, 0, len(spec))
	for i, s := range spec {
		test, severity, ok := strings.Cut(s, ":")
		if !ok {
			test, severity = "test-"+string(rune('a'+i)), s
		}
		out = append(out, Finding{Test: test, Severity: severity})
	}
	return out
}

func repeat(severity string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = severity
	}
	return out
}

func TestRisk(t *testing.T) {
	cases := []struct {
		name     string
		findings []Finding
		want     int
	}{
		{"nic nie padło", nil, 0},
		{"jedno niskie", findings("LOW"), 5},
		{"jedno średnie", findings("MEDIUM"), 25},
		{"jedno wysokie", findings("HIGH"), 50},
		{"jedno krytyczne", findings("CRITICAL"), 75},
		// Typowa strona bez części nagłówków: cztery niskie i brak CSP.
		{"cztery niskie i średnie", findings("LOW", "LOW", "LOW", "LOW", "MEDIUM"), 39},
		// Dawna średnia dawała tu 32, mniej niż za samo krytyczne.
		{"krytyczne i dziesięć niskich", findings(append([]string{"CRITICAL"}, repeat("LOW", 10)...)...), 85},
		{"dwa wysokie", findings("HIGH", "HIGH"), 75},
		{"same niskie nie wychodzą ponad średnie pasmo", findings(repeat("LOW", 18)...), 49},
		{"średnie nie wychodzą ponad wysokie pasmo", findings(repeat("MEDIUM", 8)...), 74},
		{"nieznana waga liczy się jak niskie", findings("WEIRD"), 5},
		{"wielkość liter i spacje", findings(" high "), 50},
		// Brak HTTPS: błąd TLS w teście certyfikatu i brak HSTS to jego skutki.
		{"strona bez HTTPS", findings(httpsTest+":MEDIUM", sslTest+":MEDIUM", hstsTest+":LOW"), 25},
		{"certyfikat liczy się, gdy HTTPS działa", findings(sslTest+":MEDIUM", hstsTest+":LOW"), 29},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := int(math.Round(Risk(tc.findings))); got != tc.want {
				t.Errorf("Risk = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRiskNeverDropsWhenAFindingIsAdded(t *testing.T) {
	base := findings("HIGH", "MEDIUM")
	before := Risk(base)
	for _, severity := range []string{"LOW", "MEDIUM", "HIGH", "CRITICAL"} {
		if after := Risk(append(base, Finding{Test: "extra", Severity: severity})); after < before {
			t.Errorf("dodanie %s obniżyło ryzyko z %.1f do %.1f", severity, before, after)
		}
	}
}
