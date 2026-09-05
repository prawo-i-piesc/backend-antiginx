package oauth

import "testing"

func TestSanitizeNextAcceptsRelativePaths(t *testing.T) {
	for _, next := range []string{
		"/dashboard",
		"/profile/security",
		"/scans?id=42",
		"/a",
	} {
		t.Run(next, func(t *testing.T) {
			if got := SanitizeNext(next); got != next {
				t.Errorf("SanitizeNext(%q) = %q, want %q", next, got, next)
			}
		})
	}
}

func TestSanitizeNextRejectsOpenRedirects(t *testing.T) {
	for _, next := range []string{
		"https://attacker.invalid",
		"http://attacker.invalid/x",
		"//attacker.invalid",
		"//attacker.invalid/path",
		"/\\attacker.invalid",
		"/\\/attacker.invalid",
		"dashboard",
		"",
		"javascript:alert(1)",
		"/dashboard\r\nLocation: https://attacker.invalid",
		"/dashboard\nSet-Cookie: x=1",
		"/dash\tboard",
	} {
		t.Run(next, func(t *testing.T) {
			if got := SanitizeNext(next); got != DefaultNext {
				t.Errorf("SanitizeNext(%q) = %q, a powinno wrócić do %q", next, got, DefaultNext)
			}
		})
	}
}
