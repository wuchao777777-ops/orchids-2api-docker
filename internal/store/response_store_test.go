package store

import (
	"context"
	"errors"
	"orchids-api/internal/testutil"
	"testing"
	"time"
)

func TestStoredResponseOwnershipLifecycleAndIsolation(t *testing.T) {
	s, _ := newTestRedisStore(t, "responses-test:")

	ctx := context.Background()
	record := &StoredResponse{
		ResponseID: "resp_123", OwnerHash: "owner-a", AccountID: 42,
		Model: "grok-4.6", Provider: "build",
	}
	testutil.NoError(t, s.SaveStoredResponse(ctx, record, time.Hour), "SaveStoredResponse() error = %v")
	got, err := s.GetStoredResponse(ctx, "resp_123", "owner-a")
	testutil.Falsef(t, err != nil || got.AccountID != 42 || got.Model != "grok-4.6" || got.ExpiresAt.IsZero(), "GetStoredResponse() = %#v, %v", got, err)
	_, err = s.GetStoredResponse(ctx, "resp_123", "owner-b")
	testutil.Falsef(t, !errors.Is(err, ErrNoRows), "cross-owner lookup error = %v, want ErrNoRows", err)
	testutil.NoError(t, s.DeleteStoredResponse(ctx, "resp_123", "owner-a"), "DeleteStoredResponse() error = %v")
	_, err = s.GetStoredResponse(ctx, "resp_123", "owner-a")
	testutil.Falsef(t, !errors.Is(err, ErrNoRows), "lookup after delete error = %v, want ErrNoRows", err)
}

func TestStoredResponseExpires(t *testing.T) {
	s, mini := newTestRedisStore(t, "responses-expiry:")
	if err := s.SaveStoredResponse(context.Background(), &StoredResponse{
		ResponseID: "resp_expiring", OwnerHash: "owner", AccountID: 1,
	}, time.Second); err != nil {
		t.Fatal(err)
	}
	mini.FastForward(2 * time.Second)
	_, err := s.GetStoredResponse(context.Background(), "resp_expiring", "owner")
	testutil.Falsef(t, !errors.Is(err, ErrNoRows), "expired lookup error = %v, want ErrNoRows", err)
}
