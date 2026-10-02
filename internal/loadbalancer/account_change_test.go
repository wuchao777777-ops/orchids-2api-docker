package loadbalancer

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

// TestInvalidateAccounts_MakesChangesImmediate is the acceptance rule for the
// notification chain: a change must be visible to the NEXT request, not after the
// cache TTL expires. Before this, a deleted or disabled account could still be
// selected for up to cacheTTL.
func TestInvalidateAccounts_MakesChangesImmediate(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{RedisAddr: mini.Addr(), RedisPrefix: "pool-invalidate:"})
	testutil.NoError(t, err, "store.New() error = %v")
	t.Cleanup(func() { _ = s.Close() })

	acc := &store.Account{AccountType: "workbuddy", RefreshToken: "session-a", Enabled: true, Weight: 1}
	testutil.NoError(t, s.CreateAccount(context.Background(), acc), "CreateAccount: %v")

	// A long TTL makes the point: without notification the pool would keep serving
	// the stale snapshot for the whole window.
	lb := NewWithCacheTTL(s, time.Hour)
	_, err = lb.GetNextAccountExcludingByChannelWithTrackerFilter(context.Background(), nil, "workbuddy", nil, nil)
	testutil.CheckNoError(t, err)

	// Delete the account, then notify as the change bus would.
	testutil.NoError(t, s.DeleteAccount(context.Background(), acc.ID), "DeleteAccount: %v")
	lb.AccountChanges([]int64{acc.ID})

	_, err = lb.GetNextAccountExcludingByChannelWithTrackerFilter(context.Background(), nil, "workbuddy", nil, nil)
	testutil.Error(t, err)
}

// TestInvalidateAccounts_KeepsUnrelatedAccounts guards the blast radius: only the
// changed account leaves the snapshot.
func TestInvalidateAccounts_KeepsUnrelatedAccounts(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{RedisAddr: mini.Addr(), RedisPrefix: "pool-keep:"})
	testutil.NoError(t, err, "store.New() error = %v")
	t.Cleanup(func() { _ = s.Close() })

	first := &store.Account{AccountType: "workbuddy", RefreshToken: "session-a", Enabled: true, Weight: 1}
	second := &store.Account{AccountType: "workbuddy", RefreshToken: "session-b", Enabled: true, Weight: 1}
	for _, acc := range []*store.Account{first, second} {
		testutil.NoError(t, s.CreateAccount(context.Background(), acc), "CreateAccount: %v")
	}

	lb := NewWithCacheTTL(s, time.Hour)
	_, err = lb.GetNextAccountExcludingByChannelWithTrackerFilter(context.Background(), nil, "workbuddy", nil, nil)
	testutil.CheckNoError(t, err)

	lb.AccountChanges([]int64{first.ID})

	lb.mu.RLock()
	remaining := len(lb.cachedAccounts)
	ids := make([]int64, 0, remaining)
	for _, acc := range lb.cachedAccounts {
		ids = append(ids, acc.ID)
	}
	lb.mu.RUnlock()
	testutil.Equal(t, remaining, 1)
	testutil.Equal(t, ids[0], second.ID)
}

// TestInvalidateAccounts_WithEmptySnapshotIsSafe covers the cold path.
func TestInvalidateAccounts_WithEmptySnapshotIsSafe(t *testing.T) {
	lb := NewWithCacheTTL(nil, time.Minute)
	lb.AccountChanges([]int64{1, 2, 3})
	lb.AccountChanges(nil)
	testutil.Equal(t, len(lb.cachedAccounts), 0)
}
