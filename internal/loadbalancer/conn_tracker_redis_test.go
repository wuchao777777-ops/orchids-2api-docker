package loadbalancer

import (
	"context"
	"orchids-api/internal/testutil"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisConnTrackerLeaseIsAtomicSharedAndReleased(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer client.Close()

	first := NewRedisConnTracker(client, "test:")
	second := NewRedisConnTracker(client, "test:")
	testutil.False(t, !first.TryAcquire(42, 1), "first lease was rejected")
	testutil.False(t, second.TryAcquire(42, 1), "second process exceeded the shared hard limit")
	testutil.Equal(t, second.GetCount(42), 1)
	// Constructing another tracker must never erase live leases from a peer.
	third := NewRedisConnTracker(client, "test:")
	testutil.Equal(t, third.GetCount(42), 1)
	first.Release(42)
	testutil.Equal(t, second.GetCount(42), 0)
}

func TestRedisConnTrackerReclaimsExpiredAndLegacyCounters(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer client.Close()
	tracker := NewRedisConnTracker(client, "test:")
	key := tracker.key(7)

	testutil.NoError(t, client.ZAdd(context.Background(), key, redis.Z{Score: float64(time.Now().Add(-time.Minute).UnixMilli()), Member: "dead"}).Err())
	testutil.Equal(t, tracker.GetCount(7), 0)

	testutil.NoError(t, client.Set(context.Background(), key, "99", 0).Err())
	testutil.False(t, !tracker.TryAcquire(7, 1), "legacy string counter prevented lease migration")
	testutil.Equal(t, tracker.GetCount(7), 1)
	tracker.Release(7)
}

func TestRedisConnTrackerCloseReleasesOwnedLeases(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer client.Close()

	tracker := NewRedisConnTracker(client, "test:")
	peer := NewRedisConnTracker(client, "test:")
	testutil.False(t, !tracker.TryAcquire(42, 2) || !peer.TryAcquire(42, 2), "failed to acquire owned leases")
	tracker.Close()
	testutil.Equal(t, peer.GetCount(42), 1)
	peer.Release(42)
	testutil.False(t, tracker.TryAcquire(42, 1), "closed tracker acquired a new lease")
}
