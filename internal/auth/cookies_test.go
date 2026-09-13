package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func recordCookies(t *testing.T, handler gin.HandlerFunc) []*http.Cookie {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", handler)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	return (&http.Response{Header: w.Header()}).Cookies()
}

func TestSetSessionCookieAttributes(t *testing.T) {
	cookies := recordCookies(t, func(c *gin.Context) {
		SetSessionCookie(c, "token-wartosc", 720*time.Hour, true)
	})

	if len(cookies) != 1 {
		t.Fatalf("ustawiono %d ciasteczek, a odpowiedź musi nieść dokładnie jedno", len(cookies))
	}

	cookie := cookies[0]
	if cookie.Name != SessionCookieName {
		t.Errorf("nazwa = %q, want %q", cookie.Name, SessionCookieName)
	}
	if cookie.Value != "token-wartosc" {
		t.Errorf("wartość = %q", cookie.Value)
	}
	if !cookie.HttpOnly {
		t.Error("brak HttpOnly")
	}
	if !cookie.Secure {
		t.Error("brak Secure mimo COOKIE_SECURE=true")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("Path = %q, want / — inaczej bramka na /admin nie zobaczy ciasteczka", cookie.Path)
	}
	if cookie.Domain != "" {
		t.Errorf("Domain = %q, a atrybutu Domain nie wolno ustawiać", cookie.Domain)
	}
	if cookie.MaxAge != int((720 * time.Hour).Seconds()) {
		t.Errorf("MaxAge = %d", cookie.MaxAge)
	}
}

func TestSetSessionCookieWithoutSecure(t *testing.T) {
	cookies := recordCookies(t, func(c *gin.Context) {
		SetSessionCookie(c, "token", time.Hour, false)
	})

	if cookies[0].Secure {
		t.Error("Secure ustawione mimo COOKIE_SECURE=false")
	}
}

func TestClearSessionCookie(t *testing.T) {
	cookies := recordCookies(t, func(c *gin.Context) {
		ClearSessionCookie(c, true)
	})

	if len(cookies) != 1 {
		t.Fatalf("ustawiono %d ciasteczek", len(cookies))
	}
	if cookies[0].Value != "" {
		t.Errorf("wartość = %q, want pusta", cookies[0].Value)
	}
	if cookies[0].MaxAge >= 0 {
		t.Errorf("MaxAge = %d, ciasteczko nie zostanie skasowane", cookies[0].MaxAge)
	}
}

func TestSessionCookieReadsValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	var got string
	r.GET("/", func(c *gin.Context) {
		got = SessionCookie(c)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "wartosc"})
	r.ServeHTTP(httptest.NewRecorder(), req)

	if got != "wartosc" {
		t.Errorf("SessionCookie = %q, want %q", got, "wartosc")
	}
}

func TestSessionCookieMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	var got string
	r.GET("/", func(c *gin.Context) {
		got = SessionCookie(c)
		c.Status(http.StatusOK)
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if got != "" {
		t.Errorf("SessionCookie = %q, want pusty", got)
	}
}
