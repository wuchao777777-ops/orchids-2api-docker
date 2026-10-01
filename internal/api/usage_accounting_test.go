package api

import (
	"testing"

	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

func TestBuildQuotaMonthlyHasPresentationPriorityOverPercent(t *testing.T) {
	acc := buildAccount("supergrok", store.GrokBillingSnapshot{
		Weekly:  store.GrokQuotaWindow{HasUsage: true, UsagePercent: 40},
		Monthly: store.GrokQuotaWindow{HasLimit: true, Limit: 200, HasRemaining: true, Remaining: 150},
	})
	fields := buildQuotaResponseFields(acc)
	testutil.Equal(t, fieldString(t, fields, "quota_mode"), "monthly")
	testutil.Equal(t, fieldFloat(t, fields, "quota_limit"), 200)
	testutil.Equal(t, fieldFloat(t, fields, "quota_weekly_usage_percent"), 40)
}
