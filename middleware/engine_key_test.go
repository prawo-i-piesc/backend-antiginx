package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func engineKeyRouter(keys []string) *gin.Engine {
	r := gin.New()
	r.POST("/results", RequireEngineKey(keys), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return r
}

func postResult(r *gin.Engine, key string) int {
	req := httptest.NewRequest(http.MethodPost, "/results", nil)
	if key != "" {
		req.Header.Set(EngineKeyHeader, key)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestRequireEngineKeyAcceptsEveryConfiguredKey(t *testing.T) {
	r := engineKeyRouter([]string{"nowy-klucz", "stary-klucz"})

	for _, key := range []string{"nowy-klucz", "stary-klucz"} {
		if code := postResult(r, key); code != http.StatusOK {
			t.Errorf("klucz %q: status = %d, want 200", key, code)
		}
	}
}

func TestRequireEngineKeyRejectsMissingOrWrongKey(t *testing.T) {
	r := engineKeyRouter([]string{"nowy-klucz"})

	for _, key := range []string{"", "zly-klucz", "nowy-klucz-", "nowy"} {
		if code := postResult(r, key); code != http.StatusForbidden {
			t.Errorf("klucz %q: status = %d, want 403", key, code)
		}
	}
}

func TestRequireEngineKeyIsOpenWithoutKeys(t *testing.T) {
	if code := postResult(engineKeyRouter(nil), ""); code != http.StatusOK {
		t.Errorf("status = %d, want 200 przy pustej liście kluczy", code)
	}
}
