package bolt

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"orchids-api/internal/store"
)

var proTierTokens = map[int]float64{
	1:  10_000_000,
	2:  26_000_000,
	3:  55_000_000,
	4:  120_000_000,
	5:  180_000_000,
	6:  240_000_000,
	7:  300_000_000,
	8:  360_000_000,
	9:  420_000_000,
	10: 480_000_000,
	11: 540_000_000,
	12: 600_000_000,
	13: 900_000_000,
	14: 1_200_000_000,
}

var teamsTierTokens = map[int]float64{
	1:  10_000_000,
	2:  26_000_000,
	3:  55_000_000,
	4:  55_000_000,
	5:  180_000_000,
	6:  240_000_000,
	7:  300_000_000,
	8:  360_000_000,
	9:  420_000_000,
	10: 480_000_000,
	11: 540_000_000,
	12: 600_000_000,
	13: 900_000_000,
	14: 1_200_000_000,
}

func ApplyRootData(acc *store.Account, data *RootData) {
	if acc == nil || data == nil || data.User == nil {
		return
	}

	user := data.User
	if user.ID != "" {
		acc.UserID = user.ID
	}
	if user.Email != "" {
		acc.Email = user.Email
	}
	if strings.TrimSpace(acc.Token) == "" && strings.TrimSpace(data.Token) != "" {
		acc.Token = strings.TrimSpace(data.Token)
	}

	subscription := "free"
	inferredLimit := 0.0

	if org := activeOrganization(user); org != nil && org.Billing != nil {
		if tier := formatTier(org.Billing.Tier); tier != "" {
			subscription = "teams-" + tier
		} else if org.Billing.Paid {
			subscription = "teams"
		}
		if inferred := inferTierLimit("teams", org.Billing.Tier); inferred > 0 {
			inferredLimit = inferred
		}
	} else if user.Membership != nil {
		if tier := formatTier(user.Membership.Tier); tier != "" {
			subscription = "pro-" + tier
		} else if user.Membership.Paid {
			subscription = "pro"
		}
		if inferred := inferTierLimit("pro", user.Membership.Tier); inferred > 0 {
			inferredLimit = inferred
		}
	}

	remaining, limit := deriveQuota(user, inferredLimit)

	acc.Subscription = subscription
	acc.UsageCurrent = remaining
	acc.UsageTotal = remaining
	acc.UsageLimit = limit
}

func ApplyRateLimits(acc *store.Account, limits *RateLimits) {
	if acc == nil || limits == nil {
		return
	}

	remaining, limit := deriveRemainingQuota(limits, acc.Subscription)
	if limit <= 0 {
		limit = maxPositive(limits.MaxPerMonth, remaining)
	}
	if remaining > limit {
		limit = remaining
	}

	acc.UsageCurrent = remaining
	acc.UsageTotal = remaining
	acc.UsageLimit = limit

	if limits.BillingPeriod != nil && limits.BillingPeriod.To > 0 {
		acc.QuotaResetAt = time.UnixMilli(limits.BillingPeriod.To)
	}
}

func deriveQuota(user *RootUser, inferredLimit float64) (float64, float64) {
	if user == nil {
		return 0, 0
	}

	recurringRemaining, recurringTotal := sumTokenAllocations(user.TokenAllocations, true)
	oneOffRemaining, oneOffTotal := sumTokenAllocations(user.TokenAllocations, false)
	expirableRemaining, expirableTotal := sumAllocationValues(user.ExpirableBoltTokenPurchases)

	purchaseRemaining := maxPositive(user.TotalBoltTokenPurchases, expirableRemaining)
	purchaseTotal := maxPositive(user.TotalBoltTokenPurchases, expirableTotal)
	extraRemaining := purchaseRemaining + oneOffRemaining
	extraTotal := purchaseTotal + oneOffTotal

	remaining := 0.0
	limit := 0.0
	if recurringRemaining > 0 || recurringTotal > 0 {
		remaining = recurringRemaining + extraRemaining
		limit = maxPositive(recurringTotal, inferredLimit) + extraTotal
	} else {
		remaining = maxPositive(user.TotalBoltTokenPurchases, oneOffRemaining, expirableRemaining)
		limit = maxPositive(inferredLimit, purchaseTotal, oneOffTotal)
	}

	if remaining > limit {
		limit = remaining
	}
	if limit <= 0 && remaining > 0 {
		limit = remaining
	}
	return remaining, limit
}

