package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
)

const Issuer = "backend-antiginx"

const (
	TokenTypeAccess = "access"
	TokenTypeMFA    = "mfa"
	TokenTypeStepUp = "stepup"
)

var (
	ErrTokenInvalid = errors.New("auth: token is invalid")

	ErrTokenWrongType = errors.New("auth: token has unexpected type")
)

type Claims struct {
	ID      string
	Subject string
	Role    string
	Type    string
}

func GenerateToken(secret []byte, tokenType, subject, role string, ttl time.Duration) (string, error) {
	token, _, err := GenerateTokenWithID(secret, tokenType, subject, role, ttl)
	return token, err
}

func GenerateTokenWithID(secret []byte, tokenType, subject, role string, ttl time.Duration) (string, string, error) {
	if len(secret) == 0 {
		return "", "", errors.New("auth: signing secret is empty")
	}
	if subject == "" {
		return "", "", errors.New("auth: token subject is empty")
	}
	if ttl <= 0 {
		return "", "", fmt.Errorf("auth: token ttl must be positive, got %v", ttl)
	}

	id := uuid.NewString()
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"jti":  id,
		"sub":  subject,
		"role": normalizeRole(role),
		"typ":  tokenType,
		"exp":  now.Add(ttl).Unix(),
		"iat":  now.Unix(),
		"iss":  Issuer,
	})

	signed, err := token.SignedString(secret)
	if err != nil {
		return "", "", err
	}
	return signed, id, nil
}

func ParseToken(secret []byte, tokenString, expectedType string) (*Claims, error) {
	if len(secret) == 0 {
		return nil, errors.New("auth: verification secret is empty")
	}

	parsed, err := jwt.Parse(
		tokenString,
		func(*jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(Issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !parsed.Valid {
		return nil, ErrTokenInvalid
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrTokenInvalid
	}

	subject, ok := claims["sub"].(string)
	if !ok || strings.TrimSpace(subject) == "" {
		return nil, ErrTokenInvalid
	}

	tokenType, ok := claims["typ"].(string)
	if !ok || tokenType == "" {
		return nil, ErrTokenInvalid
	}
	if tokenType != expectedType {
		return nil, ErrTokenWrongType
	}

	role, _ := claims["role"].(string)
	id, _ := claims["jti"].(string)

	return &Claims{
		ID:      id,
		Subject: strings.TrimSpace(subject),
		Role:    normalizeRole(role),
		Type:    tokenType,
	}, nil
}

func normalizeRole(role string) string {
	normalized := strings.ToLower(strings.TrimSpace(role))
	if normalized == "" {
		return models.UserRoleUser
	}
	return normalized
}
