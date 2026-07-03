package server

import (
	"sync"
	"time"
)

const (
	loginMaxFails = 5
	loginWindow   = 15 * time.Minute
)

// loginLimiter is a small in-memory sliding-window limiter for login attempts,
// keyed by client IP (and username). It protects against brute-force guessing.
type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{fails: make(map[string][]time.Time)}
}

// blocked reports whether the key currently exceeds the failure threshold.
func (l *loginLimiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key)) >= loginMaxFails
}

// fail records a failed attempt for the key.
func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[key] = append(l.prune(key), time.Now())
}

// reset clears the key after a successful login.
func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

// prune drops entries outside the window (caller holds the lock).
func (l *loginLimiter) prune(key string) []time.Time {
	cutoff := time.Now().Add(-loginWindow)
	kept := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, key)
	} else {
		l.fails[key] = kept
	}
	return kept
}
