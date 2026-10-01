package qoder

import (
	"context"
	"testing"
	"time"

	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

// quotaNoticeStore records what a quota-notice write asked the store to persist.
// It implements both the atomic patch path and the full-account fallback so a
// test can pin which one a notice uses.
type quotaNoticeStore struct {
	patches []store.QoderAccountPatch
	full    []store.Account
}

func (s *quotaNoticeStore) UpdateAccount(_ context.Context, acc *store.Account) error {
	s.full = append(s.full, *acc)
	return nil
}

func (s *quotaNoticeStore) UpdateQoderAccount(_ context.Context, _ int64, patch store.QoderAccountPatch) error {
	s.patches = append(s.patches, patch)
	return nil
}

func newQuotaNoticeClient(t *testing.T) (*Client, *store.Account, *quotaNoticeStore) {
	t.Helper()
	acc := &store.Account{ID: 7, AccountType: "qoder", Name: "gate-under-test"}
	acc.QoderQuota = store.QoderQuotaSnapshot{
		Limit: 300, Used: 120, Remaining: 180,
		PlanTier: "Pro Trial", SyncedAt: time.Now().Add(-time.Hour),
	}
	client := &Client{account: acc}
	fake := &quotaNoticeStore{}
	client.SetAccountStore(fake)
	return client, acc, fake
}

// TestRecordQuotaNoticeMovesTheAccountView pins that an in-band quota verdict
// actually reaches the account, in memory and in the store.
//
// The gateway used to count NOTIFICATIONS frames and throw the payload away, so
// an account the upstream had already told us was spent kept its stale "has
// credits" snapshot until the next periodic sync — which is how a request stayed
// routable onto an account whose window had already closed.
func TestRecordQuotaNoticeMovesTheAccountView(t *testing.T) {
	client, acc, fake := newQuotaNoticeClient(t)
	previousSynced := acc.QoderQuota.SyncedAt
	reset := time.Now().Add(2 * time.Hour).Round(time.Millisecond)

	client.recordQuotaNotice(context.Background(), &QuotaNotice{
		Kind: "NOTIFICATIONS", Exhausted: true, NextResetAt: reset,
		UpgradeURL: "https://qoder.com/upgrade",
	})

	testutil.Equal(t, len(fake.patches), 1)
	testutil.Equal(t, len(fake.full), 0)
	patch := fake.patches[0]
	if patch.Quota == nil {
		t.Fatal("patch carried no quota; the notice was dropped on the way to the store")
	}
	if !patch.Quota.Exhausted {
		t.Error("patch.Quota.Exhausted = false, want true")
	}
	if !patch.Quota.ResetAt.Equal(reset) {
		t.Errorf("patch.Quota.ResetAt = %v, want the boundary the stream named (%v)", patch.Quota.ResetAt, reset)
	}
	testutil.CheckEqual(t, patch.Quota.UpgradeURL, "https://qoder.com/upgrade")
	if !patch.Quota.SyncedAt.After(previousSynced) {
		t.Errorf("SyncedAt = %v, want it newer than the reading it replaces (%v)", patch.Quota.SyncedAt, previousSynced)
	}
	// The stale counters must not be presented as current: the upstream said the
	// allowance is spent, so the flag — which is authoritative over the
	// arithmetic — has to say so too.
	if !acc.QoderQuota.Exhausted {
		t.Error("in-memory snapshot kept claiming credits after a quota_exceeded notice")
	}
	if !acc.QoderQuota.ResetAt.Equal(reset) {
		t.Errorf("in-memory ResetAt = %v, want %v", acc.QoderQuota.ResetAt, reset)
	}
	testutil.CheckEqual(t, acc.QoderQuota.PlanTier, "Pro Trial")
}

// TestRecordQuotaNoticeIgnoresNonVerdicts guards the other direction: only an
// exhaustion verdict may move the account, because a quota_low warning or a nil
// notice parked on a healthy account would take it out of rotation for nothing.
func TestRecordQuotaNoticeIgnoresNonVerdicts(t *testing.T) {
	client, _, fake := newQuotaNoticeClient(t)

	client.recordQuotaNotice(context.Background(), nil)
	client.recordQuotaNotice(context.Background(), &QuotaNotice{Kind: "NOTIFICATIONS"})
	client.recordQuotaNotice(context.Background(), &QuotaNotice{Kind: "NOTIFICATIONS", NextResetAt: time.Now().Add(time.Hour)})

	testutil.Equal(t, len(fake.patches), 0)
}

// TestRecordQuotaNoticeWithoutAResetKeepsTheOldWindow covers the boundary the
// upstream sometimes omits: the exhaustion still lands, but a window the notice
// does not name must not be invented or cleared.
func TestRecordQuotaNoticeWithoutAResetKeepsTheOldWindow(t *testing.T) {
	client, acc, fake := newQuotaNoticeClient(t)
	previous := acc.QoderQuota.ResetAt

	client.recordQuotaNotice(context.Background(), &QuotaNotice{Kind: "NOTIFICATIONS", Exhausted: true})

	testutil.Equal(t, len(fake.patches), 1)
	if got := fake.patches[0].Quota.ResetAt; !got.Equal(previous) {
		t.Errorf("ResetAt = %v, want the previous window %v preserved", got, previous)
	}
	if !acc.QoderQuota.Exhausted {
		t.Error("Exhausted = false, want the verdict recorded even without a boundary")
	}
}
