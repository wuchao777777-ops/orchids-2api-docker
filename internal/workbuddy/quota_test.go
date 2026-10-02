package workbuddy

import (
	"testing"
	"time"

	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

func TestSummarizeQuota_AggregatesPackages(t *testing.T) {
	t.Parallel()

	// Mirrors a real personal account: a free-plan package plus a bonus pack,
	// both reported with fractional precise values.
	var payload resourceResponse
	payload.Response.Data.TotalCount = 2
	payload.Response.Data.Accounts = []meterAccount{
		{
			PackageName:           "Free Plan Subscription",
			CapacityUnit:          "credit",
			CapacitySize:          250,
			CapacityRemain:        47,
			CapacityRemainPrecise: "47.28",
			CycleCapacitySize:     250,
			CycleCapacityRemain:   47,
			CycleCapacitySizeP:    "250",
			CycleCapacityRemainP:  "47.28",
			CycleCapacityUsedP:    "202.72",
			CycleEndTime:          "2026-09-26 00:13:42",
		},
		{
			PackageName:           "Bonus Pack",
			CapacityUnit:          "credit",
			CapacitySize:          100,
			CapacityRemain:        100,
			CapacityRemainPrecise: "100",
			CycleCapacitySize:     100,
			CycleCapacityRemain:   100,
			CycleCapacitySizeP:    "100",
			CycleCapacityRemainP:  "100",
			CycleEndTime:          "2026-09-26 00:13:42",
		},
	}

	now := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	quota := summarizeQuota(payload, now)

	testutil.Equal(t, quota.Limit, 350)
	testutil.Equal(t, quota.Remaining, 147.28)
	testutil.Equal(t, quota.VisibleUsed(), 202.72)
	testutil.Equal(t, quota.PackageRemaining, 147.28)
	testutil.NotEqual(t, quota.PackageName, "")
	wantReset := time.Date(2026, 9, 26, 0, 13, 42, 0, time.UTC)
	testutil.Falsef(t, !quota.ResetAt.Equal(wantReset), "ResetAt = %v, want %v", quota.ResetAt, wantReset)
	testutil.False(t, quota.SyncedAt.IsZero(), "SyncedAt is zero")
}

func TestSummarizeQuota_EmptyMeterIsZeroNotError(t *testing.T) {
	t.Parallel()

	quota := summarizeQuota(resourceResponse{}, time.Now())
	testutil.Equal(t, quota.Limit, 0)
	testutil.Equal(t, quota.Remaining, 0)
	testutil.Equal(t, quota.VisibleUsed(), 0)
}

func TestParseMeterTime_IgnoresPlaceholder(t *testing.T) {
	t.Parallel()

	got := parseMeterTime("9999-99-99 99:99:99")
	testutil.Falsef(t, !got.IsZero(), "placeholder parsed as %v, want zero", got)
	got = parseMeterTime("")
	testutil.Falsef(t, !got.IsZero(), "empty parsed as %v, want zero", got)
	got = parseMeterTime("2026-09-26 00:13:42")
	testutil.False(t, got.IsZero(), "valid timestamp did not parse")
}

func TestApplyQuota_MapsRemainingIntoSchedulingFields(t *testing.T) {
	t.Parallel()

	acc := &store.Account{AccountType: "workbuddy"}
	quota := &Quota{
		Limit:       350,
		Remaining:   147.28,
		ResetAt:     time.Date(2026, 9, 26, 0, 13, 42, 0, time.UTC),
		PackageName: "Free Plan Subscription",
		Unit:        "credit",
		SyncedAt:    time.Now(),
	}
	ApplyQuota(acc, quota)

	// UsageCurrent is the REMAINING value for this channel; the renderer derives
	// "used" from it, so storing used here would invert the quota bar.
	testutil.Equal(t, acc.UsageLimit, 350)
	testutil.Equal(t, acc.UsageCurrent, 147.28)
	testutil.False(t, acc.QuotaResetAt.IsZero(), "QuotaResetAt was not set from the cycle end")
	testutil.Falsef(t, acc.WorkBuddyQuota.PackageName == "" || acc.WorkBuddyQuota.SyncedAt.IsZero(), "snapshot = %+v", acc.WorkBuddyQuota)
	testutil.Equal(t, acc.WorkBuddyQuota.Used, 202.72)
}

func TestLastConsumedUnits_ResetsWithTheCycle(t *testing.T) {
	t.Parallel()

	cycleEnd := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	previous := store.WorkBuddyQuotaSnapshot{
		Limit:     350,
		Remaining: 200,
		Used:      150,
		ResetAt:   cycleEnd,
		SyncedAt:  time.Now().Add(-time.Hour),
	}

	grown := &Quota{Limit: 350, Remaining: 147.28, ResetAt: cycleEnd, SyncedAt: time.Now()}
	if got := grown.LastConsumedUnits(previous); got != 52 {
		// 202 whole credits consumed now vs 150 before: the counter reports the
		// whole-credit delta (fractional credit is not a countable "call").
		t.Fatalf("LastConsumedUnits() = %d, want 52", got)
	}

	// A new cycle re-arms the allowance; the counter must restart, not go negative.
	rearmed := &Quota{
		Limit:     350,
		Remaining: 350,
		ResetAt:   cycleEnd.Add(14 * 24 * time.Hour),
		SyncedAt:  time.Now(),
	}
	testutil.Equal(t, rearmed.LastConsumedUnits(previous), 0)

	// First ever snapshot reports the consumption observed so far.
	fresh := &Quota{Limit: 350, Remaining: 147.28, ResetAt: cycleEnd, SyncedAt: time.Now()}
	testutil.Equal(t, fresh.LastConsumedUnits(store.WorkBuddyQuotaSnapshot{}), 202)
}

// TestSummarizeQuota_PreciseZeroIsAReading pins the difference between "the
// meter says nothing" and "the meter says zero". A spent package reports
// "0.00", and treating that as absent let a stale coarse field win: an account
// with nothing left was then advertised as funded.
func TestSummarizeQuota_PreciseZeroIsAReading(t *testing.T) {
	t.Parallel()

	var payload resourceResponse
	payload.Response.Data.Accounts = []meterAccount{{
		PackageName:          "Free Plan Subscription",
		CapacityUnit:         "credit",
		CapacitySize:         100,
		CapacityRemain:       100, // stale coarse value: the precise field is the authority
		CycleCapacitySize:    100,
		CycleCapacityRemain:  100,
		CycleCapacitySizeP:   "100",
		CycleCapacityRemainP: "0.00",
		CycleEndTime:         "2026-09-30 23:59:59",
	}}

	quota := summarizeQuota(payload, time.Now())
	testutil.Equal(t, quota.Remaining, 0)
	testutil.Equal(t, quota.Limit, 100)
}

// TestSummarizeQuota_FallsBackWhenPreciseIsAbsent keeps the other direction: an
// omitted precise field still falls back to the coarse value.
func TestSummarizeQuota_FallsBackWhenPreciseIsAbsent(t *testing.T) {
	t.Parallel()

	testutil.Equal(t, preciseOr("", 0, 47.5), 47.5)
	testutil.Equal(t, preciseOr("0.00", 47.5), 0)
}

// TestParsePrecise_HandlesGrouping pins the grouping a large allowance carries:
// Sscanf stops at the comma and would report a thousandth of the real value.
func TestParsePrecise_HandlesGrouping(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]float64{
		"1,234.50": 1234.5,
		"12,000":   12000,
		"350":      350,
		"0.00":     0,
		"":         0,
		" 47.28 ":  47.28,
	} {
		testutil.CheckEqual(t, parsePrecise(raw), want)
	}
}

