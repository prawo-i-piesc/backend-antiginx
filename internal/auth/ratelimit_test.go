package auth

import (
	"sync"
	"testing"
	"time"
)

func TestRateLimiterAllowsUpToLimit(t *testing.T) {
	limiter := NewRateLimiter()

	for i := 1; i <= 3; i++ {
		allowed, _ := limiter.Allow("adres", 3, time.Hour)
		if !allowed {
			t.Fatalf("próba %d odrzucona przed limitem", i)
		}
	}

	allowed, retryAfter := limiter.Allow("adres", 3, time.Hour)
	if allowed {
		t.Fatal("czwarta próba przeszła mimo limitu trzech")
	}
	if retryAfter <= 0 || retryAfter > time.Hour {
		t.Errorf("retryAfter = %v", retryAfter)
	}
}

func TestRateLimiterKeysAreIndependent(t *testing.T) {
	limiter := NewRateLimiter()

	for i := 0; i < 3; i++ {
		limiter.Allow("jeden", 3, time.Hour)
	}

	if allowed, _ := limiter.Allow("drugi", 3, time.Hour); !allowed {
		t.Error("wyczerpanie limitu jednego klucza zablokowało inny")
	}
}

func TestRateLimiterWindowExpires(t *testing.T) {
	limiter := NewRateLimiter()

	if allowed, _ := limiter.Allow("adres", 1, time.Millisecond); !allowed {
		t.Fatal("pierwsza próba odrzucona")
	}
	if allowed, _ := limiter.Allow("adres", 1, time.Millisecond); allowed {
		t.Fatal("druga próba przeszła w tym samym oknie")
	}

	time.Sleep(5 * time.Millisecond)

	if allowed, _ := limiter.Allow("adres", 1, time.Millisecond); !allowed {
		t.Error("po wygaśnięciu okna limit się nie zresetował")
	}
}

// Udany reset hasła zwalnia limit adresu, żeby kolejna prośba nie odbiła się
// od licznika nabitego przy poprzedniej, już zużytej próbie.
func TestRateLimiterReset(t *testing.T) {
	limiter := NewRateLimiter()

	for i := 0; i < 3; i++ {
		limiter.Allow("adres", 3, time.Hour)
	}
	limiter.Reset("adres")

	if allowed, _ := limiter.Allow("adres", 3, time.Hour); !allowed {
		t.Error("Reset nie wyzerował licznika")
	}
}

func TestRateLimiterIsSafeForConcurrentUse(t *testing.T) {
	limiter := NewRateLimiter()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limiter.Allow("adres", 10, time.Hour)
			limiter.Allow("inny", 10, time.Hour)
		}()
	}
	wg.Wait()
}

func TestRateLimiterCountsExactly(t *testing.T) {
	limiter := NewRateLimiter()

	var (
		mu      sync.Mutex
		granted int
		wg      sync.WaitGroup
	)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if allowed, _ := limiter.Allow("adres", 7, time.Hour); allowed {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if granted != 7 {
		t.Errorf("przepuszczono %d żądań, limit wynosił 7", granted)
	}
}
