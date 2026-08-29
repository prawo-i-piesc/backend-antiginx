package auth

import (
	"regexp"
	"strings"
	"testing"
)

var recoveryCodeFormat = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`)

func TestGenerateRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}

	if len(codes) != RecoveryCodeCount {
		t.Fatalf("wygenerowano %d kodów, oczekiwano %d", len(codes), RecoveryCodeCount)
	}

	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		if !recoveryCodeFormat.MatchString(code) {
			t.Errorf("kod %q nie ma formatu XXXX-XXXX z alfabetu Crockforda", code)
		}
		if seen[code] {
			t.Errorf("kod %q powtórzył się w jednej partii", code)
		}
		seen[code] = true
	}
}

func TestGenerateRecoveryCodesAreNotRepeatedAcrossBatches(t *testing.T) {
	first, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	second, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}

	seen := make(map[string]bool, len(first))
	for _, code := range first {
		seen[code] = true
	}
	for _, code := range second {
		if seen[code] {
			t.Errorf("kod %q pojawił się w dwóch partiach", code)
		}
	}
}

func TestNormalizeRecoveryCode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"A3F9-K2M7", "A3F9K2M7"},
		{"a3f9-k2m7", "A3F9K2M7"},
		{"  A3F9-K2M7  ", "A3F9K2M7"},
		{"A3F9K2M7", "A3F9K2M7"},
		{"A3F9 K2M7", "A3F9K2M7"},
		{"I3F9-K2M7", "13F9K2M7"},
		{"L3F9-K2M7", "13F9K2M7"},
		{"O3F9-K2M7", "03F9K2M7"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := NormalizeRecoveryCode(tt.input); got != tt.want {
				t.Errorf("NormalizeRecoveryCode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHashRecoveryCodeIgnoresFormatting(t *testing.T) {
	codes, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	code := codes[0]

	stored := HashRecoveryCode(code)
	for _, variant := range []string{
		strings.ToLower(code),
		strings.ReplaceAll(code, "-", ""),
		" " + code + " ",
	} {
		if !RecoveryCodeMatches(stored, HashRecoveryCode(variant)) {
			t.Errorf("wariant %q nie pasuje do zapisanego hasza", variant)
		}
	}
}

func TestRecoveryCodeMatchesRejectsOtherCode(t *testing.T) {
	codes, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}

	if RecoveryCodeMatches(HashRecoveryCode(codes[0]), HashRecoveryCode(codes[1])) {
		t.Error("dwa różne kody uznane za zgodne")
	}
}
