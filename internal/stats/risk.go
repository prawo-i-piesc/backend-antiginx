package stats

import (
	"math"
	"strings"
)

// Model ryzyka skanu, 0–100. Front liczy to samo dla listy skanów
// (frontend-antiginx app/lib/risk.ts): zmiana tutaj wymaga zmiany tam.
//
//  1. Najgorsze znalezisko wyznacza pasmo. Jedno znalezisko danej wagi daje
//     dolną granicę jej pasma: LOW 5, MEDIUM 25, HIGH 50, CRITICAL 75
//     (pasma: 1–24 niskie, 25–49 średnie, 50–74 wysokie, 75–100 krytyczne).
//  2. Każde kolejne znalezisko podnosi wynik: zamyka swoją wagą część luki do
//     100, jak niezależne zdarzenia (1 − Π(1 − w)). Więcej problemów nigdy nie
//     obniża ryzyka, czym grzeszyła dawna średnia wag nieudanych testów.
//  3. Drobne problemy nie udają poważnych: same niskie kończą się na 49,
//     przy najgorszym średnim na 74, przy najgorszym wysokim na 94.
//  4. Jedna przyczyna to jedno znalezisko: strona bez HTTPS nie dostaje
//     osobno kary za brak połączenia TLS w teście certyfikatu ani za brak HSTS.
//
// None i Info to wyniki zdane; komunikaty silnika (notice) nie są wynikami
// i w ogóle tu nie trafiają.

// severityWeight to ryzyko jednego znaleziska danej wagi, 0–1.
var severityWeight = map[string]float64{
	"CRITICAL": 0.75,
	"HIGH":     0.50,
	"MEDIUM":   0.25,
	"LOW":      0.05,
}

// severityCap to najwyższy wynik, gdy najgorsze znalezisko ma daną wagę.
var severityCap = map[string]float64{
	"CRITICAL": 1.00,
	"HIGH":     0.94,
	"MEDIUM":   0.74,
	"LOW":      0.49,
}

// Nazwy testów tak, jak raportuje je silnik.
const (
	httpsTest = "HTTPS Protocol Verification"
	sslTest   = "SSL Certificate Security Analysis"
	hstsTest  = "HSTS Header Analysis"
)

// consequenceOf: znalezisko testu-klucza, gdy padł test-wartość, wynika z tej
// samej przyczyny i się go nie liczy.
var consequenceOf = map[string]string{
	sslTest:  httpsTest,
	hstsTest: httpsTest,
}

// Finding to jeden nieudany wynik testu.
type Finding struct {
	Test     string
	Severity string
}

// Risk zwraca ryzyko skanu, 0–100, przed zaokrągleniem.
func Risk(findings []Finding) float64 {
	failed := make(map[string]bool, len(findings))
	for _, finding := range findings {
		failed[finding.Test] = true
	}

	safe, worst := 1.0, ""
	for _, finding := range findings {
		if cause, ok := consequenceOf[finding.Test]; ok && failed[cause] {
			continue
		}
		severity := strings.ToUpper(strings.TrimSpace(finding.Severity))
		weight, known := severityWeight[severity]
		if !known {
			// Nieudany wynik bez znanej wagi to wciąż problem, tylko najmniejszy.
			severity, weight = "LOW", severityWeight["LOW"]
		}
		safe *= 1 - weight
		if worst == "" || severityWeight[severity] > severityWeight[worst] {
			worst = severity
		}
	}
	if worst == "" {
		return 0
	}
	return math.Min(1-safe, severityCap[worst]) * 100
}
