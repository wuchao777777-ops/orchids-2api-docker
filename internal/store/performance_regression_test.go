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
	if _, err := s.AuthorizeApiKey(ctx, "old-secret"); err != nil {
		t.Fatal(err)
	}
	key.KeyHash = hash("new-secret")
	key.AllowedModels = []string{"restricted"}
	testutil.NoError(t, s.UpdateApiKey(ctx, key))
	if _, err := s.AuthorizeApiKey(ctx, "old-secret"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("rotated key accepted: %v", err)
	}
	current, err := s.AuthorizeApiKey(ctx, "new-secret")
	if err != nil || len(current.AllowedModels) != 1 {
		t.Fatalf("policy=%v err=%v", current, err)
	}
	testutil.NoError(t, s.DeleteApiKey(ctx, key.ID))
	if _, err := s.AuthorizeApiKey(ctx, "new-secret"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("deleted key accepted: %v", err)
	}
}
