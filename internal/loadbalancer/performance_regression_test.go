package loadbalancer

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"orchids-api/internal/store"
	"testing"
	"time"
)

type boundedCountTracker struct {
	fixedConnTracker
	maxBatch int
}

func (t *boundedCountTracker) GetCounts(ids []int64) map[int64]int64 {
	if len(ids) > t.maxBatch {
		t.maxBatch = len(ids)
	}
	return t.fixedConnTracker.GetCounts(ids)
}
func TestLargeSelectionUsesBoundedCountsAndFindsCapacity(t *testing.T) {
	accounts := make([]*store.Account, 4096)
	counts := map[int64]int64{}
	for i := range accounts {
		accounts[i] = &store.Account{ID: int64(i + 1), MaxConcurrent: 1}
		counts[int64(i+1)] = 1
	}
	counts[4096] = 0
	tracker := &boundedCountTracker{fixedConnTracker: fixedConnTracker{counts: counts}}
	lb := &LoadBalancer{}
	picked := lb.selectAccountWithTracker(accounts, tracker)
	if picked == nil || picked.ID != 4096 || tracker.maxBatch > accountScanWindow {
		t.Fatalf("picked=%v batch=%d", picked, tracker.maxBatch)
	}
}
func TestBatchRenewalDoesNotResurrectReleasedLease(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer client.Close()
	tracker := NewRedisConnTracker(client, "perf:")
	defer tracker.Close()
	if !tracker.TryAcquire(1, 1) {
		t.Fatal("acquire")
	}
	tracker.mu.Lock()
	id := tracker.held[1][0].id
	tracker.mu.Unlock()
	before, err := client.ZScore(context.Background(), tracker.key(1), id).Result()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	tracker.renewBatch()
	after, err := client.ZScore(context.Background(), tracker.key(1), id).Result()
	if err != nil || after <= before {
		t.Fatalf("renew: %v %v", after, err)
	}
	tracker.Release(1)
	tracker.renewBatch()
	if tracker.GetCount(1) != 0 {
		t.Fatal("released lease resurrected")
	}
}
func TestCachedCountsNeverOverrideAtomicLimit(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer client.Close()
	a := NewRedisConnTracker(client, "limit:")
	defer a.Close()
	b := NewRedisConnTracker(client, "limit:")
	defer b.Close()
	a.GetCounts([]int64{1})
	if !b.TryAcquire(1, 1) {
		t.Fatal("first acquire")
	}
	if a.TryAcquire(1, 1) {
		t.Fatal("cached zero bypassed atomic admission")
	}
}
