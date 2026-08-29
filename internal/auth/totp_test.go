package auth

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestGenerateTOTPEnrollment(t *testing.T) {
	enrollment, err := GenerateTOTPEnrollment("jan@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPEnrollment: %v", err)
	}

	if enrollment.Secret == "" {
		t.Fatal("pusty sekret")
	}

	u, err := url.Parse(enrollment.OTPAuthURI)
	if err != nil {
		t.Fatalf("otpauth_uri nie jest poprawnym URI: %v", err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" {
		t.Errorf("otpauth_uri = %q", enrollment.OTPAuthURI)
	}
	if !strings.Contains(u.Path, TOTPIssuer) || !strings.Contains(u.Path, "jan@example.com") {
		t.Errorf("etykieta nie zawiera wydawcy i konta: %q", u.Path)
	}

	q := u.Query()
	for key, want := range map[string]string{
		"issuer":    TOTPIssuer,
		"algorithm": "SHA1",
		"digits":    "6",
		"period":    "30",
		"secret":    enrollment.Secret,
	} {
		if got := q.Get(key); got != want {
			t.Errorf("parametr %s = %q, want %q", key, got, want)
		}
	}
}

func TestGenerateTOTPEnrollmentIsUnique(t *testing.T) {
	first, err := GenerateTOTPEnrollment("jan@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPEnrollment: %v", err)
	}
	second, err := GenerateTOTPEnrollment("jan@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPEnrollment: %v", err)
	}

	if first.Secret == second.Secret {
		t.Error("dwa enrollmenty dały ten sam sekret")
	}
}

func currentCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, at)
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	return code
}

func TestValidateTOTPCode(t *testing.T) {
	enrollment, err := GenerateTOTPEnrollment("jan@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPEnrollment: %v", err)
	}
	now := time.Now().UTC()

	if !ValidateTOTPCode(enrollment.Secret, currentCode(t, enrollment.Secret, now)) {
		t.Error("bieżący kod odrzucony")
	}
}

func TestValidateTOTPCodeAcceptsOneStepSkew(t *testing.T) {
	enrollment, err := GenerateTOTPEnrollment("jan@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPEnrollment: %v", err)
	}
	now := time.Now().UTC()

	for _, offset := range []time.Duration{-30 * time.Second, 30 * time.Second} {
		code := currentCode(t, enrollment.Secret, now.Add(offset))
		if !ValidateTOTPCode(enrollment.Secret, code) {
			t.Errorf("kod z przesunięciem %v odrzucony, a okno to +/-1 krok", offset)
		}
	}
}

func TestValidateTOTPCodeRejectsCodeOutsideWindow(t *testing.T) {
	enrollment, err := GenerateTOTPEnrollment("jan@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPEnrollment: %v", err)
	}
	now := time.Now().UTC()

	for _, offset := range []time.Duration{-5 * time.Minute, 5 * time.Minute} {
		code := currentCode(t, enrollment.Secret, now.Add(offset))
		if ValidateTOTPCode(enrollment.Secret, code) {
			t.Errorf("kod z przesunięciem %v został przyjęty", offset)
		}
	}
}

func TestValidateTOTPCodeRejectsGarbage(t *testing.T) {
	enrollment, err := GenerateTOTPEnrollment("jan@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPEnrollment: %v", err)
	}

	for _, code := range []string{"", "000000", "abcdef", "12345", "1234567"} {
		if ValidateTOTPCode(enrollment.Secret, code) {
			t.Errorf("kod %q został przyjęty", code)
		}
	}
}

func TestValidateTOTPCodeRejectsOtherSecret(t *testing.T) {
	mine, err := GenerateTOTPEnrollment("jan@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPEnrollment: %v", err)
	}
	theirs, err := GenerateTOTPEnrollment("ktos@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPEnrollment: %v", err)
	}

	if ValidateTOTPCode(mine.Secret, currentCode(t, theirs.Secret, time.Now().UTC())) {
		t.Error("kod z cudzego sekretu został przyjęty")
	}
}
