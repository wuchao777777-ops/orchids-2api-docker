package api

import (
	"strings"
	"time"

	"orchids-api/internal/store"
)

// Preserve omitted edit fields without normalizing either stored credentials or
// whitespace-only incoming values when the stored value is also empty.
func preserveBlank(target *string, previous string) {
	if strings.TrimSpace(*target) == "" {
		*target = previous
	}
}

func preserveTime(target *time.Time, previous time.Time) {
	if target.IsZero() {
		*target = previous
	}
}

// Qoder and WorkBuddy share provider-observed usage and health. Qoder also
// preserves VerifiedAt in its own edit adapter.
func preserveObservedAccountState(acc, existing *store.Account) {
	acc.UsageLimit = existing.UsageLimit
	acc.UsageCurrent = existing.UsageCurrent
	acc.UsageTotal = existing.UsageTotal
	preserveTime(&acc.QuotaResetAt, existing.QuotaResetAt)
	preserveBlank(&acc.StatusCode, existing.StatusCode)
	preserveTime(&acc.LastAttempt, existing.LastAttempt)
}
