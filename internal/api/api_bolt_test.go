package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"orchids-api/internal/bolt"
	"orchids-api/internal/config"
	"orchids-api/internal/store"
)

func TestRefreshAccountState_BoltRequiresSessionToken(t *testing.T) {
	t.Parallel()

	a := New(nil, "", "", &config.Config{})
	acc := &store.Account{AccountType: "bolt", ProjectID: "sb1-demo"}

	status, httpStatus, err := a.refreshAccountState(context.Background(), acc)
	if err == nil {
		t.Fatal("expected error")
	}
	if status != "" {
		t.Fatalf("status=%q want empty", status)
	}
	if httpStatus != http.StatusBadRequest {
		t.Fatalf("httpStatus=%d want %d", httpStatus, http.StatusBadRequest)
	}
}

func TestRefreshAccountState_BoltRequiresProjectID(t *testing.T) {
	t.Parallel()

	a := New(nil, "", "", &config.Config{})
	acc := &store.Account{AccountType: "bolt", SessionCookie: "sess"}

	status, httpStatus, err := a.refreshAccountState(context.Background(), acc)
	if err == nil {
		t.Fatal("expected error")
	}
	if status != "" {
		t.Fatalf("status=%q want empty", status)
	}
	if httpStatus != http.StatusBadRequest {
		t.Fatalf("httpStatus=%d want %d", httpStatus, http.StatusBadRequest)
	}
}

func TestRefreshAccountState_BoltAcceptsCompleteCredentials(t *testing.T) {
	prevFetch := boltFetchRootData
	t.Cleanup(func() {
		boltFetchRootData = prevFetch
	})

	boltFetchRootData = func(ctx context.Context, acc *store.Account, cfg *config.Config) (*bolt.RootData, error) {
		return &bolt.RootData{
			Token: "root-token",
			User: &bolt.RootUser{
				ID:                      "user_bolt",
				Email:                   "bolt@example.com",
				TotalBoltTokenPurchases: 1_000_000,
			},
		}, nil
	}

	a := New(nil, "", "", &config.Config{})
	acc := &store.Account{AccountType: "bolt", SessionCookie: "sess", ProjectID: "sb1-demo"}

	status, httpStatus, err := a.refreshAccountState(context.Background(), acc)
	if err != nil {
		t.Fatalf("refreshAccountState() error = %v", err)
	}
	if status != "" || httpStatus != 0 {
		t.Fatalf("unexpected status=%q httpStatus=%d", status, httpStatus)
	}
	if acc.UserID != "user_bolt" || acc.Email != "bolt@example.com" {
		t.Fatalf("unexpected bolt identity sync: user=%q email=%q", acc.UserID, acc.Email)
	}
	if acc.Subscription != "free" || acc.UsageCurrent != 1_000_000 || acc.UsageLimit != 1_000_000 {
		t.Fatalf("unexpected bolt quota sync: subscription=%q current=%v limit=%v", acc.Subscription, acc.UsageCurrent, acc.UsageLimit)
	}
}

func TestRefreshAccountState_BoltSyncsPaidTierQuota(t *testing.T) {
	prevFetch := boltFetchRootData
	t.Cleanup(func() {
		boltFetchRootData = prevFetch
	})

	boltFetchRootData = func(ctx context.Context, acc *store.Account, cfg *config.Config) (*bolt.RootData, error) {
		return &bolt.RootData{
			User: &bolt.RootUser{
				ID:                      "user_paid",
				TotalBoltTokenPurchases: 120_000_000,
				Membership: &bolt.Membership{
					Paid: true,
					Tier: float64(4),
				},
			},
		}, nil
	}

	a := New(nil, "", "", &config.Config{})
	acc := &store.Account{AccountType: "bolt", SessionCookie: "sess", ProjectID: "sb1-demo"}

	status, httpStatus, err := a.refreshAccountState(context.Background(), acc)
	if err != nil {
		t.Fatalf("refreshAccountState() error = %v", err)
	}
	if status != "" || httpStatus != 0 {
		t.Fatalf("unexpected status=%q httpStatus=%d", status, httpStatus)
	}
	if acc.Subscription != "pro-4" || acc.UsageCurrent != 120_000_000 || acc.UsageLimit != 120_000_000 {
		t.Fatalf("unexpected bolt paid quota sync: subscription=%q current=%v limit=%v", acc.Subscription, acc.UsageCurrent, acc.UsageLimit)
	}
}

func TestRefreshAccountState_BoltPropagatesUpstreamStatus(t *testing.T) {
	prevFetch := boltFetchRootData
	t.Cleanup(func() {
		boltFetchRootData = prevFetch
	})

	boltFetchRootData = func(ctx context.Context, acc *store.Account, cfg *config.Config) (*bolt.RootData, error) {
		return nil, errors.New("unexpected status code 401: unauthorized")
	}

	a := New(nil, "", "", &config.Config{})
	acc := &store.Account{AccountType: "bolt", SessionCookie: "sess", ProjectID: "sb1-demo"}

	status, httpStatus, err := a.refreshAccountState(context.Background(), acc)
	if err == nil {
		t.Fatal("expected error")
	}
	if status != "401" {
		t.Fatalf("status=%q want 401", status)
	}
	if httpStatus != http.StatusUnauthorized {
		t.Fatalf("httpStatus=%d want %d", httpStatus, http.StatusUnauthorized)
	}
}
