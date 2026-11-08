package favicons

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func siteOf(t *testing.T, raw string) Site {
	t.Helper()
	site, ok := SiteOf(raw)
	if !ok {
		t.Fatalf("SiteOf(%q)", raw)
	}
	return site
}

func TestPublicOnlyRefusesInternalAddresses(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:80", "10.0.0.5:443", "192.168.1.1:80", "100.64.0.1:80", "169.254.169.254:80", "[::1]:443", "[fd00::1]:80", "0.0.0.0:80"} {
		if err := publicOnly("tcp", addr, nil); !errors.Is(err, errPrivateAddress) {
			t.Errorf("publicOnly(%s) = %v, want errPrivateAddress", addr, err)
		}
	}
	for _, addr := range []string{"93.184.216.34:443", "[2606:2800:220:1:248:1893:25c8:1946]:80"} {
		if err := publicOnly("tcp", addr, nil); err != nil {
			t.Errorf("publicOnly(%s) = %v", addr, err)
		}
	}
}

// Sprawdzenie nazwy można obejść domeną, która rozwiązuje się na adres
// wewnętrzny. Połączenie i tak nie powstanie, bo dialer sprawdza adres IP.
func TestFetcherNeverConnectsToAPrivateAddress(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()

	_, _, err := newFetcher(false).get(context.Background(), srv.URL+"/favicon.ico", "image/*", downloadLimit, false)
	if !errors.Is(err, errPrivateAddress) || hit {
		t.Errorf("err = %v, reached server = %v", err, hit)
	}

	if _, err := newFetcher(false).fetch(context.Background(), siteOf(t, srv.URL)); !errors.Is(err, errNoIcon) || hit {
		t.Errorf("fetch of a private site: err = %v, reached server = %v", err, hit)
	}
}

func TestFetchPicksTheDeclaredIconAndFallsBackPastBrokenOnes(t *testing.T) {
	icon32 := pngOf(t, 32, 32)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`<html><head>
			<link rel="icon" href="/missing.png" sizes="32x32">
			<link rel="icon" href="/huge.png" sizes="48x48">
			<link rel="icon" href="/real.png">
		</head></html>`))
	})
	mux.HandleFunc("/huge.png", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte{0}, downloadLimit+1))
	})
	mux.HandleFunc("/real.png", func(w http.ResponseWriter, _ *http.Request) {
		// Nagłówek serwera kłamie; liczy się treść.
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(icon32)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	icon, err := newFetcher(true).fetch(context.Background(), siteOf(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if icon.ContentType != "image/png" || !bytes.Equal(icon.Data, icon32) {
		t.Errorf("got %s, %d bytes", icon.ContentType, len(icon.Data))
	}
}

func TestFetchFollowsARedirectedHomePage(t *testing.T) {
	icon := pngOf(t, 16, 16)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/pl/", http.StatusFound)
	})
	mux.HandleFunc("/pl/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<head><link rel="icon" href="icon.png"></head>`))
	})
	mux.HandleFunc("/pl/icon.png", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(icon) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := newFetcher(true).fetch(context.Background(), siteOf(t, srv.URL))
	if err != nil || !bytes.Equal(got.Data, icon) {
		t.Errorf("fetch = %v, %d bytes", err, len(got.Data))
	}
}

func TestFetchTellsAMissingIconFromAnUnreachableSite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><head><title>no icon</title></head></html>"))
	}))
	site := siteOf(t, srv.URL)
	if _, err := newFetcher(true).fetch(context.Background(), site); !errors.Is(err, errNoIcon) {
		t.Errorf("page without icon: %v, want errNoIcon", err)
	}

	srv.Close()
	if _, err := newFetcher(true).fetch(context.Background(), site); err == nil || errors.Is(err, errNoIcon) {
		t.Errorf("closed server: %v, want a network error", err)
	}
}
