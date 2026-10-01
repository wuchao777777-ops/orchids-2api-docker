package store

import (
	"context"
	"testing"
	"time"

	"orchids-api/internal/modelcatalog"
	"orchids-api/internal/testutil"
)

func TestUpdateAccountPreservesAndDeepCopiesNewestGrokCatalog(t *testing.T) {
	s, _ := newTestRedisStore(t, "catalog:")
	ctx := context.Background()
	synced := time.Now().UTC().Truncate(time.Second)
	acc := &Account{AccountType: "grok", Enabled: true, GrokModels: []string{"grok-4.7"}, GrokModelCatalog: []modelcatalog.Profile{{ModelID: "grok-4.7", ReasoningEfforts: []string{"high"}, ContextWindow: 500000}}, GrokModelsSyncedAt: synced}
	testutil.NoError(t, s.CreateAccount(ctx, acc))
	fresh, _ := s.GetAccount(ctx, acc.ID)
	stale := *fresh
	fresh.GrokModelCatalog[0].ReasoningEfforts[0] = "xhigh"
	fresh.GrokModelsSyncedAt = synced.Add(time.Minute)
	testutil.NoError(t, s.UpdateAccount(ctx, fresh))
	fresh.GrokModelCatalog[0].ReasoningEfforts[0] = "mutated-after-write"
	stale.GrokModels = []string{"stale"}
	stale.GrokModelCatalog = []modelcatalog.Profile{{ModelID: "stale"}}
	testutil.NoError(t, s.UpdateAccount(ctx, &stale))
	got, _ := s.GetAccount(ctx, acc.ID)
	if len(got.GrokModelCatalog) != 1 || got.GrokModelCatalog[0].ModelID != "grok-4.7" || got.GrokModelCatalog[0].ReasoningEfforts[0] != "xhigh" || got.GrokModels[0] != "grok-4.7" {
		t.Fatalf("newest catalog was overwritten or aliased: %+v", got.GrokModelCatalog)
	}
}

func TestUpdateAccount_PersistsGrokOAuthFields(t *testing.T) {
	t.Parallel()

	s, _ := newTestRedisStore(t, "test:")

	ctx := context.Background()
	acc := &Account{
		AccountType:       "grok",
		CredentialType:    "oauth",
		OAuthAccessToken:  "old-access",
		OAuthRefreshToken: "old-refresh",
		OAuthExpiresAt:    time.Now().UTC().Add(-time.Hour).Truncate(time.Second),
		TeamID:            "team-old",
		Enabled:           true,
	}
	testutil.NoError(t, s.CreateAccount(ctx, acc), "CreateAccount() error = %v")

	acc.OAuthAccessToken = "new-access"
	acc.OAuthRefreshToken = "new-refresh"
	acc.OAuthExpiresAt = time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	acc.TeamID = "team-new"
	testutil.NoError(t, s.UpdateAccount(ctx, acc), "UpdateAccount() error = %v")

	got, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	testutil.Equal(t, got.CredentialType, "oauth")
	testutil.Equal(t, got.OAuthAccessToken, "new-access")
	testutil.Equal(t, got.OAuthRefreshToken, "new-refresh")
	testutil.Equal(t, got.TeamID, "team-new")
	if got.OAuthExpiresAt.IsZero() || !got.OAuthExpiresAt.Equal(acc.OAuthExpiresAt) {
		t.Fatalf("OAuthExpiresAt=%v want %v", got.OAuthExpiresAt, acc.OAuthExpiresAt)
	}
}
