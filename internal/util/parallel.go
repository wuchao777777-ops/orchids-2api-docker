// Package util provides general-purpose helpers.
package util

import (
	"context"
	"runtime"
	"sync"
	"time"
)

// ParallelFor runs n tasks concurrently, each receiving its index in [0, n).
// Concurrency is derived from the CPU count; small batches run serially to avoid
// the goroutine overhead.
func ParallelFor(n int, fn func(int)) {
	if n <= 0 {
		return
	}

	// Concurrency threshold: below this count running serially is cheaper.
	const parallelThreshold = 8

	if n < parallelThreshold {
		// Handle small batches serially.
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}

	workers := min(runtime.GOMAXPROCS(0), n)

	var wg sync.WaitGroup
	jobs := make(chan int, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				func() {
					defer func() {
						if r := recover(); r != nil {
							// Prevent crash from panic in worker
						}
					}()
					fn(idx)
				}()
			}
		}()
	}

	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}

// SleepWithContext is a cancellable sleep; false means the context was cancelled.
func SleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
