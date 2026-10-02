package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"orchids-api/internal/testutil"
	"testing"
)

func TestCachedKeyIndexRejectsRotatedKeyAndReadsCurrentPolicy(t *testing.T) {
	s, _ := newTestRedisStore(t, "rotation:")
	ctx := context.Background()
	hash := func(raw string) string { sum := sha256.Sum256([]byte(raw)); return hex.EncodeToString(sum[:]) }
	key := &ApiKey{Name: "perf", KeyHash: hash("old-secret"), Enabled: true}
	testutil.NoError(t, s.CreateApiKey(ctx, key))
	_, err := s.AuthorizeApiKey(ctx, "old-secret")
	testutil.NoError(t, err)
	key.KeyHash = hash("new-secret")
	key.AllowedModels = []string{"restricted"}
	testutil.NoError(t, s.UpdateApiKey(ctx, key))
	_, err = s.AuthorizeApiKey(ctx, "old-secret")
	testutil.Falsef(t, !errors.Is(err, ErrNoRows), "rotated key accepted: %v", err)
	current, err := s.AuthorizeApiKey(ctx, "new-secret")
	testutil.Equal(t, err, nil)
	testutil.Equal(t, len(current.AllowedModels), 1)
	testutil.NoError(t, s.DeleteApiKey(ctx, key.ID))
	_, err = s.AuthorizeApiKey(ctx, "new-secret")
	testutil.Falsef(t, !errors.Is(err, ErrNoRows), "deleted key accepted: %v", err)
}
