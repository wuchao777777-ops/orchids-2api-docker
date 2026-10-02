package store

import (
	"context"
	"orchids-api/internal/testutil"
	"testing"
	"time"
)

func TestUpdateAccountStaleRequestCannotOverwriteNewerQuota(t *testing.T) {
	s, _ := newTestRedisStore(t, "quota-order:")

	acc := &Account{AccountType: "qoder", Enabled: true}
	testutil.NoError(t, s.CreateAccount(context.Background(), acc))
	stale := *acc

	newReset := time.Now().Add(24 * time.Hour).Round(time.Millisecond)
	newSync := time.Now().Round(time.Millisecond)
	fresh := *acc
	fresh.QuotaResetAt = newReset
	fresh.QoderQuota = QoderQuotaSnapshot{Exhausted: true, ResetAt: newReset, SyncedAt: newSync}
	testutil.NoError(t, s.UpdateAccount(context.Background(), &fresh))

	stale.StatusCode = "402"
	stale.LastAttempt = time.Now()
	stale.QuotaResetAt = time.Now().Add(time.Hour)
	stale.QoderQuota = QoderQuotaSnapshot{ResetAt: stale.QuotaResetAt, SyncedAt: newSync.Add(-time.Minute)}
	testutil.NoError(t, s.UpdateAccount(context.Background(), &stale))

	got, err := s.GetAccount(context.Background(), acc.ID)
	testutil.NoError(t, err)
	testutil.Falsef(t, !got.QuotaResetAt.Equal(newReset) || !got.QoderQuota.ResetAt.Equal(newReset) || !got.QoderQuota.Exhausted, "stale write overwrote quota: reset=%v snapshot=%+v", got.QuotaResetAt, got.QoderQuota)
	testutil.Equal(t, got.StatusCode, "402")
}
