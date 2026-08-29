package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"strings"
)

const (
	RecoveryCodeCount  = 10
	RecoveryCodeLength = 8

	crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

func GenerateRecoveryCodes() ([]string, error) {
	codes := make([]string, 0, RecoveryCodeCount)
	for i := 0; i < RecoveryCodeCount; i++ {
		code, err := generateRecoveryCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

func generateRecoveryCode() (string, error) {
	buf := make([]byte, RecoveryCodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.Grow(RecoveryCodeLength + 1)
	for i, b := range buf {
		if i == RecoveryCodeLength/2 {
			sb.WriteByte('-')
		}
		sb.WriteByte(crockfordAlphabet[int(b)%len(crockfordAlphabet)])
	}
	return sb.String(), nil
}

func NormalizeRecoveryCode(code string) string {
	var sb strings.Builder
	sb.Grow(len(code))
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		switch r {
		case '-', ' ':
		case 'I', 'L':
			sb.WriteRune('1')
		case 'O':
			sb.WriteRune('0')
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func HashRecoveryCode(code string) []byte {
	sum := sha256.Sum256([]byte(NormalizeRecoveryCode(code)))
	return sum[:]
}

func RecoveryCodeMatches(stored, candidate []byte) bool {
	return subtle.ConstantTimeCompare(stored, candidate) == 1
}
