package store

import (
	"context"
	"testing"
	"time"
)

// TestUpdateQoderAccountQuotaPatchRespectsFreshness pins the write path an
// in-band quota notice uses.
//
// The notice arrives on a stream that is being answered right now, which means
// it can race a periodic sync that read a newer snapshot. The patch therefore
// goes through the same freshness rule as a synced reading: a notice at least as
// new as the stored one wins, an older one is discarded instead of rewinding the
// account's view of its own allowance.
func TestUpdateQoderAccountQuotaPatchRespectsFreshness(t *testing.T) {
	s, _ := newTestRedisStore(t, "qoder-quota-patch:")

	acc := &Account{AccountType: "qoder", Enabled: true}
	if err := s.CreateAccount(context.Background(), acc); err != nil {
		t.Fatal(err)
	}

	// The account starts with no snapshot at all: the first notice must land.
	first := time.Now().Round(time.Millisecond)
	reset := first.Add(3 * time.Hour)
	if err := s.UpdateQoderAccount(context.Background(), acc.ID, QoderAccountPatch{
		Quota: &QoderQuotaSnapshot{Exhausted: true, ResetAt: reset, SyncedAt: first},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetAccount(context.Background(), acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.QoderQuota.Exhausted {
		t.Fatalf("a notice against an empty snapshot was dropped: %+v", got.QoderQuota)
	}
	if !got.QoderQuota.ResetAt.Equal(reset) {
		t.Fatalf("ResetAt = %v, want %v", got.QoderQuota.ResetAt, reset)
	}

	// A newer notice replaces it, including the boundary the upstream named.
	newer := first.Add(time.Minute)
	newReset := reset.Add(24 * time.Hour)
	if err := s.UpdateQoderAccount(context.Background(), acc.ID, QoderAccountPatch{
		Quota: &QoderQuotaSnapshot{Exhausted: true, ResetAt: newReset, SyncedAt: newer},
	}); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetAccount(context.Background(), acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.QoderQuota.ResetAt.Equal(newReset) {
		t.Fatalf("ResetAt = %v, want the newer boundary %v", got.QoderQuota.ResetAt, newReset)
	}

	// A notice older than the stored reading must not move it backwards.
	if err := s.UpdateQoderAccount(context.Background(), acc.ID, QoderAccountPatch{
		Quota: &QoderQuotaSnapshot{Exhausted: false, Remaining: 300, SyncedAt: first},
	}); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetAccount(context.Background(), acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.QoderQuota.ResetAt.Equal(newReset) || !got.QoderQuota.Exhausted {
		t.Fatalf("a stale notice rewound the snapshot: reset=%v snapshot=%+v", got.QoderQuota.ResetAt, got.QoderQuota)
	}

	// A patch that carries no quota leaves the stored snapshot alone, which is
	// what a plain credential rotation relies on.
	if err := s.UpdateQoderAccount(context.Background(), acc.ID, QoderAccountPatch{UserID: "u-1"}); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetAccount(context.Background(), acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.QoderQuota.ResetAt.Equal(newReset) || got.QoderUserID != "u-1" {
		t.Fatalf("a credential patch disturbed the quota: user=%q snapshot=%+v", got.QoderUserID, got.QoderQuota)
	}
}
