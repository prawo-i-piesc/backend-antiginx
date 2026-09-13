package auth

import (
	"sync"
	"time"
)

type window struct {
	count    int
	resetsAt time.Time
}

type RateLimiter struct {
	mu      sync.Mutex
	windows map[string]*window
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{windows: make(map[string]*window)}
}

func (r *RateLimiter) Allow(key string, limit int, per time.Duration) (bool, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.sweepLocked(now)

	current, ok := r.windows[key]
	if !ok || now.After(current.resetsAt) {
		current = &window{resetsAt: now.Add(per)}
		r.windows[key] = current
	}

	if current.count >= limit {
		return false, time.Until(current.resetsAt)
	}

	current.count++
	return true, 0
}

func (r *RateLimiter) Reset(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.windows, key)
}

func (r *RateLimiter) sweepLocked(now time.Time) {
	for key, current := range r.windows {
		if now.After(current.resetsAt) {
			delete(r.windows, key)
		}
	}
}
