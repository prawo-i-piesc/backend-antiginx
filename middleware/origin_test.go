package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/httpx"
)

const expectedOrigin = "https://antiginx.pl"

func withOrigin(t *testing.T, origin string) (int, httpx.Code) {
	t.Helper()

	r := gin.New()
	r.POST("/", RequireOrigin(expectedOrigin), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		return w.Code, ""
	}

	var resp httpx.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("odpowiedź nie jest poprawnym JSON-em (%q): %v", w.Body.String(), err)
	}
	return w.Code, resp.Code
}

func TestRequireOriginAllowsMatchingOrigin(t *testing.T) {
	if status, _ := withOrigin(t, expectedOrigin); status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
}

func TestRequireOriginAllowsMissingHeader(t *testing.T) {
	if status, _ := withOrigin(t, ""); status != http.StatusOK {
		t.Errorf("status = %d, want 200 — proxy po stronie serwera nie wysyła Origin", status)
	}
}

func TestRequireOriginRejectsForeignOrigin(t *testing.T) {
	for _, origin := range []string{
		"https://evil.pl",
		"https://antiginx.pl.evil.pl",
		"http://antiginx.pl",
		"null",
	} {
		t.Run(origin, func(t *testing.T) {
			status, code := withOrigin(t, origin)
			if status != http.StatusForbidden {
				t.Errorf("status = %d, want 403", status)
			}
			if code != httpx.CodeForbidden {
				t.Errorf("code = %q, want %q", code, httpx.CodeForbidden)
			}
		})
	}
}
