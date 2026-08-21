package store

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestUpdateAccount_PersistsGrokOAuthFields(t *testing.T) {
	t.Parallel()

	mini := miniredis.RunT(t)
	s, err := New(Options{
		StoreMode:   "redis",
		RedisAddr:   mini.Addr(),
		RedisDB:     0,
		RedisPrefix: "test:",
	})
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		mini.Close()
	})

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
	if err := s.CreateAccount(ctx, acc); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	acc.OAuthAccessToken = "new-access"
	acc.OAuthRefreshToken = "new-refresh"
	acc.OAuthExpiresAt = time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	acc.TeamID = "team-new"
	if err := s.UpdateAccount(ctx, acc); err != nil {
		t.Fatalf("UpdateAccount() error = %v", err)
	}

	got, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if got.CredentialType != "oauth" {
		t.Fatalf("CredentialType=%q", got.CredentialType)
	}
	if got.OAuthAccessToken != "new-access" {
		t.Fatalf("OAuthAccessToken=%q", got.OAuthAccessToken)
	}
	if got.OAuthRefreshToken != "new-refresh" {
		t.Fatalf("OAuthRefreshToken=%q", got.OAuthRefreshToken)
	}
	if got.TeamID != "team-new" {
		t.Fatalf("TeamID=%q", got.TeamID)
	}
	if got.OAuthExpiresAt.IsZero() || !got.OAuthExpiresAt.Equal(acc.OAuthExpiresAt) {
		t.Fatalf("OAuthExpiresAt=%v want %v", got.OAuthExpiresAt, acc.OAuthExpiresAt)
	}
}
