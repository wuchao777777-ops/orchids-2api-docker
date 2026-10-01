package store

import (
	"context"
	"orchids-api/internal/testutil"
	"testing"
	"time"
)

// TestUpdateAccount_VerifiedAtIsMonotonicPerCredential pins the persistence rule
// the scheduler relies on: an ordinary partial update must not un-verify an
// account, and only an explicit ClearVerifiedAt (credential replacement) drops
// the verdict stamp.
func TestUpdateAccount_VerifiedAtIsMonotonicPerCredential(t *testing.T) {
	s, _ := newTestRedisStore(t, "verified-at:")
	ctx := context.Background()

	acc := &Account{
		Name:             "grok-build",
		AccountType:      "grok",
		CredentialType:   "oauth",
		GrokProvider:     "build",
		OAuthAccessToken: "access-token",
		Enabled:          true,
		Weight:           1,
		VerifiedAt:       time.Now().Add(-time.Minute),
	}
	testutil.NoError(t, s.CreateAccount(ctx, acc), "CreateAccount() error = %v")
	stamp := acc.VerifiedAt

	// A partial update that carries no verdict stamp (request counters, quota
	// rotation) must keep the stored one.
	partial, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	partial.VerifiedAt = time.Time{}
	partial.StatusCode = "401"
	partial.StatusMessage = "upstream rejected the OAuth credential"
	testutil.NoError(t, s.UpdateAccount(ctx, partial), "UpdateAccount(partial) error = %v")
	afterPartial, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if !afterPartial.VerifiedAt.Equal(stamp) {
		t.Fatalf("verified_at = %v, want the stored %v", afterPartial.VerifiedAt, stamp)
	}
	if afterPartial.StatusCode != "401" || afterPartial.StatusMessage == "" {
		t.Fatalf("status write lost: %q / %q", afterPartial.StatusCode, afterPartial.StatusMessage)
	}

	// Replacing the credential drops it so the new credential is verified.
	afterPartial.OAuthAccessToken = "replacement-token"
	afterPartial.ClearVerifiedAt = true
	testutil.NoError(t, s.UpdateAccount(ctx, afterPartial), "UpdateAccount(replacement) error = %v")
	afterEdit, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if !afterEdit.VerifiedAt.IsZero() {
		t.Fatalf("verified_at = %v, want cleared for a replaced credential", afterEdit.VerifiedAt)
	}
	if afterEdit.ClearVerifiedAt {
		t.Fatal("ClearVerifiedAt leaked into the stored record")
	}

	// A fresh verdict is stored normally.
	afterEdit.VerifiedAt = time.Now()
	testutil.NoError(t, s.UpdateAccount(ctx, afterEdit), "UpdateAccount(verdict) error = %v")
	final, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if final.VerifiedAt.IsZero() {
		t.Fatal("a new verdict timestamp was not persisted")
	}
}
