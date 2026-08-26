package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestStoredResponseOwnershipLifecycleAndIsolation(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := New(Options{RedisAddr: mini.Addr(), RedisPrefix: "responses-test:"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	record := &StoredResponse{
		ResponseID: "resp_123", OwnerHash: "owner-a", AccountID: 42,
		Model: "grok-4.6", Provider: "build",
	}
	if err := s.SaveStoredResponse(ctx, record, time.Hour); err != nil {
		t.Fatalf("SaveStoredResponse() error = %v", err)
	}
	got, err := s.GetStoredResponse(ctx, "resp_123", "owner-a")
	if err != nil || got.AccountID != 42 || got.Model != "grok-4.6" || got.ExpiresAt.IsZero() {
		t.Fatalf("GetStoredResponse() = %#v, %v", got, err)
	}
	if _, err := s.GetStoredResponse(ctx, "resp_123", "owner-b"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("cross-owner lookup error = %v, want ErrNoRows", err)
	}
	if err := s.DeleteStoredResponse(ctx, "resp_123", "owner-a"); err != nil {
		t.Fatalf("DeleteStoredResponse() error = %v", err)
	}
	if _, err := s.GetStoredResponse(ctx, "resp_123", "owner-a"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("lookup after delete error = %v, want ErrNoRows", err)
	}
}

func TestStoredResponseExpires(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := New(Options{RedisAddr: mini.Addr(), RedisPrefix: "responses-expiry:"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.SaveStoredResponse(context.Background(), &StoredResponse{
		ResponseID: "resp_expiring", OwnerHash: "owner", AccountID: 1,
	}, time.Second); err != nil {
		t.Fatal(err)
	}
	mini.FastForward(2 * time.Second)
	if _, err := s.GetStoredResponse(context.Background(), "resp_expiring", "owner"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("expired lookup error = %v, want ErrNoRows", err)
	}
}
