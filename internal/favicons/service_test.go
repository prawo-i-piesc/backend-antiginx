package favicons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Favicon{}, &models.SiteIcon{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// fakeSites odpowiada jak witryny o podanych ikonach i liczy zapytania.
type fakeSites struct {
	mu    sync.Mutex
	icons map[string]any // []byte, errNoIcon albo inny błąd
	calls map[string]int
}

func (f *fakeSites) fetch(_ context.Context, site Site) (Icon, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[site.Key]++
	switch v := f.icons[site.Key].(type) {
	case []byte:
		return Icon{ContentType: "image/png", Data: v}, nil
	case error:
		return Icon{}, v
	}
	return Icon{}, errNoIcon
}

func newTestService(t *testing.T, sites map[string]any) (*Service, *fakeSites, *time.Time) {
	t.Helper()
	fake := &fakeSites{icons: sites, calls: map[string]int{}}
	s := newService(testDB(t), fake.fetch)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	return s, fake, &now
}

// drain obsługuje wszystko, co czeka w kolejce, w tym wątku.
func drain(s *Service) {
	for {
		select {
		case site := <-s.jobs:
			s.work(context.Background(), site)
		default:
			return
		}
	}
}

func TestLookupQueuesUnknownSitesAndServesThemOnceFetched(t *testing.T) {
	icon := []byte("icon-a")
	s, fake, _ := newTestService(t, map[string]any{"a.com": icon})
	ctx := context.Background()

	if icons := s.Lookup(ctx, "https://a.com/x", "http://a.com/y"); len(icons) != 0 {
		t.Fatalf("icons before fetch: %v", icons)
	}
	if len(s.jobs) != 1 {
		t.Fatalf("queued %d jobs for one site, want 1", len(s.jobs))
	}
	drain(s)

	got := s.Lookup(ctx, "https://a.com/").For("https://a.com/")
	if got == nil || *got != hashOf(icon) {
		t.Fatalf("hash = %v, want %s", got, hashOf(icon))
	}
	if len(s.jobs) != 0 || fake.calls["a.com"] != 1 {
		t.Errorf("fresh icon re-queued: %d jobs, %d fetches", len(s.jobs), fake.calls["a.com"])
	}
}

func TestSitesWithTheSameIconShareOneRow(t *testing.T) {
	same := []byte("shared")
	s, _, _ := newTestService(t, map[string]any{"a.com": same, "b.com": same, "c.com": []byte("other")})
	s.Want("https://a.com")
	s.Want("https://b.com")
	s.Want("https://c.com")
	drain(s)

	var count int64
	s.db.Model(&models.Favicon{}).Count(&count)
	if count != 2 {
		t.Errorf("%d favicon rows, want 2", count)
	}
}

func TestClaimLetsOneAttemptThroughUntilItIsDue(t *testing.T) {
	s, _, now := newTestService(t, nil)
	ctx := context.Background()

	for i, want := range []bool{true, false} {
		if got, err := s.claim(ctx, "a.com"); err != nil || got != want {
			t.Fatalf("claim %d = %v, %v; want %v", i, got, err, want)
		}
	}
	*now = now.Add(retryAfter)
	if got, _ := s.claim(ctx, "a.com"); !got {
		t.Error("site without icon not retried after retryAfter")
	}

	if err := s.save(ctx, "a.com", &Icon{ContentType: "image/png", Data: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(retryAfter)
	if got, _ := s.claim(ctx, "a.com"); got {
		t.Error("site with icon retried before refreshAfter")
	}
	*now = now.Add(refreshAfter)
	if got, _ := s.claim(ctx, "a.com"); !got {
		t.Error("site with icon not refreshed after refreshAfter")
	}
}

func TestAnUnreachableSiteKeepsItsIconAndAMissingOneLosesIt(t *testing.T) {
	icon := []byte("old")
	s, fake, now := newTestService(t, map[string]any{"a.com": icon})
	ctx := context.Background()
	s.Want("https://a.com")
	drain(s)

	*now = now.Add(refreshAfter)
	fake.icons["a.com"] = errors.New("connection refused")
	s.Want("https://a.com")
	drain(s)
	if got := s.Lookup(ctx, "https://a.com").For("https://a.com"); got == nil || *got != hashOf(icon) {
		t.Fatalf("icon lost after a network error: %v", got)
	}

	*now = now.Add(refreshAfter)
	fake.icons["a.com"] = errNoIcon
	s.Want("https://a.com")
	drain(s)
	if got := s.Lookup(ctx, "https://a.com").For("https://a.com"); got != nil {
		t.Errorf("icon kept after the site dropped it: %s", *got)
	}
	var count int64
	s.db.Model(&models.Favicon{}).Count(&count)
	if count != 0 {
		t.Errorf("%d unused favicon rows left", count)
	}
}

func TestAChangedIconIsRemovedOnlyWhenNoOtherSiteUsesIt(t *testing.T) {
	shared, next := []byte("shared"), []byte("next")
	s, fake, now := newTestService(t, map[string]any{"a.com": shared, "b.com": shared})
	s.Want("https://a.com")
	s.Want("https://b.com")
	drain(s)

	*now = now.Add(refreshAfter)
	fake.icons["a.com"] = next
	s.Want("https://a.com")
	drain(s)

	var hashes []string
	s.db.Model(&models.Favicon{}).Order("hash").Pluck("hash", &hashes)
	if len(hashes) != 2 {
		t.Fatalf("favicons = %v, want shared and next", hashes)
	}

	*now = now.Add(refreshAfter)
	fake.icons["b.com"] = next
	s.Want("https://b.com")
	drain(s)
	hashes = nil
	s.db.Model(&models.Favicon{}).Pluck("hash", &hashes)
	if len(hashes) != 1 || hashes[0] != hashOf(next) {
		t.Errorf("favicons = %v, want only next", hashes)
	}
}

func TestWantNeverBlocksAndDoesNotQueueASiteTwice(t *testing.T) {
	s, _, _ := newTestService(t, nil)
	s.Want("https://a.com")
	s.Want("https://A.com/other")
	if len(s.jobs) != 1 {
		t.Errorf("%d jobs, want 1", len(s.jobs))
	}

	for i := range queueSize + 10 {
		s.Want("https://site" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".com")
	}
	if len(s.jobs) != queueSize {
		t.Errorf("%d jobs, want a full queue of %d", len(s.jobs), queueSize)
	}
}

func TestANilServiceDoesNothing(t *testing.T) {
	var s *Service
	s.Want("https://a.com")
	if icons := s.Lookup(context.Background(), "https://a.com"); icons.For("https://a.com") != nil {
		t.Error("nil service returned an icon")
	}
	s.Run(context.Background())
}
