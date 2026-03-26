package main

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"orchids-api/internal/store"
)

func TestPreserveLatestAccountStatus_PreservesBlockedState(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{
		StoreMode:   "redis",
		RedisAddr:   mini.Addr(),
		RedisDB:     0,
		RedisPrefix: "test:",
	})
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer func() {
		_ = s.Close()
		mini.Close()
	}()

	original := &store.Account{
		Name:        "warp-1",
		AccountType: "warp",
		Enabled:     true,
		Weight:      1,
		StatusCode:  "403",
		LastAttempt: time.Now().Add(-2 * time.Minute),
	}
	if err := s.CreateAccount(context.Background(), original); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	stale := &store.Account{
		ID:          original.ID,
		Name:        original.Name,
		AccountType: original.AccountType,
		Enabled:     true,
		Weight:      1,
	}

	preserveLatestAccountStatus(context.Background(), s, stale)

	if stale.StatusCode != "403" {
		t.Fatalf("status_code=%q want 403", stale.StatusCode)
	}
	if stale.LastAttempt.IsZero() {
		t.Fatal("expected last_attempt to be preserved")
	}
}
