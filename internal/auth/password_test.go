package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePasswordLength(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  error
	}{
		{"za krótkie", "krotkie", ErrPasswordTooShort},
		{"jedenaście znaków", strings.Repeat("a", 11), ErrPasswordTooShort},
		{"dokładnie dwanaście", "prawidloweXY", nil},
		{"długie", "bardzo-dlugie-i-unikalne-haslo", nil},
		{"puste", "", ErrPasswordTooShort},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidatePassword(%q) = %v, want %v", tt.password, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePasswordCountsRunesNotBytes(t *testing.T) {
	password := strings.Repeat("ł", MinPasswordLength)

	if len(password) < MinPasswordLength {
		t.Fatal("test zakłada, że hasło ma więcej bajtów niż znaków")
	}
	if err := ValidatePassword(password); err != nil {
		t.Errorf("hasło o %d znakach odrzucone: %v", MinPasswordLength, err)
	}

	if err := ValidatePassword(strings.Repeat("ł", MinPasswordLength-1)); !errors.Is(err, ErrPasswordTooShort) {
		t.Error("hasło o 11 znakach wielobajtowych przeszło, a liczy się znaki")
	}
}

func TestValidatePasswordRejectsCommonPasswords(t *testing.T) {
	for _, password := range []string{
		"password1234",
		"qwertyuiop123",
		"iloveyou1234",
		"mysecretpassword",
		"thisisapassword",
	} {
		t.Run(password, func(t *testing.T) {
			if err := ValidatePassword(password); !errors.Is(err, ErrPasswordTooCommon) {
				t.Errorf("ValidatePassword(%q) = %v, want ErrPasswordTooCommon", password, err)
			}
		})
	}
}

func TestIsCommonPasswordIgnoresCase(t *testing.T) {
	for _, variant := range []string{"PASSWORD1234", "Password1234", "  password1234  "} {
		if !IsCommonPassword(variant) {
			t.Errorf("wariant %q nie został rozpoznany jako popularne hasło", variant)
		}
	}
}

func TestIsCommonPasswordAcceptsUnusualPassword(t *testing.T) {
	if IsCommonPassword("kolczasty-rower-hiacynt-42") {
		t.Error("nietypowe hasło uznane za popularne")
	}
}

func TestCommonPasswordListIsLoaded(t *testing.T) {
	commonPasswordsOnce.Do(loadCommonPasswords)

	if len(commonPasswords) < 100 {
		t.Errorf("lista popularnych haseł ma %d pozycji, to podejrzanie mało", len(commonPasswords))
	}
	if _, found := commonPasswords[""]; found {
		t.Error("pusty wiersz trafił na listę")
	}
}

func TestCompareDummyPasswordDoesNotPanic(t *testing.T) {
	CompareDummyPassword("cokolwiek")
	CompareDummyPassword("")
}
