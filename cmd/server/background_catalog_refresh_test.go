package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

func TestCatalogRefreshDueUsesProviderSyncTimestamp(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-providerHealthRefreshInterval + time.Minute)
	stale := now.Add(-providerHealthRefreshInterval - time.Minute)

	if clineCatalogRefreshDue(nil, now) || workBuddyCatalogRefreshDue(nil, now) {
		t.Fatal("nil accounts must not be due")
	}
	if !clineCatalogRefreshDue(&store.Account{}, now) || !workBuddyCatalogRefreshDue(&store.Account{}, now) {
		t.Fatal("empty snapshots must be due")
	}
	if !clineCatalogRefreshDue(&store.Account{ClineModelIDs: []string{"m"}}, now) {
		t.Fatal("Cline snapshot without sync timestamp must be due")
	}
	if clineCatalogRefreshDue(&store.Account{ClineModelIDs: []string{"m"}, ClineModelsSyncedAt: fresh, UpdatedAt: stale}, now) {
		t.Fatal("fresh Cline sync must not become due because UpdatedAt is old")
	}
	if !clineCatalogRefreshDue(&store.Account{ClineModelIDs: []string{"m"}, ClineModelsSyncedAt: stale, UpdatedAt: now}, now) {
		t.Fatal("stale Cline sync must be due even when UpdatedAt is fresh")
	}
	if workBuddyCatalogRefreshDue(&store.Account{WorkBuddyModelIDs: []string{"m"}, WorkBuddyModelsSyncedAt: fresh}, now) {
		t.Fatal("fresh WorkBuddy sync must not be due")
	}
	if !workBuddyCatalogRefreshDue(&store.Account{WorkBuddyModelIDs: []string{"m"}, WorkBuddyModelsSyncedAt: stale}, now) {
		t.Fatal("stale WorkBuddy sync must be due")
	}
}

func TestRefreshWorkBuddyCatalogPersistsSuccessAndKeepsLKGOnFailure(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/config" {
			http.NotFound(w, r)
			return
		}
		if fail.Load() {
			http.Error(w, "temporary outage", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"models":[{"id":"fresh-model"}],"agents":[{"name":"cli","models":["fresh-model"]}]}}`))
	}))
	defer srv.Close()

	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{RedisAddr: mini.Addr(), RedisPrefix: "workbuddy-background:"})
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	acc := &store.Account{
		Name:                    "workbuddy",
		AccountType:             "workbuddy",
		Enabled:                 true,
		WorkBuddyAccessToken:    "access",
		WorkBuddyUID:            "uid",
		WorkBuddyModelIDs:       []string{"last-known-good"},
		WorkBuddyModelsSyncedAt: time.Now().Add(-time.Hour),
	}
	testutil.NoError(t, s.CreateAccount(context.Background(), acc), "CreateAccount() error = %v")
	cfg := &config.Config{WorkBuddyBaseURL: srv.URL}
	refreshWorkBuddyCatalog(context.Background(), cfg, s, acc)

	got, err := s.GetAccount(context.Background(), acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if len(got.WorkBuddyModelIDs) != 1 || !strings.Contains(got.WorkBuddyModelIDs[0], `"id":"fresh-model"`) || got.WorkBuddyModelsSyncedAt.IsZero() {
		t.Fatalf("successful refresh = ids %v synced_at %v", got.WorkBuddyModelIDs, got.WorkBuddyModelsSyncedAt)
	}

	fail.Store(true)
	got.WorkBuddyModelsSyncedAt = time.Now().Add(-time.Hour)
	testutil.NoError(t, s.UpdateAccount(context.Background(), got), "make snapshot due: %v")
	refreshWorkBuddyCatalog(context.Background(), cfg, s, got)
	afterFailure, err := s.GetAccount(context.Background(), acc.ID)
	if err != nil {
		t.Fatalf("GetAccount(after failure) error = %v", err)
	}
	if len(afterFailure.WorkBuddyModelIDs) != 1 || !strings.Contains(afterFailure.WorkBuddyModelIDs[0], `"id":"fresh-model"`) {
		t.Fatalf("failed refresh replaced LKG: %v", afterFailure.WorkBuddyModelIDs)
	}
}

// TestWorkBuddyQuotaRefreshDueUsesMeterTimestamp keeps the credit-meter reading
// on the same cadence as every other provider snapshot. Before this the reading
// was only written at login and on a manual check, so production held a
// 36-hour-old allowance for an account whose balance had already moved.
func TestWorkBuddyQuotaRefreshDueUsesMeterTimestamp(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-providerHealthRefreshInterval + time.Minute)
	stale := now.Add(-providerHealthRefreshInterval - time.Minute)

	if workBuddyQuotaRefreshDue(nil, now) {
		t.Fatal("a nil account must not be due")
	}
	if !workBuddyQuotaRefreshDue(&store.Account{}, now) {
		t.Fatal("an account with no meter reading must be due")
	}
	if workBuddyQuotaRefreshDue(&store.Account{WorkBuddyQuota: store.WorkBuddyQuotaSnapshot{SyncedAt: fresh}}, now) {
		t.Fatal("a fresh reading must not be re-read on this tick")
	}
	if !workBuddyQuotaRefreshDue(&store.Account{WorkBuddyQuota: store.WorkBuddyQuotaSnapshot{SyncedAt: stale}}, now) {
		t.Fatal("a stale reading must be re-read")
	}
}
