package store

import (
	"context"
	"orchids-api/internal/testutil"
	"testing"
	"time"
)

func TestIncrementAccountStats_PassthroughAccountKeepsRemoteQuotaCurrent(t *testing.T) {
	t.Parallel()

	s, _ := newTestRedisStore(t, "test:")

	ctx := context.Background()
	acc := &Account{
		AccountType:  "workbuddy",
		Enabled:      true,
		UsageCurrent: 11_000_000,
		UsageLimit:   11_000_000,
	}
	testutil.NoError(t, s.CreateAccount(ctx, acc), "CreateAccount() error = %v")

	testutil.NoError(t, s.IncrementAccountStats(ctx, acc.ID, 2048, 1), "IncrementAccountStats() error = %v")

	got, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	testutil.Equal(t, got.UsageCurrent, 11_000_000)
	testutil.Equal(t, got.UsageTotal, 2048)
	testutil.Equal(t, got.RequestCount, 1)
}

func TestIncrementAccountStats_ZeroUsageStillCountsRequest(t *testing.T) {
	t.Parallel()

	s, _ := newTestRedisStore(t, "test:")

	ctx := context.Background()
	acc := &Account{
		AccountType:  "workbuddy",
		Enabled:      true,
		UsageCurrent: 11_000_000,
		UsageTotal:   123,
		UsageLimit:   11_000_000,
	}
	testutil.NoError(t, s.CreateAccount(ctx, acc), "CreateAccount() error = %v")

	testutil.NoError(t, s.IncrementAccountStats(ctx, acc.ID, 0, 1), "IncrementAccountStats() error = %v")

	got, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	testutil.Equal(t, got.UsageCurrent, 11_000_000)
	testutil.Equal(t, got.UsageTotal, 123)
	testutil.Equal(t, got.RequestCount, 1)
}

func TestUpdateAccount_DoesNotOverwriteAtomicUsageCounters(t *testing.T) {
	t.Parallel()

	s, _ := newTestRedisStore(t, "test:")

	ctx := context.Background()
	acc := &Account{AccountType: "qoder", Enabled: true}
	testutil.NoError(t, s.CreateAccount(ctx, acc), "CreateAccount() error = %v")
	stale, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	testutil.NoError(t, s.IncrementAccountStats(ctx, acc.ID, 100, 1), "IncrementAccountStats() error = %v")
	stale.StatusCode = "429"
	testutil.NoError(t, s.UpdateAccount(ctx, stale), "UpdateAccount() error = %v")
	got, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if got.UsageTotal != 100 || got.TokensToday != 100 || got.TokensDate == "" {
		t.Fatalf("atomic counters overwritten by stale update: total=%v today=%v date=%q", got.UsageTotal, got.TokensToday, got.TokensDate)
	}
}

func TestIncrementAccountStats_WorkBuddyKeepsRemoteRemainingCredits(t *testing.T) {
	t.Parallel()

	s, _ := newTestRedisStore(t, "test:")

	ctx := context.Background()
	acc := &Account{AccountType: "workbuddy", Enabled: true, UsageCurrent: 0, UsageLimit: 1000}
	testutil.NoError(t, s.CreateAccount(ctx, acc), "CreateAccount() error = %v")
	testutil.NoError(t, s.IncrementAccountStats(ctx, acc.ID, 500, 1), "IncrementAccountStats() error = %v")
	got, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if got.UsageCurrent != 0 || got.UsageTotal != 500 || got.TokensToday != 500 {
		t.Fatalf("workbuddy counters = current %v total %v today %v, want 0/500/500", got.UsageCurrent, got.UsageTotal, got.TokensToday)
	}
}

func TestIncrementAccountStatsOperationIsDurablyIdempotent(t *testing.T) {
	s, _ := newTestRedisStore(t, "stats-idempotent:")
	ctx := context.Background()
	acc := &Account{AccountType: "qoder", Enabled: true}
	testutil.NoError(t, s.CreateAccount(ctx, acc))
	completed := time.Date(2026, 3, 5, 23, 59, 59, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if err := s.IncrementAccountStatsOperation(ctx, acc.ID, 42, 1, "request-123", completed); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	got, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UsageTotal != 42 || got.RequestCount != 1 || got.TokensToday != 42 || got.TokensDate != "2026-03-05" {
		t.Fatalf("duplicate operation applied twice: total=%v requests=%d today=%v date=%q", got.UsageTotal, got.RequestCount, got.TokensToday, got.TokensDate)
	}
}

func TestIncrementAccountStatsOperationUsesCompletionUTCDateAcrossMidnight(t *testing.T) {
	s, _ := newTestRedisStore(t, "stats-midnight:")
	ctx := context.Background()
	acc := &Account{AccountType: "qoder", Enabled: true}
	testutil.NoError(t, s.CreateAccount(ctx, acc))
	beforeMidnight := time.Date(2026, 3, 5, 23, 59, 59, 0, time.UTC)
	afterMidnight := beforeMidnight.Add(2 * time.Second)
	testutil.NoError(t, s.IncrementAccountStatsOperation(ctx, acc.ID, 10, 1, "newer", afterMidnight))
	testutil.NoError(t, s.IncrementAccountStatsOperation(ctx, acc.ID, 20, 1, "delayed-older", beforeMidnight))
	got, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UsageTotal != 30 || got.RequestCount != 2 {
		t.Fatalf("lifetime totals lost: total=%v requests=%d", got.UsageTotal, got.RequestCount)
	}
	if got.TokensDate != "2026-03-06" || got.TokensToday != 10 {
		t.Fatalf("delayed prior-day completion rewound current UTC bucket: today=%v date=%q", got.TokensToday, got.TokensDate)
	}
}
