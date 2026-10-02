package store

import (
	"orchids-api/internal/testutil"
	"testing"
	"time"
)

// workBuddyAccount stores an account whose credit-meter reading is in place.
func workBuddyAccount(t *testing.T, s *Store, usage, limit float64, syncedAt time.Time) *Account {
	t.Helper()
	acc := &Account{
		AccountType:    "workbuddy",
		Enabled:        true,
		UsageLimit:     limit,
		UsageCurrent:   usage,
		WorkBuddyQuota: WorkBuddyQuotaSnapshot{Limit: limit, Remaining: usage, SyncedAt: syncedAt},
	}
	testutil.NoError(t, s.CreateAccount(t.Context(), acc), "CreateAccount() error = %v")
	return acc
}

// TestUpdateAccount_KeepsWorkBuddyUsageWithoutAMeterReading is the regression
// test for the account that reported "0 of 0" while its snapshot said 350.
//
// The generic usage slots mirror the credit-meter snapshot, but the merge rule
// only protected the snapshot: a write built from a fresh object — the login flow
// updating an existing row in place, an edit that never read the meter — copied
// its zeroes over the numbers the operator reads.
func TestUpdateAccount_KeepsWorkBuddyUsageWithoutAMeterReading(t *testing.T) {
	s, _ := newTestRedisStore(t, "wbusage:")
	acc := workBuddyAccount(t, s, 350, 350, time.Now())

	// A re-login style in-place update: the same credential, no meter reading.
	fresh := &Account{ID: acc.ID, AccountType: "workbuddy", Enabled: true}
	testutil.NoError(t, s.UpdateAccount(t.Context(), fresh), "UpdateAccount() error = %v")

	after, err := s.GetAccount(t.Context(), acc.ID)
	testutil.NoError(t, err, "GetAccount() error = %v")
	testutil.Equal(t, after.UsageCurrent, 350)
	testutil.Equal(t, after.UsageLimit, 350)
	testutil.Falsef(t, after.WorkBuddyQuota.Remaining != 350 || after.WorkBuddyQuota.SyncedAt.IsZero(), "quota = %+v, want the stored snapshot", after.WorkBuddyQuota)
}

// TestUpdateAccount_AcceptsANewerMeterReading is the other half: a real reading
// must move both the snapshot and the slots that mirror it.
func TestUpdateAccount_AcceptsANewerMeterReading(t *testing.T) {
	s, _ := newTestRedisStore(t, "wbnewer:")
	acc := workBuddyAccount(t, s, 350, 350, time.Now().Add(-time.Hour))

	newer := &Account{
		ID: acc.ID, AccountType: "workbuddy", Enabled: true,
		UsageLimit: 100, UsageCurrent: 0,
		WorkBuddyQuota: WorkBuddyQuotaSnapshot{Limit: 100, Remaining: 0, SyncedAt: time.Now()},
	}
	testutil.NoError(t, s.UpdateAccount(t.Context(), newer), "UpdateAccount() error = %v")

	after, err := s.GetAccount(t.Context(), acc.ID)
	testutil.NoError(t, err, "GetAccount() error = %v")
	testutil.Equal(t, after.UsageCurrent, 0)
	testutil.Equal(t, after.UsageLimit, 100)
	testutil.Equal(t, after.WorkBuddyQuota.Limit, 100)
}

// TestUpdateAccount_IgnoresAnOlderMeterReading covers the third case: a copy
// cached before a sync must not roll the stored reading back.
func TestUpdateAccount_IgnoresAnOlderMeterReading(t *testing.T) {
	s, _ := newTestRedisStore(t, "wbolder:")
	acc := workBuddyAccount(t, s, 350, 350, time.Now())

	stale := &Account{
		ID: acc.ID, AccountType: "workbuddy", Enabled: true,
		UsageLimit: 350, UsageCurrent: 999, // whatever the older copy held
		WorkBuddyQuota: WorkBuddyQuotaSnapshot{Limit: 350, Remaining: 999, SyncedAt: time.Now().Add(-time.Hour)},
	}
	testutil.NoError(t, s.UpdateAccount(t.Context(), stale), "UpdateAccount() error = %v")

	after, err := s.GetAccount(t.Context(), acc.ID)
	testutil.NoError(t, err, "GetAccount() error = %v")
	testutil.Equal(t, after.UsageCurrent, 350)
	testutil.Equal(t, after.WorkBuddyQuota.Remaining, 350)
}

// TestUpdateAccount_OtherChannelsKeepWritingUsage guards the guard: the rule is
// scoped to the channel whose generic slots mirror a meter, so every other
// channel still writes its usage fields as before.
func TestUpdateAccount_OtherChannelsKeepWritingUsage(t *testing.T) {
	s, _ := newTestRedisStore(t, "otherusage:")
	acc := &Account{AccountType: "cline", Enabled: true, UsageLimit: 100, UsageCurrent: 100}
	testutil.NoError(t, s.CreateAccount(t.Context(), acc), "CreateAccount() error = %v")

	updated := &Account{ID: acc.ID, AccountType: "cline", Enabled: true, UsageLimit: 40, UsageCurrent: 25}
	testutil.NoError(t, s.UpdateAccount(t.Context(), updated), "UpdateAccount() error = %v")

	after, err := s.GetAccount(t.Context(), acc.ID)
	testutil.NoError(t, err, "GetAccount() error = %v")
	testutil.Equal(t, after.UsageCurrent, 25)
	testutil.Equal(t, after.UsageLimit, 40)
}
