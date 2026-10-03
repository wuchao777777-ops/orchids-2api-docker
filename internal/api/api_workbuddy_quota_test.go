package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

// TestRefreshAccountState_WorkBuddySpentMeterEnablesFreeOnlyMode pins the
// operator-confirmed behaviour: an empty metered package keeps the account
// available for confirmed free models from its latest catalog.
func TestRefreshAccountState_WorkBuddySpentMeterEnablesFreeOnlyMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/config":
			_, _ = w.Write([]byte(`{"code":0,"data":{"models":[{"id":"hy3"}],"agents":[{"name":"cli","models":["hy3"]}]}}`))
		case "/v2/billing/meter/get-user-resource":
			_, _ = w.Write([]byte(`{"code":0,"data":{"Response":{"Data":{"TotalCount":1,"Accounts":[{
				"PackageName":"Free Plan Subscription",
				"CapacityUnit":"credit",
				"CapacitySize":250,"CapacityRemain":0,
				"CapacityRemainPrecise":"0",
				"CycleCapacitySize":250,"CycleCapacityRemain":0,
				"CycleCapacitySizePrecise":"250","CycleCapacityRemainPrecise":"0",
				"CycleEndTime":"2026-09-26 00:13:42","Status":0}]}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a := New(nil, "", "", &config.Config{WorkBuddyBaseURL: srv.URL})
	acc := &store.Account{
		ID:                    5,
		AccountType:           "workbuddy",
		WorkBuddyAccessToken:  "access-token",
		WorkBuddyRefreshToken: "refresh-token",
		Enabled:               true,
	}

	status, httpStatus, err := a.refreshAccountState(context.Background(), acc)
	testutil.NoError(t, err, "refreshAccountState() error = %v")
	testutil.Equal(t, status, store.AccountStatusWorkBuddyQuotaExhausted)
	testutil.Equal(t, httpStatus, 0)
	// The spent package must still be visible: the 配额 column renders remaining=0.
	testutil.Equal(t, acc.UsageLimit, 250)
	testutil.Equal(t, acc.UsageCurrent, 0)
	testutil.Falsef(t, acc.WorkBuddyQuota.SyncedAt.IsZero() || acc.WorkBuddyQuota.Remaining != 0, "workbuddy_quota = %+v, want a synced snapshot with remaining 0", acc.WorkBuddyQuota)
	fields := buildQuotaResponseFields(acc)
	testutil.Equal(t, fields["quota_remaining"], 0.0)
	testutil.Equal(t, fields["quota_supported"], true)
}

// TestHandleAccounts_CheckWorkBuddySpentMeterKeepsThePark covers an account parked
// for a spent allowance.
//
// The check verifies the credential, and the credential is fine — what parked the
// account is the upstream refusing an actual request. The next check must therefore
// not clear the marker while the meter still reports the allowance spent, or the
// console shows an account turning green and failing again on the next request.
// The release that matters is covered by ...ClearsParkOnceToppedUp below.
func TestHandleAccounts_CheckWorkBuddySpentMeterKeepsThePark(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/config":
			_, _ = w.Write([]byte(`{"code":0,"data":{"models":[{"id":"hy3"}],"agents":[{"name":"cli","models":["hy3"]}]}}`))
		case "/v2/billing/meter/get-user-resource":
			_, _ = w.Write([]byte(`{"code":0,"data":{"Response":{"Data":{"TotalCount":1,"Accounts":[{
				"PackageName":"Free Plan Subscription","CapacityUnit":"credit",
				"CapacitySize":250,"CapacityRemain":0,"CapacityRemainPrecise":"0",
				"CycleCapacitySize":250,"CycleCapacityRemain":0,
				"CycleCapacitySizePrecise":"250","CycleCapacityRemainPrecise":"0",
				"CycleEndTime":"2026-09-26 00:13:42","Status":0}]}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s, _ := newTestStore(t, "wb-park:")
	ctx := context.Background()
	acc := &store.Account{
		AccountType:           "workbuddy",
		Enabled:               true,
		Weight:                1,
		WorkBuddyAccessToken:  "access-token",
		WorkBuddyRefreshToken: "refresh-token",
		StatusCode:            "402",
		StatusMessage:         "credits exhausted",
		LastAttempt:           time.Now(),
	}
	testutil.NoError(t, s.CreateAccount(ctx, acc), "CreateAccount() error = %v")

	a := New(s, "", "", &config.Config{WorkBuddyBaseURL: srv.URL})
	rec := httptest.NewRecorder()
	a.HandleAccountByID(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/"+strconv.FormatInt(acc.ID, 10)+"/check", nil))
	testutil.Equal(t, rec.Code, http.StatusOK)

	stored, err := s.GetAccount(ctx, acc.ID)
	testutil.NoError(t, err, "GetAccount() error = %v")
	testutil.Equal(t, stored.StatusCode, store.AccountStatusWorkBuddyQuotaExhausted)
	testutil.Equal(t, stored.StatusMessage, "")
	testutil.False(t, stored.VerifiedAt.IsZero(), "the check must still record that the credential was exercised")
}

// TestHandleAccounts_CheckWorkBuddyClearsParkOnceToppedUp is the release half, and
// the reason the rule reads the meter rather than only the reset time: an operator
// who buys credits must get the account back on the next check, not at the cycle
// boundary the reset time names.
func TestHandleAccounts_CheckWorkBuddyClearsParkOnceToppedUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/config":
			_, _ = w.Write([]byte(`{"code":0,"data":{"models":[{"id":"hy3"}],"agents":[{"name":"cli","models":["hy3"]}]}}`))
		case "/v2/billing/meter/get-user-resource":
			// The same package and cycle, but with credits added.
			_, _ = w.Write([]byte(`{"code":0,"data":{"Response":{"Data":{"TotalCount":1,"Accounts":[{
				"PackageName":"Free Plan Subscription","CapacityUnit":"credit",
				"CapacitySize":250,"CapacityRemain":250,"CapacityRemainPrecise":"250",
				"CycleCapacitySize":250,"CycleCapacityRemain":250,
				"CycleCapacitySizePrecise":"250","CycleCapacityRemainPrecise":"250",
				"CycleEndTime":"2026-09-26 00:13:42","Status":0}]}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s, _ := newTestStore(t, "wb-topup:")
	ctx := context.Background()
	acc := &store.Account{
		AccountType:           "workbuddy",
		Enabled:               true,
		Weight:                1,
		WorkBuddyAccessToken:  "access-token",
		WorkBuddyRefreshToken: "refresh-token",
		StatusCode:            "402",
		StatusMessage:         "credits exhausted",
		LastAttempt:           time.Now(),
	}
	testutil.NoError(t, s.CreateAccount(ctx, acc), "CreateAccount() error = %v")

	a := New(s, "", "", &config.Config{WorkBuddyBaseURL: srv.URL})
	rec := httptest.NewRecorder()
	a.HandleAccountByID(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/"+strconv.FormatInt(acc.ID, 10)+"/check", nil))
	testutil.Equal(t, rec.Code, http.StatusOK)

	stored, err := s.GetAccount(ctx, acc.ID)
	testutil.NoError(t, err, "GetAccount() error = %v")
	testutil.Equal(t, stored.StatusCode, "")
}

func TestRefreshAccountState_WorkBuddyMeterFailureKeepsAccountUsable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/config":
			_, _ = w.Write([]byte(`{"code":0,"data":{"models":[{"id":"hy3"}],"agents":[{"name":"cli","models":["hy3"]}]}}`))
		case "/v2/billing/meter/get-user-resource":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":10001,"msg":"forbidden"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a := New(nil, "", "", &config.Config{WorkBuddyBaseURL: srv.URL})
	acc := &store.Account{ID: 4, AccountType: "workbuddy", WorkBuddyAccessToken: "access"}

	status, httpStatus, err := a.refreshAccountState(context.Background(), acc)
	testutil.NoError(t, err, "a meter failure must not fail the account sync: %v")
	testutil.Equal(t, status, "")
	testutil.Equal(t, httpStatus, 0)
	testutil.Equal(t, len(acc.WorkBuddyModelIDs), 1)
	fields := buildQuotaResponseFields(acc)
	testutil.Equal(t, fields["quota_supported"], false)
}
