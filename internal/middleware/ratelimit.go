package middleware

import (
	"net"
	"strings"
	"sync"
	"time"
)

// RateLimiter implements a scalable token-bucket rate limiter keyed by IP.
type RateLimiter struct {
	entries     sync.Map
	maxAttempts int
	window      time.Duration
}

type limiterEntry struct {
	mu        sync.Mutex
	tokens    float64
	lastVisit time.Time
}

// NewRateLimiter creates a rate limiter that allows maxAttempts within the
// given window duration per IP address.
func NewRateLimiter(maxAttempts int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		maxAttempts: maxAttempts,
		window:      window,
	}
	go rl.cleanupLoop()
	return rl
}

// Allow reports whether the given IP is allowed to make another attempt.
func (rl *RateLimiter) Allow(ip string) bool {
	val, ok := rl.entries.Load(ip)
	if !ok {
		// New IP
		entry := &limiterEntry{
			tokens:    float64(rl.maxAttempts - 1), // Consume 1 token
			lastVisit: time.Now(),
		}
		rl.entries.Store(ip, entry)
		return true
	}

	entry := val.(*limiterEntry)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(entry.lastVisit)

	// Replenish tokens based on elapsed time
	ratePerSec := float64(rl.maxAttempts) / rl.window.Seconds()
	entry.tokens += elapsed.Seconds() * ratePerSec
	if entry.tokens > float64(rl.maxAttempts) {
		entry.tokens = float64(rl.maxAttempts)
	}

	entry.lastVisit = now

	if entry.tokens >= 1 {
		entry.tokens--
		return true
	}

	return false
}

// cleanupLoop periodically removes expired entries to prevent unbounded
// memory growth.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute) // Less frequent cleanup needed
	defer ticker.Stop()
	for range ticker.C {
		rl.cleanup()
	}
}

func (rl *RateLimiter) cleanup() {
	now := time.Now()
	// TTL is twice the window size to ensure we don't prematurely delete active entries
	ttl := rl.window * 2

	rl.entries.Range(func(key, value interface{}) bool {
		entry := value.(*limiterEntry)
		entry.mu.Lock()
		lastVisit := entry.lastVisit
		entry.mu.Unlock()

		if now.Sub(lastVisit) > ttl {
			rl.entries.Delete(key)
		}
		return true
	})
}

// ExtractIP returns the client IP from the request, checking
// X-Forwarded-For and X-Real-IP before falling back to RemoteAddr.
func ExtractIP(r_remoteAddr string, xForwardedFor string, xRealIP string) string {
	if xff := strings.TrimSpace(xForwardedFor); xff != "" {
		// Take the first IP from X-Forwarded-For.
		if idx := strings.IndexByte(xff, ','); idx >= 0 {
			xff = strings.TrimSpace(xff[:idx])
		}
		if xff != "" {
			return xff
		}
	}
	if xri := strings.TrimSpace(xRealIP); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r_remoteAddr)
	if err != nil {
		return r_remoteAddr
	}
	return host
}
