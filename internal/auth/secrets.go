package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

const SecretTokenBytes = 32

func GenerateSecretToken() (string, []byte, error) {
	buf := make([]byte, SecretTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	return token, HashSecretToken(token), nil
}

func HashSecretToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