func deriveRemainingQuota(limits *RateLimits, subscription string) (float64, float64) {
	if limits == nil {
		return 0, 0
	}

	plan := subscriptionPlan(subscription)
	regular := regularTokenBalance(limits)
	referrals := referralTokenBalance(limits, plan)
	reward := includedRewardBalance(limits, plan)
	purchased := includedPurchasedBalance(limits, plan)
	special := limits.SpecialTokens

	remaining := remainingBalance(regular) +
		remainingBalance(referrals) +
		remainingBalance(reward) +
		remainingBalance(purchased) +
		remainingBalance(special)

	limit := totalBalance(regular) +
		totalBalance(referrals) +
		totalBalance(reward) +
		totalBalance(purchased) +
		totalBalance(special)

	if limit <= 0 {
		limit = maxPositive(limits.MaxPerMonth, remaining)
	}
	return remaining, limit
}

func activeOrganization(user *RootUser) *Organization {
	if user == nil || user.ActiveOrganizationID == 0 {
		return nil
	}
	for i := range user.Organizations {
		if user.Organizations[i].ID == user.ActiveOrganizationID {
			return &user.Organizations[i]
		}
	}
	return nil
}

func inferTierLimit(plan string, tier interface{}) float64 {
	tierNumber, ok := tierAsInt(tier)
	if !ok {
		return 0
	}
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "teams":
		return teamsTierTokens[tierNumber]
	default:
		return proTierTokens[tierNumber]
	}
}

func tierAsInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return n, err == nil
	default:
		text := formatTier(value)
		if text == "" {
			return 0, false
		}
		n, err := strconv.Atoi(text)
		return n, err == nil
	}
}

func formatTier(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.Itoa(int(v))
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func sumTokenAllocations(items []TokenAllocation, recurring bool) (float64, float64) {
	remaining := 0.0
	total := 0.0
	for _, item := range items {
		isRecurring := strings.EqualFold(strings.TrimSpace(item.Kind), "recurring")
		if isRecurring != recurring {
			continue
		}
		itemRemaining, itemTotal := quotaValue(item)
		remaining += itemRemaining
		total += itemTotal
	}
	return remaining, total
}

func sumAllocationValues(items []TokenAllocation) (float64, float64) {
	remaining := 0.0
	total := 0.0
	for _, item := range items {
		itemRemaining, itemTotal := quotaValue(item)
		remaining += itemRemaining
		total += itemTotal
	}
	return remaining, total
}

func quotaValue(item TokenAllocation) (float64, float64) {
	remaining := maxPositive(item.Remaining, item.Balance, item.Tokens, item.Amount)
	total := maxPositive(item.Amount, item.Tokens, item.Remaining, item.Balance)
	if total < remaining {
		total = remaining
	}
	return remaining, total
}

func subscriptionPlan(subscription string) string {
	subscription = strings.ToLower(strings.TrimSpace(subscription))
	if subscription == "" {
		return ""
	}
	if idx := strings.Index(subscription, "-"); idx > 0 {
		return strings.TrimSpace(subscription[:idx])
	}
	return subscription
}

func regularTokenBalance(limits *RateLimits) *TokenBalance {
	if limits == nil {
		return nil
	}
	if limits.RegularTokens != nil {
		return limits.RegularTokens
	}
	return &TokenBalance{
		Available: limits.MaxPerMonth,
		Used:      limits.TotalThisMonth,
	}
}

func referralTokenBalance(limits *RateLimits, plan string) *TokenBalance {
	if limits == nil || limits.ReferralTokens == nil {
		return nil
	}
	balance := TokenBalance{}
	if limits.ReferralTokens.Free != nil {
		balance.Available += limits.ReferralTokens.Free.Available
		balance.Used += limits.ReferralTokens.Free.Used
	}
	if limits.ReferralTokens.Paid != nil && strings.EqualFold(plan, "pro") {
		balance.Available += limits.ReferralTokens.Paid.Available
		balance.Used += limits.ReferralTokens.Paid.Used
	}
	if balance.Available <= 0 && balance.Used <= 0 {
		return nil
	}
	return &balance
}

func includedRewardBalance(limits *RateLimits, plan string) *TokenBalance {
	if limits == nil || limits.RewardTokens == nil {
		return nil
	}
	if strings.EqualFold(plan, "personal") {
		return nil
	}
	return limits.RewardTokens
}

func includedPurchasedBalance(limits *RateLimits, plan string) *TokenBalance {
	if limits == nil || limits.Purchased == nil {
		return nil
	}
	if strings.EqualFold(plan, "personal") {
		return nil
	}
	return limits.Purchased
}

func remainingBalance(balance *TokenBalance) float64 {
	if balance == nil {
		return 0
	}
	return maxPositive(balance.Available - minPositive(balance.Used, balance.Available))
}

func totalBalance(balance *TokenBalance) float64 {
	if balance == nil {
		return 0
	}
	return maxPositive(balance.Available)
}

func minPositive(values ...float64) float64 {
	if len(values) == 0 {
		return 0
	}
	minValue := values[0]
	for _, value := range values[1:] {
		if value < minValue {
			minValue = value
		}
	}
	return minValue
}

func maxPositive(values ...float64) float64 {
	maxValue := 0.0
	for _, value := range values {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}
