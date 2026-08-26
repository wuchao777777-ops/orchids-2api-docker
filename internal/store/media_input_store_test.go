package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestStoredMediaInputLifecycleAndIsolation(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := New(Options{RedisAddr: mini.Addr(), RedisPrefix: "media-input-test:"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	record := &StoredMediaInput{
		ID: "input_abcdefghijklmnopqrstuvwxyz012345", OwnerHash: "owner-a",
		Kind: "image", MIMEType: "image/png", ContentPath: "data/tmp/image/input.png", SizeBytes: 123,
	}
	if err := s.SaveStoredMediaInput(context.Background(), record, time.Hour); err != nil {
		t.Fatalf("SaveStoredMediaInput() error = %v", err)
	}
	got, err := s.GetStoredMediaInput(context.Background(), record.ID, record.OwnerHash)
	if err != nil || got.Kind != "image" || got.MIMEType != "image/png" || got.ExpiresAt.IsZero() {
		t.Fatalf("GetStoredMediaInput() = %#v, %v", got, err)
	}
	if _, err := s.GetStoredMediaInput(context.Background(), record.ID, "owner-b"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("cross-owner lookup error = %v, want ErrNoRows", err)
	}
	if err := s.DeleteStoredMediaInput(context.Background(), record.ID, record.OwnerHash); err != nil {
		t.Fatalf("DeleteStoredMediaInput() error = %v", err)
	}
	if _, err := s.GetStoredMediaInput(context.Background(), record.ID, record.OwnerHash); !errors.Is(err, ErrNoRows) {
		t.Fatalf("lookup after delete error = %v, want ErrNoRows", err)
	}
}

func TestStoredMediaInputExpires(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := New(Options{RedisAddr: mini.Addr(), RedisPrefix: "media-input-expiry:"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.SaveStoredMediaInput(context.Background(), &StoredMediaInput{
		ID: "input_abcdefghijklmnopqrstuvwxyz012345", OwnerHash: "owner", Kind: "image",
	}, time.Second); err != nil {
		t.Fatal(err)
	}
	mini.FastForward(2 * time.Second)
	if _, err := s.GetStoredMediaInput(context.Background(), "input_abcdefghijklmnopqrstuvwxyz012345", "owner"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("expired lookup error = %v, want ErrNoRows", err)
	}
}
