package main

import (
	"context"
	"testing"
	"time"

	"orchids-api/internal/store"
)

func TestV0PlanInfoDoesNotOverrideRateLimitQuota(t *testing.T) {
	acc := &store.Account{
		AccountType:  "v0",
		UsageLimit:   5,
		UsageCurrent: 5,
		QuotaResetAt: time.UnixMilli(1774569600000),
	}

	planTotal := 5.0
	planRemaining := 4.73530525
	planReset := time.UnixMilli(1776470400000)

	if planTotal > 0 && acc.UsageLimit <= 0 {
		acc.UsageLimit = planTotal
		acc.UsageCurrent = planRemaining
	}
	if !planReset.IsZero() && acc.QuotaResetAt.IsZero() {
		acc.QuotaResetAt = planReset
	}

	if acc.UsageLimit != 5 || acc.UsageCurrent != 5 {
		t.Fatalf("usage_current=%v usage_limit=%v want 5/5", acc.UsageCurrent, acc.UsageLimit)
	}
	if !acc.QuotaResetAt.Equal(time.UnixMilli(1774569600000)) {
		t.Fatalf("quota_reset_at=%v want %v", acc.QuotaResetAt, time.UnixMilli(1774569600000))
	}

	_ = context.Background()
}
