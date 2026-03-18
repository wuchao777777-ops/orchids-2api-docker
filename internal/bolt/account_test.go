package bolt

import (
	"testing"

	"orchids-api/internal/store"
)

func TestApplyRootData_AggregatesRecurringAndExtraQuota(t *testing.T) {
	t.Parallel()

	acc := &store.Account{AccountType: "bolt"}
	ApplyRootData(acc, &RootData{
		User: &RootUser{
			TotalBoltTokenPurchases: 1_000_000,
			Membership: &Membership{
				Paid: true,
				Tier: float64(1),
			},
			TokenAllocations: []TokenAllocation{
				{
					Kind:   "recurring",
					Tokens: 10_000_000,
				},
			},
		},
	})

	if acc.Subscription != "pro-1" {
		t.Fatalf("subscription=%q want pro-1", acc.Subscription)
	}
	if acc.UsageCurrent != 11_000_000 {
		t.Fatalf("usage_current=%v want 11000000", acc.UsageCurrent)
	}
	if acc.UsageLimit != 11_000_000 {
		t.Fatalf("usage_limit=%v want 11000000", acc.UsageLimit)
	}
}

func TestApplyRateLimits_ComputesRemainingLikeBoltPricing(t *testing.T) {
	t.Parallel()

	acc := &store.Account{
		AccountType:  "bolt",
		Subscription: "pro-1",
	}
	ApplyRateLimits(acc, &RateLimits{
		MaxPerMonth: 10_000_000,
		RegularTokens: &TokenBalance{
			Available: 10_000_000,
			Used:      255_061,
		},
		Purchased: &TokenBalance{
			Available: 1_000_000,
			Used:      0,
		},
		RewardTokens:  &TokenBalance{},
		SpecialTokens: &TokenBalance{},
		ReferralTokens: &ReferralTokens{
			Free: &TokenBalance{},
			Paid: &TokenBalance{},
		},
	})

	if acc.UsageCurrent != 10_744_939 {
		t.Fatalf("usage_current=%v want 10744939", acc.UsageCurrent)
	}
	if acc.UsageLimit != 11_000_000 {
		t.Fatalf("usage_limit=%v want 11000000", acc.UsageLimit)
	}
}

func TestApplyRateLimits_IncludesSpecialAndPaidReferralTokensForPro(t *testing.T) {
	t.Parallel()

	acc := &store.Account{
		AccountType:  "bolt",
		Subscription: "pro-1",
	}
	ApplyRateLimits(acc, &RateLimits{
		MaxPerMonth: 1_000_000,
		RegularTokens: &TokenBalance{
			Available: 1_000_000,
			Used:      100_000,
		},
		Purchased: &TokenBalance{
			Available: 200_000,
			Used:      50_000,
		},
		RewardTokens: &TokenBalance{
			Available: 300_000,
			Used:      20_000,
		},
		SpecialTokens: &TokenBalance{
			Available: 50_000,
			Used:      10_000,
		},
		ReferralTokens: &ReferralTokens{
			Free: &TokenBalance{
				Available: 30_000,
				Used:      5_000,
			},
			Paid: &TokenBalance{
				Available: 40_000,
				Used:      10_000,
			},
		},
	})

	if acc.UsageCurrent != 1_425_000 {
		t.Fatalf("usage_current=%v want 1425000", acc.UsageCurrent)
	}
	if acc.UsageLimit != 1_620_000 {
		t.Fatalf("usage_limit=%v want 1620000", acc.UsageLimit)
	}
}
