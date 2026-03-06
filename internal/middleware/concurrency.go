package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"
)

// ConcurrencyLimiter limits concurrent request processing using a weighted semaphore.
// This is more efficient than channel-based semaphore for high-throughput scenarios.
type ConcurrencyLimiter struct {
	// 64-bit atomic fields must be at the top for 32-bit alignment
	activeCount   int64
	totalReqs     int64
	rejectedReqs  int64
	maxConcurrent int64
	cachedP95     int64 // Cached P95 to avoid sorting on the hot path

	sem     *semaphore.Weighted
	timeout time.Duration

	// Adaptive timeout
	adaptive      bool
	latencyWindow []int64 // Milliseconds
	windowIdx     int
	windowSize    int
	mu            sync.RWMutex
	
	lastP95Update time.Time
}

// NewConcurrencyLimiter creates a new limiter with the specified max concurrent requests and timeout.
func NewConcurrencyLimiter(maxConcurrent int, timeout time.Duration, adaptive bool) *ConcurrencyLimiter {
	if maxConcurrent <= 0 {
		maxConcurrent = 100
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &ConcurrencyLimiter{
		sem:           semaphore.NewWeighted(int64(maxConcurrent)),
		maxConcurrent: int64(maxConcurrent),
		timeout:       timeout,
		adaptive:      adaptive,
		latencyWindow: make([]int64, 100), // Keep last 100 requests
		windowSize:    100,
	}
}

func (cl *ConcurrencyLimiter) Limit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&cl.totalReqs, 1)

		// Calculate wait timeout
		waitTimeout := 60 * time.Second
		if cl.adaptive {
			p95 := atomic.LoadInt64(&cl.cachedP95)
			if p95 > 0 {
				// Allow 1.5x P95 wait time, clamped
				calcWait := time.Duration(float64(p95)*1.5) * time.Millisecond
				if calcWait < 5*time.Second {
					waitTimeout = 5 * time.Second
				} else if calcWait > 60*time.Second {
					waitTimeout = 60 * time.Second
				} else {
					waitTimeout = calcWait
				}
			}
		}

		if cl.timeout < waitTimeout {
			waitTimeout = cl.timeout
		}

		waitCtx, cancelWait := context.WithTimeout(r.Context(), waitTimeout)
		defer cancelWait()

		// Try to acquire semaphore with wait timeout
		acquireStart := time.Now()
		if err := cl.sem.Acquire(waitCtx, 1); err != nil {
			atomic.AddInt64(&cl.rejectedReqs, 1)
			slog.Warn("Concurrency limit: Wait timeout", "duration", time.Since(acquireStart), "total_rejected", atomic.LoadInt64(&cl.rejectedReqs), "wait_timeout", waitTimeout)
			http.Error(w, "Request timed out while waiting for a worker slot or server busy", http.StatusServiceUnavailable)
			return
		}

		slog.Debug("Concurrency limit: Slot acquired", "wait_duration", time.Since(acquireStart), "active", atomic.LoadInt64(&cl.activeCount)+1)

		atomic.AddInt64(&cl.activeCount, 1)
		reqStart := time.Now()

		defer func() {
			cl.sem.Release(1)
			atomic.AddInt64(&cl.activeCount, -1)

			duration := time.Since(reqStart)
			if cl.adaptive {
				cl.UpdateStats(duration)
			}
			slog.Debug("Concurrency limit: Slot released", "active", atomic.LoadInt64(&cl.activeCount), "duration", duration)
		}()

		// Use the full concurrency timeout for actual request execution
		execCtx, cancelExec := context.WithTimeout(r.Context(), cl.timeout)
		defer cancelExec()

		slog.Debug("Concurrency limit: Serving request", "path", r.URL.Path, "timeout", cl.timeout)
		next.ServeHTTP(w, r.WithContext(execCtx))
	}
}

// UpdateStats records request latency for adaptive timeout
func (cl *ConcurrencyLimiter) UpdateStats(d time.Duration) {
	ms := d.Milliseconds()
	cl.mu.Lock()
	cl.latencyWindow[cl.windowIdx] = ms
	cl.windowIdx = (cl.windowIdx + 1) % cl.windowSize
	
	// Update cached P95 periodically (e.g. at most once per second or every 10 requests) to avoid hot path bottleneck
	now := time.Now()
	shouldRecalc := now.Sub(cl.lastP95Update) > time.Second
	cl.mu.Unlock()

	if shouldRecalc {
		cl.recalcP95()
	}
}

// recalcP95 recalculates and caches the 95th percentile latency
func (cl *ConcurrencyLimiter) recalcP95() {
	cl.mu.Lock()
	now := time.Now()
	
	// Double-checked locking to avoid concurrent recalculations
	if now.Sub(cl.lastP95Update) <= time.Second {
		cl.mu.Unlock()
		return
	}
	cl.lastP95Update = now

	// Make a copy of the window to avoid holding the lock while sorting
	localWindow := make([]int64, cl.windowSize)
	copy(localWindow, cl.latencyWindow)
	cl.mu.Unlock()

	// Filter out zeros (uninitialized slots) to avoid skewing the result
	valid := make([]int64, 0, len(localWindow))
	for _, v := range localWindow {
		if v > 0 {
			valid = append(valid, v)
		}
	}
	if len(valid) < 10 {
		return // Not enough data
	}

	sort.Slice(valid, func(i, j int) bool { return valid[i] < valid[j] })

	idx := int(float64(len(valid)) * 0.95)
	if idx >= len(valid) {
		idx = len(valid) - 1
	}
	
	atomic.StoreInt64(&cl.cachedP95, valid[idx])
}

// GetP95 returns the cached 95th percentile latency in milliseconds
func (cl *ConcurrencyLimiter) GetP95() int64 {
	return atomic.LoadInt64(&cl.cachedP95)
}
