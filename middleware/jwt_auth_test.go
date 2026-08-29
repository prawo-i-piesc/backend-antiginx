package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/models"
)

var testSecret = []byte("test-secret-that-is-long-enough-for-hs256")

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

func guarded(t *testing.T, authHeader string) (status int, userID, role string) {
	t.Helper()

	r := gin.New()
	r.GET("/", RequireAuth(testSecret), func(c *gin.Context) {
		userID = c.GetString("userID")
		role = c.GetString("userRole")
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	return w.Code, userID, role
}

func bearer(t *testing.T, tokenType, subject, role string, ttl time.Duration) string {
	t.Helper()
	token, err := auth.GenerateToken(testSecret, tokenType, subject, role, ttl)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	return "Bearer " + token
}

func TestRequireAuthAcceptsAccessToken(t *testing.T) {
	status, userID, role := guarded(t, bearer(t, auth.TokenTypeAccess, "user-1", models.UserRoleAdmin, time.Hour))

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if userID != "user-1" {
		t.Errorf("userID = %q, want %q", userID, "user-1")
	}
	if role != models.UserRoleAdmin {
		t.Errorf("userRole = %q, want %q", role, models.UserRoleAdmin)
	}
}

func TestRequireAuthRejectsMFATokenAsBearer(t *testing.T) {
	status, userID, _ := guarded(t, bearer(t, auth.TokenTypeMFA, "user-1", models.UserRoleUser, 5*time.Minute))

	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — an MFA token was accepted as an access token", status)
	}
	if userID != "" {
		t.Errorf("userID = %q, want the handler not to have run", userID)
	}
}

func TestRequireAuthRejectsStepUpTokenAsBearer(t *testing.T) {
	status, _, _ := guarded(t, bearer(t, auth.TokenTypeStepUp, "user-1", models.UserRoleUser, 5*time.Minute))

	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
}

func TestRequireAuthRejects(t *testing.T) {
	tests := []struct {
		name   string
		header func(t *testing.T) string
	}{
		{"no header", func(*testing.T) string { return "" }},
		{"no Bearer prefix", func(t *testing.T) string {
			token, err := auth.GenerateToken(testSecret, auth.TokenTypeAccess, "user-1", "", time.Hour)
			if err != nil {
				t.Fatalf("GenerateToken: %v", err)
			}
			return token
		}},
		{"wrong scheme", func(t *testing.T) string { return "Basic dXNlcjpwYXNz" }},
		{"lowercase bearer", func(t *testing.T) string {
			token, err := auth.GenerateToken(testSecret, auth.TokenTypeAccess, "user-1", "", time.Hour)
			if err != nil {
				t.Fatalf("GenerateToken: %v", err)
			}
			return "bearer " + token
		}},
		{"garbage token", func(*testing.T) string { return "Bearer not-a-token" }},
		{"expired token", func(t *testing.T) string {
			return bearer(t, auth.TokenTypeAccess, "user-1", "", time.Nanosecond)
		}},
		{"signed with another secret", func(t *testing.T) string {
			token, err := auth.GenerateToken([]byte("a-completely-different-secret-value"), auth.TokenTypeAccess, "user-1", "", time.Hour)
			if err != nil {
				t.Fatalf("GenerateToken: %v", err)
			}
			return "Bearer " + token
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, userID, _ := guarded(t, tt.header(t))
			if status != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", status)
			}
			if userID != "" {
				t.Errorf("userID = %q, want the handler not to have run", userID)
			}
		})
	}
}

func TestRequireAuthDefaultsMissingRole(t *testing.T) {
	status, _, role := guarded(t, bearer(t, auth.TokenTypeAccess, "user-1", "", time.Hour))

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if role != models.UserRoleUser {
		t.Errorf("userRole = %q, want %q", role, models.UserRoleUser)
	}
}
