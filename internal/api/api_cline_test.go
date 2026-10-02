package api

import (
	"testing"
	"time"

	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

func TestPreserveClineCredentialsOnEditKeepsCatalogTimestamp(t *testing.T) {
	syncedAt := time.Now().UTC().Truncate(time.Second)
	existing := &store.Account{
		ClineAccessToken:    "access",
		ClineRefreshToken:   "refresh",
		ClineModelIDs:       []string{"model-a"},
		ClineModelsSyncedAt: syncedAt,
	}
	edited := &store.Account{}
	PreserveClineCredentialsOnEdit(edited, existing)
	testutil.Equal(t, len(edited.ClineModelIDs), 1)
	testutil.Equal(t, edited.ClineModelIDs[0], "model-a")
	testutil.Falsef(t, !edited.ClineModelsSyncedAt.Equal(syncedAt), "ClineModelsSyncedAt = %v, want %v", edited.ClineModelsSyncedAt, syncedAt)
}
