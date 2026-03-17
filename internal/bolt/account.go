package bolt

import (
	"fmt"
	"strconv"
	"strings"

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

	remaining := user.TotalBoltTokenPurchases
	if remaining <= 0 {
		remaining = firstPositiveTokenValue(user.TokenAllocations)
	}
	if remaining <= 0 {
		remaining = firstPositiveTokenValue(user.ExpirableBoltTokenPurchases)
	}

	subscription := "free"
	limit := remaining

	if org := activeOrganization(user); org != nil && org.Billing != nil {
		if tier := formatTier(org.Billing.Tier); tier != "" {
			subscription = "teams-" + tier
		} else if org.Billing.Paid {
			subscription = "teams"
		}
		if inferred := inferTierLimit("teams", org.Billing.Tier); inferred > 0 {
			limit = inferred
		}
	} else if user.Membership != nil {
		if tier := formatTier(user.Membership.Tier); tier != "" {
			subscription = "pro-" + tier
		} else if user.Membership.Paid {
			subscription = "pro"
		}
		if inferred := inferTierLimit("pro", user.Membership.Tier); inferred > 0 {
			limit = inferred
		}
	}

	if remaining > limit {
		limit = remaining
	}
	if limit <= 0 && remaining > 0 {
		limit = remaining
	}

	acc.Subscription = subscription
	acc.UsageCurrent = remaining
	acc.UsageTotal = remaining
	acc.UsageLimit = limit
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

func firstPositiveTokenValue(items []TokenAllocation) float64 {
	for _, item := range items {
		for _, value := range []float64{item.Remaining, item.Balance, item.Tokens, item.Amount} {
			if value > 0 {
				return value
			}
		}
	}
	return 0
}
