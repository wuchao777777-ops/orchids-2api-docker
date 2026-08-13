package puter

import (
	"context"
	"crypto/sha256"
	"net/url"
	"strings"
	"sync"
	"time"
)

const puterRequestInterval = time.Second

var puterRequestPacer = struct {
	sync.Mutex
	next map[[32]byte]time.Time
}{next: make(map[[32]byte]time.Time)}

// waitForPuterRequestSlot smooths bursts sent through the undocumented driver
// endpoint. The limit is per auth token, so independent Puter accounts can
// still make progress concurrently.
func waitForPuterRequestSlot(ctx context.Context, rawURL, authToken string) error {
	endpoint, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(endpoint.Hostname(), "api.puter.com") {
		return nil
	}

	key := sha256.Sum256([]byte(strings.TrimSpace(authToken)))
	now := time.Now()
	puterRequestPacer.Lock()
	next := puterRequestPacer.next[key]
	if next.Before(now) {
		next = now
	}
	puterRequestPacer.next[key] = next.Add(puterRequestInterval)
	if len(puterRequestPacer.next) > 256 {
		cutoff := now.Add(-time.Minute)
		for candidate, candidateNext := range puterRequestPacer.next {
			if candidateNext.Before(cutoff) {
				delete(puterRequestPacer.next, candidate)
			}
		}
	}
	puterRequestPacer.Unlock()

	delay := time.Until(next)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
