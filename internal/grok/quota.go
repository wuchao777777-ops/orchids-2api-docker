package grok

import (
	"net/http"
	"strings"
	"time"

	"orchids-api/internal/store"
)

// ApplyWebQuotaInfo persists the independent Web SSO auto/fast windows and
// keeps the legacy aggregate Usage* fields useful to the existing admin UI.
// A partial response is valid: one upstream mode can be temporarily absent.
func ApplyWebQuotaInfo(acc *store.Account, windows map[string]*RateLimitInfo) bool {
	if acc == nil || len(windows) == 0 {
		return false
	}
	snapshot := store.GrokWebQuotaSnapshot{SyncedAt: time.Now().UTC(), Source: "grok_web_rate_limits"}
	if info := windows["auto"]; info != nil {
		snapshot.Auto = quotaWindowFromRateLimitInfo(info)
	}
	if info := windows["fast"]; info != nil {
		snapshot.Fast = quotaWindowFromRateLimitInfo(info)
	}
	changed := acc.GrokWebQuota != snapshot
	acc.GrokWebQuota = snapshot

	// Prefer auto, then fast, for compatibility fields used by older clients.
	var preferred *RateLimitInfo
	if windows["auto"] != nil {
		preferred = windows["auto"]
	} else {
		preferred = windows["fast"]
	}
	if preferred != nil {
		if ApplyQuotaInfo(acc, preferred) {
			changed = true
		}
	}
	return changed
}

func quotaWindowFromRateLimitInfo(info *RateLimitInfo) store.GrokQuotaWindow {
	if info == nil {
		return store.GrokQuotaWindow{}
	}
	window := store.GrokQuotaWindow{
		Limit:        float64(info.Limit),
		Remaining:    float64(info.Remaining),
		HasLimit:     info.HasLimit,
		HasRemaining: info.HasRemaining,
		ResetAt:      info.ResetAt,
	}
	if info.HasLimit && info.Limit > 0 && info.HasRemaining {
		used := info.Limit - info.Remaining
		if used < 0 {
			used = 0
		}
		window.UsagePercent = float64(used) * 100 / float64(info.Limit)
		window.HasUsage = true
	}
	return window
}

const (
	basicDefaultQuota float64 = 30
	liteDefaultQuota  float64 = 70
	superDefaultQuota float64 = 140
	heavyDefaultQuota float64 = 400
)

func InferQuotaLimit(acc *store.Account) float64 {
	if acc == nil {
		return basicDefaultQuota
	}
	if acc.UsageLimit > 0 {
		return acc.UsageLimit
	}
	sub := strings.ToLower(strings.TrimSpace(acc.Subscription))
	if strings.Contains(sub, "heavy") {
		return heavyDefaultQuota
	}
	if strings.Contains(sub, "super") || strings.Contains(sub, "pro") {
		return superDefaultQuota
	}
	if strings.Contains(sub, "lite") {
		return liteDefaultQuota
	}
	return basicDefaultQuota
}

func inferSubscriptionFromRateLimitInfo(info *RateLimitInfo) string {
	if info == nil || !info.HasLimit {
		return ""
	}
	switch limit := info.Limit; {
	case limit >= 150:
		return "heavy"
	case limit == 50 || limit == 140:
		return "super"
	case limit == 25 || limit == 70 || limit == 12:
		return "lite"
	case limit == 30 || limit == 20 || limit == 8 || limit == 7:
		return "basic"
	default:
		return ""
	}
}

func ApplyQuotaInfo(acc *store.Account, info *RateLimitInfo) bool {
	if acc == nil || info == nil {
		return false
	}

	changed := false
	if sub := inferSubscriptionFromRateLimitInfo(info); sub != "" && acc.Subscription != sub {
		acc.Subscription = sub
		changed = true
	}
	if info.HasRemaining {
		limit := InferQuotaLimit(acc)
		if info.HasLimit && info.Limit > 0 {
			limit = float64(info.Limit)
		}
		remaining := float64(info.Remaining)
		if remaining < 0 {
			remaining = 0
		}
		if limit <= 0 {
			limit = basicDefaultQuota
		}
		if remaining > limit {
			limit = remaining
		}
		if acc.UsageLimit != limit {
			acc.UsageLimit = limit
			changed = true
		}
		if acc.UsageCurrent != remaining {
			acc.UsageCurrent = remaining
			changed = true
		}
	} else if info.HasLimit && info.Limit > 0 && acc.UsageLimit <= 0 {
		acc.UsageLimit = float64(info.Limit)
		changed = true
	}

	if !info.ResetAt.IsZero() && !acc.QuotaResetAt.Equal(info.ResetAt) {
		acc.QuotaResetAt = info.ResetAt
		changed = true
	}
	return changed
}

// ApplyBuildRateLimits persists passive Build response headers separately from
// subscription Billing. These values often describe a minute-scale request or
// token bucket (such as 8300 tokens), never a remaining paid-plan balance.
func ApplyBuildRateLimits(acc *store.Account, headers http.Header) bool {
	if acc == nil || headers == nil {
		return false
	}
	requests := parseBuildRateLimitWindow(headers, "requests")
	tokens := parseBuildRateLimitWindow(headers, "tokens")
	if !requests.HasLimit && !requests.HasRemaining && requests.ResetAt.IsZero() &&
		!tokens.HasLimit && !tokens.HasRemaining && tokens.ResetAt.IsZero() {
		return false
	}
	acc.GrokRateLimits = store.GrokRateLimitSnapshot{
		Requests:   requests,
		Tokens:     tokens,
		ObservedAt: time.Now().UTC(),
	}
	return true
}

func parseBuildRateLimitWindow(headers http.Header, dimension string) store.GrokQuotaWindow {
	limit := firstHeaderValue(headers,
		"x-ratelimit-limit-"+dimension,
		"x-rate-limit-limit-"+dimension,
	)
	remaining := firstHeaderValue(headers,
		"x-ratelimit-remaining-"+dimension,
		"x-rate-limit-remaining-"+dimension,
	)
	reset := firstHeaderValue(headers,
		"x-ratelimit-reset-"+dimension,
		"x-rate-limit-reset-"+dimension,
	)
	window := store.GrokQuotaWindow{}
	if value, ok := parseRateLimitValue(limit); ok {
		window.Limit = float64(value)
		window.HasLimit = true
	}
	if value, ok := parseRateLimitValue(remaining); ok {
		window.Remaining = float64(value)
		window.HasRemaining = true
	}
	window.ResetAt = parseRateLimitReset(reset)
	return window
}
