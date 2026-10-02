package store

import (
	"context"
	"orchids-api/internal/testutil"
	"testing"
	"time"
)

// TestAccountFreeQuotaPersistsAndSurvivesPartialUpdates pins the two persistence
// rules the confirmed Free window depends on: it round-trips through Redis, and an
// unrelated partial update (a request counter, a credential rotation) cannot erase it.
func TestAccountFreeQuotaPersistsAndSurvivesPartialUpdates(t *testing.T) {
	s, _ := newTestRedisStore(t, "free-quota:")
	ctx := context.Background()

	confirmedAt := time.Now().UTC().Truncate(time.Second)
	acc := &Account{
		Name:             "build-free",
		AccountType:      "grok",
		CredentialType:   "oauth",
		GrokProvider:     "build",
		OAuthAccessToken: "access",
		Enabled:          true,
		Weight:           1,
		GrokFreeQuota: GrokFreeQuotaSnapshot{
			Used: 500123, Limit: 500000, HasLimit: true,
			ConfirmedAt: confirmedAt,
			ResetAt:     confirmedAt.Add(24 * time.Hour),
		},
	}
	testutil.NoError(t, s.CreateAccount(ctx, acc), "CreateAccount() error = %v")

	stored, err := s.GetAccount(ctx, acc.ID)
	testutil.NoError(t, err, "GetAccount() error = %v")
	testutil.Falsef(t, !stored.GrokFreeQuota.HasLimit || stored.GrokFreeQuota.Used != 500123 || stored.GrokFreeQuota.Limit != 500000, "stored free quota = %+v, want the confirmed 500123/500000 pair", stored.GrokFreeQuota)
	testutil.Falsef(t, !stored.GrokFreeQuota.ConfirmedAt.Equal(confirmedAt), "confirmedAt = %v, want %v", stored.GrokFreeQuota.ConfirmedAt, confirmedAt)

	// A partial update that never mentions the free window must keep it: the window is
	// confirmed once per exhaustion and must not be lost by the next request counter.
	partial := *stored
	partial.GrokFreeQuota = GrokFreeQuotaSnapshot{}
	partial.RequestCount = stored.RequestCount + 1
	testutil.NoError(t, s.UpdateAccount(ctx, &partial), "UpdateAccount() error = %v")

	after, err := s.GetAccount(ctx, acc.ID)
	testutil.NoError(t, err, "GetAccount() error = %v")
	testutil.Falsef(t, !after.GrokFreeQuota.HasLimit || after.GrokFreeQuota.Limit != 500000, "a partial update erased the confirmed free window: %+v", after.GrokFreeQuota)
}
