package store

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestIncrementAccountStats_BoltKeepsRemoteQuotaCurrent(t *testing.T) {
	t.Parallel()

	mini := miniredis.RunT(t)
	s, err := New(Options{
		StoreMode:   "redis",
		RedisAddr:   mini.Addr(),
		RedisDB:     0,
		RedisPrefix: "test:",
	})
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		mini.Close()
	})

	ctx := context.Background()
	acc := &Account{
		AccountType:  "bolt",
		Enabled:      true,
		UsageCurrent: 11_000_000,
		UsageLimit:   11_000_000,
	}
	if err := s.CreateAccount(ctx, acc); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	if err := s.IncrementAccountStats(ctx, acc.ID, 2048, 1); err != nil {
		t.Fatalf("IncrementAccountStats() error = %v", err)
	}

	got, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if got.UsageCurrent != 11_000_000 {
		t.Fatalf("usage_current=%v want 11000000", got.UsageCurrent)
	}
	if got.UsageTotal != 2048 {
		t.Fatalf("usage_total=%v want 2048", got.UsageTotal)
	}
	if got.RequestCount != 1 {
		t.Fatalf("request_count=%d want 1", got.RequestCount)
	}
}
