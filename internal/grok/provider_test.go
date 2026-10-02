package grok

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"orchids-api/internal/modelcatalog"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

func TestBuildCapabilitySnapshotAndRateLimitsDoNotBecomeBilling(t *testing.T) {
	acc := &store.Account{
		AccountType:    "grok",
		CredentialType: "oauth",
		UsageCurrent:   8300,
		UsageLimit:     8300,
	}
	headers := make(http.Header)
	headers.Set("x-ratelimit-limit-requests", "20")
	headers.Set("x-ratelimit-remaining-requests", "19")
	headers.Set("x-ratelimit-limit-tokens", "8300")
	headers.Set("x-ratelimit-remaining-tokens", "8192")
	testutil.False(t, !ApplyBuildRateLimits(acc, headers), "ApplyBuildRateLimits() = false")
	testutil.Falsef(t, acc.GrokRateLimits.Tokens.Limit != 8300 || acc.GrokBilling.Weekly.HasUsage, "rate limits/billing mixed: %+v %+v", acc.GrokRateLimits, acc.GrokBilling)
	testutil.False(t, !ApplyCLIBillingInfo(acc, &CLIBillingInfo{UsagePercent: 12, HasUsagePercent: true, PeriodEnd: time.Now().Add(time.Hour)}), "ApplyCLIBillingInfo() = false")
	testutil.Equal(t, acc.UsageCurrent, 0)
	testutil.Equal(t, acc.UsageLimit, 0)
	testutil.Falsef(t, !acc.GrokBilling.Weekly.HasUsage || acc.GrokBilling.Weekly.UsagePercent != 12 || acc.GrokRateLimits.Tokens.Limit != 8300, "billing/rate-limit separation lost: %+v %+v", acc.GrokBilling, acc.GrokRateLimits)
}

func TestAccountSupportsModelUsesObservedBuildCatalog(t *testing.T) {
	acc := &store.Account{AccountType: "grok", CredentialType: "oauth"}
	testutil.False(t, !AccountSupportsModel(acc, "grok-4.6"), "unsynced account should remain eligible until its catalog is read")
	ApplyCLIModelCatalog(acc, []modelcatalog.Profile{{ModelID: "grok-4.5"}}, time.Now())
	testutil.Falsef(t, AccountSupportsModel(acc, "grok-4.6") || !AccountSupportsModel(acc, "grok-4.5"), "observed catalog not enforced: %#v", acc.GrokModels)
}

// TestApplyCLIModelCatalogRestoresGrok2APICatalogCompletion checks observed
// models and supported Build-derived entries.
func TestApplyCLIModelCatalogRestoresGrok2APICatalogCompletion(t *testing.T) {
	acc := &store.Account{AccountType: "grok", CredentialType: "oauth", GrokProvider: ProviderBuild, Subscription: "super"}
	ApplyCLIModelCatalog(acc, []modelcatalog.Profile{{ModelID: "grok-4.6"}, {ModelID: "grok-imagine-video-1.5"}, {ModelID: "grok-4.6"}}, time.Now())

	want := []string{"grok-4.6", "grok-imagine-video-1.5", "grok-4.5", "grok-composer-2.5-fast"}
	testutil.Equal(t, len(acc.GrokModels), len(want))
	for i, model := range want {
		testutil.Falsef(t, !strings.EqualFold(acc.GrokModels[i], model), "catalog = %#v, want %#v", acc.GrokModels, want)
	}
	for _, derived := range want[2:] {
		testutil.True(t, AccountSupportsModel(acc, derived), "derived capability %q is missing: %#v")
	}
	testutil.False(t, acc.GrokModelsSyncedAt.IsZero(), "the snapshot was not dated")
}

// The only capability grok2api gates on tier is the video 1.5 entry: a Super
// account gains it, anything below loses it even if the catalog listed it.
