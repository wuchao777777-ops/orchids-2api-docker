package store

import (
	"context"
	"encoding/json"
	"orchids-api/internal/testutil"
	"testing"
	"time"
)

func TestClineModelsSyncedAtJSONAndUpdateMerge(t *testing.T) {
	t.Parallel()

	syncedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	raw, err := json.Marshal(&Account{ClineModelsSyncedAt: syncedAt})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded Account
	testutil.NoError(t, json.Unmarshal(raw, &decoded), "json.Unmarshal() error = %v")
	if !decoded.ClineModelsSyncedAt.Equal(syncedAt) {
		t.Fatalf("JSON round trip ClineModelsSyncedAt = %v, want %v", decoded.ClineModelsSyncedAt, syncedAt)
	}

	s, _ := newTestRedisStore(t, "cline-model-sync:")

	ctx := context.Background()
	acc := &Account{
		Name:                "cline",
		AccountType:         "cline",
		Enabled:             true,
		ClineModelIDs:       []string{"old-model"},
		ClineModelsSyncedAt: syncedAt,
	}
	testutil.NoError(t, s.CreateAccount(ctx, acc), "CreateAccount() error = %v")

	partial := *acc
	partial.ClineModelIDs = nil
	partial.ClineModelsSyncedAt = time.Time{}
	testutil.NoError(t, s.UpdateAccount(ctx, &partial), "UpdateAccount(partial) error = %v")
	got, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if !got.ClineModelsSyncedAt.Equal(syncedAt) || len(got.ClineModelIDs) != 1 || got.ClineModelIDs[0] != "old-model" {
		t.Fatalf("partial update lost snapshot: ids=%v synced_at=%v", got.ClineModelIDs, got.ClineModelsSyncedAt)
	}

	newer := syncedAt.Add(time.Minute)
	got.ClineModelIDs = []string{"new-model"}
	got.ClineModelsSyncedAt = newer
	testutil.NoError(t, s.UpdateAccount(ctx, got), "UpdateAccount(newer) error = %v")
	stale := *got
	stale.ClineModelIDs = nil
	stale.ClineModelsSyncedAt = syncedAt
	testutil.NoError(t, s.UpdateAccount(ctx, &stale), "UpdateAccount(stale) error = %v")
	got, err = s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount(after stale) error = %v", err)
	}
	if !got.ClineModelsSyncedAt.Equal(newer) {
		t.Fatalf("stale update rewound ClineModelsSyncedAt = %v, want %v", got.ClineModelsSyncedAt, newer)
	}
}