// TestSummarizeQuota_LabelsThePackageWithTheMostLeft pins the label an operator
// reads: it follows the largest remaining balance, not whichever row the
// upstream appended last.
func TestSummarizeQuota_LabelsThePackageWithTheMostLeft(t *testing.T) {
	t.Parallel()

	var payload resourceResponse
	payload.Response.Data.Accounts = []meterAccount{
		{
			PackageName: "Bonus Pack", CapacityUnit: "credit",
			CycleCapacitySizeP: "100", CycleCapacityRemainP: "5",
			CycleEndTime: "2026-09-30 00:00:00",
		},
		{
			PackageName: "Free Plan Subscription", CapacityUnit: "credit",
			CycleCapacitySizeP: "350", CycleCapacityRemainP: "300",
			CycleEndTime: "2026-09-28 00:00:00",
		},
	}

	quota := summarizeQuota(payload, time.Now())
	testutil.Equal(t, quota.PackageName, "Free Plan Subscription")
	testutil.Equal(t, quota.Remaining, 305)
	testutil.Equal(t, quota.Limit, 450)
	// The earliest cycle end is the one worth acting on.
	want := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	testutil.Falsef(t, !quota.ResetAt.Equal(want), "ResetAt = %v, want the earliest cycle end %v", quota.ResetAt, want)
}
