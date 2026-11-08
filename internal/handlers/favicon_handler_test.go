package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/favicons"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
)

const testIconHash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func faviconRouter(h *FaviconHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/favicons/:hash", h.HandleGet)
	return r
}

func TestFaviconIsServedForeverCacheableAndSandboxed(t *testing.T) {
	db := testDB(t)
	seedScan(t, db, &models.Favicon{Hash: testIconHash, ContentType: "image/svg+xml", Data: []byte("<svg/>"), CreatedAt: time.Now()})
	r := faviconRouter(NewFaviconHandler(db))

	w := get(t, r, "/favicons/"+testIconHash)
	if w.Code != http.StatusOK || w.Body.String() != "<svg/>" || w.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("got %d %q %q", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	for header, want := range map[string]string{
		"Cache-Control":                "public, max-age=31536000, immutable",
		"ETag":                         `"` + testIconHash + `"`,
		"Content-Security-Policy":      faviconCSP,
		"Cross-Origin-Resource-Policy": "same-origin",
	} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestFaviconAnswersARevalidationWithoutReadingTheDatabase(t *testing.T) {
	// Bez bazy: 304 nie może jej dotykać.
	r := faviconRouter(NewFaviconHandler(nil))
	for _, inm := range []string{`"` + testIconHash + `"`, `W/"other", W/"` + testIconHash + `"`} {
		w := sendWith(t, r, "/favicons/"+testIconHash, "If-None-Match", inm)
		if w.Code != http.StatusNotModified || w.Header().Get("ETag") == "" {
			t.Errorf("If-None-Match %s: got %d", inm, w.Code)
		}
	}
}

func TestFaviconRejectsUnknownAndMalformedHashes(t *testing.T) {
	r := faviconRouter(NewFaviconHandler(testDB(t)))
	for _, path := range []string{"/favicons/" + testIconHash, "/favicons/" + strings.ToUpper(testIconHash), "/favicons/abc"} {
		w := get(t, r, path)
		if w.Code != http.StatusNotFound || errorCode(t, w.Body.Bytes()) != httpx.CodeNotFound {
			t.Errorf("%s: got %d %s", path, w.Code, w.Body)
		}
		if w.Header().Get("Cache-Control") != "" {
			t.Errorf("%s: a miss is cacheable: %q", path, w.Header().Get("Cache-Control"))
		}
	}
}

func TestScanResponsesCarryTheSiteIcon(t *testing.T) {
	db := testDB(t)
	icons := favicons.New(db, false)
	hash := testIconHash
	seedScan(t, db, &models.SiteIcon{Host: "example.com", FaviconHash: &hash, CheckedAt: time.Now().UTC()})

	withIcon := models.Scan{ID: uuid.New(), TargetURL: "https://example.com/shop", Status: models.ScanStatusCompleted, CreatedAt: time.Now()}
	withoutIcon := models.Scan{ID: uuid.New(), TargetURL: "https://unknown.example.org", Status: models.ScanStatusCompleted, CreatedAt: time.Now()}
	seedScan(t, db, &withIcon)
	seedScan(t, db, &withoutIcon)

	h := NewScanHandler(nil, db, false)
	h.UseIcons(icons)
	r := scanRouter(h)

	for scan, want := range map[uuid.UUID]*string{withIcon.ID: &hash, withoutIcon.ID: nil} {
		w := get(t, r, "/freescans/"+scan.String())
		var body struct {
			Favicon *string `json:"favicon"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
			t.Fatalf("got %d %s", w.Code, w.Body)
		}
		if (body.Favicon == nil) != (want == nil) || (want != nil && *body.Favicon != *want) {
			t.Errorf("scan %s: favicon = %v, want %v", scan, body.Favicon, want)
		}
	}

}

func sendWith(t *testing.T, r *gin.Engine, path, header, value string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(header, value)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
