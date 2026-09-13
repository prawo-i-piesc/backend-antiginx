package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/prawo-i-piesc/backend/internal/models"
)

var testSecret = []byte("test-secret-that-is-long-enough-for-hs256")

func TestGenerateAndParseRoundTrip(t *testing.T) {
	token, err := GenerateToken(testSecret, TokenTypeAccess, "user-1", models.UserRoleAdmin, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	claims, err := ParseToken(testSecret, token, TokenTypeAccess)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.Subject != "user-1" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "user-1")
	}
	if claims.Role != models.UserRoleAdmin {
		t.Errorf("Role = %q, want %q", claims.Role, models.UserRoleAdmin)
	}
	if claims.Type != TokenTypeAccess {
		t.Errorf("Type = %q, want %q", claims.Type, TokenTypeAccess)
	}
}

func TestParseTokenRejectsWrongTokenType(t *testing.T) {
	mfaToken, err := GenerateToken(testSecret, TokenTypeMFA, "user-1", models.UserRoleUser, 5*time.Minute)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	if _, err := ParseToken(testSecret, mfaToken, TokenTypeAccess); !errors.Is(err, ErrTokenWrongType) {
		t.Fatalf("ParseToken accepted an MFA token as an access token, err = %v", err)
	}

	if _, err := ParseToken(testSecret, mfaToken, TokenTypeMFA); err != nil {
		t.Errorf("ParseToken rejected an MFA token at the MFA endpoint: %v", err)
	}
}

func TestParseTokenRejects(t *testing.T) {
	signedWith := func(t *testing.T, secret []byte, claims jwt.MapClaims) string {
		t.Helper()
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
		if err != nil {
			t.Fatalf("signing test token: %v", err)
		}
		return token
	}
	now := time.Now()

	tests := []struct {
		name   string
		token  func(t *testing.T) string
		secret []byte
	}{
		{
			name:  "garbage",
			token: func(*testing.T) string { return "not-a-token" },
		},
		{
			name: "signed with another secret",
			token: func(t *testing.T) string {
				token, err := GenerateToken([]byte("a-completely-different-secret-value"), TokenTypeAccess, "user-1", "", time.Hour)
				if err != nil {
					t.Fatalf("GenerateToken: %v", err)
				}
				return token
			},
		},
		{
			name: "expired",
			token: func(t *testing.T) string {
				return signedWith(t, testSecret, jwt.MapClaims{
					"sub": "user-1", "typ": TokenTypeAccess, "iss": Issuer,
					"exp": now.Add(-time.Minute).Unix(), "iat": now.Add(-time.Hour).Unix(),
				})
			},
		},
		{
			name: "no expiry",
			token: func(t *testing.T) string {
				return signedWith(t, testSecret, jwt.MapClaims{
					"sub": "user-1", "typ": TokenTypeAccess, "iss": Issuer, "iat": now.Unix(),
				})
			},
		},
		{
			name: "foreign issuer",
			token: func(t *testing.T) string {
				return signedWith(t, testSecret, jwt.MapClaims{
					"sub": "user-1", "typ": TokenTypeAccess, "iss": "someone-else",
					"exp": now.Add(time.Hour).Unix(),
				})
			},
		},
		{

			name: "legacy token without typ",
			token: func(t *testing.T) string {
				return signedWith(t, testSecret, jwt.MapClaims{
					"sub": "user-1", "role": models.UserRoleUser, "iss": Issuer,
					"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(),
				})
			},
		},
		{
			name: "empty subject",
			token: func(t *testing.T) string {
				return signedWith(t, testSecret, jwt.MapClaims{
					"sub": "   ", "typ": TokenTypeAccess, "iss": Issuer,
					"exp": now.Add(time.Hour).Unix(),
				})
			},
		},
		{
			name: "none algorithm",
			token: func(t *testing.T) string {
				token, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
					"sub": "user-1", "typ": TokenTypeAccess, "iss": Issuer,
					"exp": now.Add(time.Hour).Unix(),
				}).SignedString(jwt.UnsafeAllowNoneSignatureType)
				if err != nil {
					t.Fatalf("signing unsigned token: %v", err)
				}
				return token
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseToken(testSecret, tt.token(t), TokenTypeAccess); err == nil {
				t.Fatal("ParseToken accepted a token it should have rejected")
			}
		})
	}
}

func TestGenerateTokenRejectsBadInput(t *testing.T) {
	tests := []struct {
		name    string
		secret  []byte
		subject string
		ttl     time.Duration
	}{
		{"empty secret", nil, "user-1", time.Hour},
		{"empty subject", testSecret, "", time.Hour},
		{"zero ttl", testSecret, "user-1", 0},
		{"negative ttl", testSecret, "user-1", -time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := GenerateToken(tt.secret, TokenTypeAccess, tt.subject, "", tt.ttl); err == nil {
				t.Error("GenerateToken accepted invalid input")
			}
		})
	}
}

func TestGenerateTokenDefaultsEmptyRole(t *testing.T) {
	token, err := GenerateToken(testSecret, TokenTypeAccess, "user-1", "  ", time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	claims, err := ParseToken(testSecret, token, TokenTypeAccess)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.Role != models.UserRoleUser {
		t.Errorf("Role = %q, want %q", claims.Role, models.UserRoleUser)
	}
}

func TestParseTokenNormalizesRole(t *testing.T) {
	token, err := GenerateToken(testSecret, TokenTypeAccess, "user-1", "  ADMIN ", time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	claims, err := ParseToken(testSecret, token, TokenTypeAccess)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.Role != models.UserRoleAdmin {
		t.Errorf("Role = %q, want %q", claims.Role, models.UserRoleAdmin)
	}
}
