package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a sliding-window, in-memory rate limiter keyed by caller identifier.
// It is safe for concurrent use.
type Limiter struct {
	mu       sync.Mutex
	window   time.Duration
	max      int
	counters map[string][]time.Time
}

// New creates a Limiter that allows at most max calls per window per key.
func New(window time.Duration, max int) *Limiter {
	return &Limiter{
		window:   window,
		max:      max,
		counters: make(map[string][]time.Time),
	}
}

// Allow returns true if the caller identified by key is within the rate limit.
// It returns false when the limit is exceeded.
func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	times := l.counters[key]
	var recent []time.Time
	for _, t := range times {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= l.max {
		l.counters[key] = recent
		return false
	}

	l.counters[key] = append(recent, now)
	return true
}
