package handler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupRedisDedupStore(t *testing.T) (*RedisDedupStore, *miniredis.Miniredis) {
	t.Helper()
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	store := NewRedisDedupStore(client, "test:", 2*time.Second)
	return store, s
}

func TestRedisDedupStoreRegisterDuplicate(t *testing.T) {
	store, _ := setupRedisDedupStore(t)
	ctx := context.Background()

	// First request: not duplicate
	dup, _ := store.Register(ctx, "hash1")
	if dup {
		t.Fatal("first request should not be duplicate")
	}

	// Second request within window: duplicate
	dup, inFlight := store.Register(ctx, "hash1")
	if !dup {
		t.Fatal("second request should be duplicate")
	}
	if !inFlight {
		t.Fatal("should report in-flight request")
	}
}

func TestRedisDedupStoreFinish(t *testing.T) {
	store, mr := setupRedisDedupStore(t)
	ctx := context.Background()

	store.Register(ctx, "hash2")
	store.Finish(ctx, "hash2")

	// Fast-forward past the key's 10s TTL so it expires entirely in Redis.
	// (miniredis FastForward only affects key expiry, not Go's time.Now used in Lua ARGV)
	mr.FastForward(11 * time.Second)
	dup, _ := store.Register(ctx, "hash2")
	if dup {
		t.Fatal("should not be duplicate after key expiry")
	}
}

func TestRedisDedupStoreAutoExpiry(t *testing.T) {
	store, mr := setupRedisDedupStore(t)
	ctx := context.Background()

	store.Register(ctx, "hash3")
	store.Finish(ctx, "hash3")

	// Fast-forward past 10s TTL
	mr.FastForward(11 * time.Second)

	dup, _ := store.Register(ctx, "hash3")
	if dup {
		t.Fatal("should not be duplicate after key expiry")
	}
}

// --- Memory dedup tests ---

func TestMemoryDedupStoreRegisterDuplicate(t *testing.T) {
	store := NewMemoryDedupStore(2*time.Second, 10*time.Second)
	ctx := context.Background()

	dup, _ := store.Register(ctx, "hash1")
	if dup {
		t.Fatal("first request should not be duplicate")
	}

	dup, inFlight := store.Register(ctx, "hash1")
	if !dup {
		t.Fatal("second request should be duplicate")
	}
	if !inFlight {
		t.Fatal("should report in-flight")
	}
}

func TestMemoryDedupStoreFinish(t *testing.T) {
	store := NewMemoryDedupStore(100*time.Millisecond, 10*time.Second)
	ctx := context.Background()

	store.Register(ctx, "hash2")
	store.Finish(ctx, "hash2")

	time.Sleep(150 * time.Millisecond)

	dup, _ := store.Register(ctx, "hash2")
	if dup {
		t.Fatal("should not be duplicate after window")
	}
}

func TestMemoryDedupStoreConcurrent(t *testing.T) {
	store := NewMemoryDedupStore(2*time.Second, 10*time.Second)
	ctx := context.Background()

	var wg sync.WaitGroup
	dupCount := 0
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dup, _ := store.Register(ctx, "concurrent_hash")
			if dup {
				mu.Lock()
				dupCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// First one should succeed, rest should be duplicates
	if dupCount != 9 {
		t.Fatalf("expected 9 duplicates, got %d", dupCount)
	}
}
