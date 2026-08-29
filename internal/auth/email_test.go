package auth

import "testing"

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"already normalized", "jan@example.com", "jan@example.com"},
		{"uppercase", "Jan@Example.COM", "jan@example.com"},
		{"surrounding whitespace", "  jan@example.com\t", "jan@example.com"},
		{"whitespace and case", " JAN@EXAMPLE.COM ", "jan@example.com"},
		{"empty", "", ""},
		{"plus tag is preserved", "jan+scan@example.com", "jan+scan@example.com"},
		{"dots are preserved", "j.a.n@example.com", "j.a.n@example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeEmail(tt.input); got != tt.want {
				t.Errorf("NormalizeEmail(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeEmailIsIdempotent(t *testing.T) {
	once := NormalizeEmail(" Jan@Example.COM ")
	if twice := NormalizeEmail(once); twice != once {
		t.Errorf("NormalizeEmail is not idempotent: %q then %q", once, twice)
	}
}
