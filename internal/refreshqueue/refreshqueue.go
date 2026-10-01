// Package refreshqueue serialises credential refreshes per account.
//
// Before this existed the scheduler took "the next N accounts" from a global
// rotation offset, with no per-account lease. Two consequences followed: one
// account could be refreshed twice concurrently (its older snapshot winning the
// write-back and reinstating a status that had just been cleared), and an
// account that was due could sit behind an unrelated rotation for several
// cycles.
package refreshqueue

import "sync"

// defaultHub is the process-wide refresh lease set. Every refresh entrance —
// the background scheduler, the admin "check" button, a per-channel refresh —
// takes a lease here, so "one refresh per account at a time" is a property of the
// process rather than of one loop.
var defaultHub = NewHub()

// Default returns the process-wide hub.
func Default() *Hub { return defaultHub }

// WithLease runs fn while holding the account's lease. It reports false without
// running fn when another refresh of the same account is already in flight,
// which is what stops an older snapshot from overwriting a newer one.
func WithLease(accountID int64, fn func()) bool {
	if !defaultHub.TryAcquire(accountID) {
		return false
	}
	defer defaultHub.Release(accountID)
	if fn != nil {
		fn()
	}
	return true
}

// Hub tracks which accounts are currently being refreshed.
type Hub struct {
	mu       sync.Mutex
	inFlight map[int64]struct{}
}

// NewHub creates an empty refresh hub.
func NewHub() *Hub {
	return &Hub{
		inFlight: map[int64]struct{}{},
	}
}

// TryAcquire takes the per-account lease. It reports false when another refresh
// of the same account is already running, which is the signal to merge the task
// instead of starting a second one.
func (h *Hub) TryAcquire(accountID int64) bool {
	if h == nil || accountID == 0 {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, busy := h.inFlight[accountID]; busy {
		return false
	}
	h.inFlight[accountID] = struct{}{}
	return true
}

// Release frees the lease. Releasing an account that is not held is a no-op, so
// a deferred call cannot corrupt another task's lease.
func (h *Hub) Release(accountID int64) {
	if h == nil || accountID == 0 {
		return
	}
	h.mu.Lock()
	delete(h.inFlight, accountID)
	h.mu.Unlock()
}

// Len is the number of accounts currently being refreshed, for the runtime
// concurrency reporter.
func (h *Hub) Len() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.inFlight)
}
