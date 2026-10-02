package api

import (
	"context"
	"encoding/base64"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"encoding/json"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
	"orchids-api/internal/workbuddy"
)

// workBuddyAuthDocument is the shape the WorkBuddy desktop client stores. The
// access token carries the Keycloak identity claims (sub / email), which is what
// lets a pasted document label the account without an extra profile call.
var workBuddyAuthDocument = func() string {
	encode := func(value interface{}) string {
		raw, _ := json.Marshal(value)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	token := encode(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." +
		encode(map[string]interface{}{
			"sub":   "07ab88c8-5596-4257-8d21-e9fcbe3a3810",
			"email": "operator@example.com",
			"iss":   "https://www.workbuddy.ai/auth/realms/copilot",
			"exp":   time.Now().Add(48 * time.Hour).Unix(),
		}) + ".signature"
	return `{
  "account": {"uid": "07ab88c8-5596-4257-8d21-e9fcbe3a3810", "nickname": "operator@example.com"},
  "auth": {
    "accessToken": "` + token + `",
    "refreshToken": "durable-refresh-token",
    "expiresAt": 1820679206000
  }
}`
}()

func TestNormalizeWorkBuddyCredentials_SplitsDocumentIntoDedicatedFields(t *testing.T) {
	t.Parallel()

	acc := &store.Account{AccountType: "workbuddy", ClientCookie: workBuddyAuthDocument}
	testutil.False(t, !NormalizeWorkBuddyCredentials(acc), "NormalizeWorkBuddyCredentials() = false, want true")
	testutil.Equal(t, acc.WorkBuddyRefreshToken, "durable-refresh-token")
	testutil.Falsef(t, acc.WorkBuddyAccessToken == "" || workbuddy.DecodeClaims(acc.WorkBuddyAccessToken).Sub != "07ab88c8-5596-4257-8d21-e9fcbe3a3810", "WorkBuddyAccessToken = %q, want the embedded token", acc.WorkBuddyAccessToken)
	testutil.Equal(t, acc.WorkBuddyUID, "07ab88c8-5596-4257-8d21-e9fcbe3a3810")
	// The identity the table labels the row with comes from the token claims.
	testutil.Equal(t, acc.Email, "operator@example.com")
	testutil.Equal(t, acc.Name, "operator@example.com")
	testutil.False(t, acc.WorkBuddyExpiresAt.IsZero(), "WorkBuddyExpiresAt is zero, want the millisecond expiry converted")
	// The generic credential slots are shared with other channels and must stay
	// empty so the refresh token is never echoed through the account list.
	for _, value := range map[string]string{
		"ClientCookie": acc.ClientCookie,
		"Token":        acc.Token,
		"RefreshToken": acc.RefreshToken,
	} {
		testutil.Equal(t, value, "")
	}
}

func TestNormalizeWorkBuddyCredentials_AcceptsBareRefreshToken(t *testing.T) {
	t.Parallel()

	acc := &store.Account{AccountType: "workbuddy", ClientCookie: "opaque-refresh-token"}
	testutil.False(t, !NormalizeWorkBuddyCredentials(acc), "NormalizeWorkBuddyCredentials() = false, want true")
	testutil.Equal(t, acc.WorkBuddyRefreshToken, "opaque-refresh-token")
}

func TestNormalizeWorkBuddyCredentials_RejectsEmptyInput(t *testing.T) {
	t.Parallel()

	acc := &store.Account{AccountType: "workbuddy"}
	testutil.False(t, NormalizeWorkBuddyCredentials(acc), "NormalizeWorkBuddyCredentials() = true, want false for an empty credential")
}

func TestRedactWorkBuddyOutput_HidesDurableSecrets(t *testing.T) {
	t.Parallel()

	acc := &store.Account{
		AccountType:           "workbuddy",
		WorkBuddyAccessToken:  "visible-access-token",
		WorkBuddyRefreshToken: "durable-refresh-token",
		WorkBuddyUID:          "uid-1",
		Token:                 "legacy-token",
		RefreshToken:          "legacy-refresh",
		ClientCookie:          "legacy-client-cookie",
	}
	out := RedactWorkBuddyOutput(acc)
	testutil.False(t, out == nil, "RedactWorkBuddyOutput() = nil")
	testutil.Equal(t, out.WorkBuddyRefreshToken, "")
	testutil.Equal(t, out.RefreshToken, "")
	testutil.Equal(t, out.Token, "")
	testutil.Equal(t, out.WorkBuddyAccessToken, "visible-access-token")
	testutil.Equal(t, out.WorkBuddyUID, "uid-1")
	testutil.Equal(t, acc.WorkBuddyRefreshToken, "durable-refresh-token")
}

func TestWorkBuddyCredentialKey_UsesDurableToken(t *testing.T) {
	t.Parallel()

	key := WorkBuddyCredentialKey(&store.Account{
		AccountType:           "workbuddy",
		WorkBuddyAccessToken:  "access",
		WorkBuddyRefreshToken: "refresh",
	})
	testutil.Equal(t, key, "workbuddy:refresh")

	accessOnly := WorkBuddyCredentialKey(&store.Account{WorkBuddyAccessToken: "access-only"})
	testutil.Equal(t, accessOnly, "workbuddy:access-only")

	testutil.Equal(t, WorkBuddyCredentialKey(&store.Account{}), "")
}

func TestVerifyWorkBuddyAccount_RequiresCredential(t *testing.T) {
	t.Parallel()

	_, httpStatus, err := verifyWorkBuddyAccountWithStore(context.Background(), &store.Account{AccountType: "workbuddy"}, &config.Config{}, nil)
	testutil.False(t, err == nil, "expected an error for a credential-less account")
	testutil.Equal(t, httpStatus, 400)
}

func TestPreserveWorkBuddyCredentialsOnEdit_KeepsServerSideState(t *testing.T) {
	t.Parallel()

	existing := &store.Account{
		WorkBuddyAccessToken:    "stored-access",
		WorkBuddyRefreshToken:   "stored-refresh",
		WorkBuddyUID:            "stored-uid",
		WorkBuddyModelIDs:       []string{"hy3", "default-model", "gpt-6-astra", "kimi-k3"},
		WorkBuddyModelsSyncedAt: time.Now(),
	}
	edited := &store.Account{AccountType: "workbuddy"}

	PreserveWorkBuddyCredentialsOnEdit(edited, existing)

	testutil.Equal(t, edited.WorkBuddyAccessToken, "stored-access")
	testutil.Equal(t, edited.WorkBuddyRefreshToken, "stored-refresh")
	testutil.Equal(t, len(edited.WorkBuddyModelIDs), 4)
	testutil.False(t, edited.WorkBuddyModelsSyncedAt.IsZero(), "WorkBuddyModelsSyncedAt was reset")
}

func TestBuildQuotaResponseFields_WorkBuddyKeepsRemainingSemantics(t *testing.T) {
	t.Parallel()

	acc := &store.Account{
		AccountType:  "workbuddy",
		UsageLimit:   350,
		UsageCurrent: 147.28,
		WorkBuddyQuota: store.WorkBuddyQuotaSnapshot{
			Limit:             350,
			Remaining:         147.28,
			Used:              202.72,
			PackageRemaining:  147.28,
			LastConsumedUnits: 52,
			PackageName:       "Free Plan Subscription",
			Unit:              "credit",
			ResetAt:           time.Date(2026, 9, 26, 0, 13, 42, 0, time.UTC),
			SyncedAt:          time.Now(),
		},
	}

	fields := buildQuotaResponseFields(acc)
	testutil.Equal(t, fields["quota_limit"], 350.0)
	testutil.Equal(t, fields["quota_remaining"], 147.28)
	// UsageCurrent stores REMAINING for this channel; "used" must come from the
	// meter snapshot, never from reading UsageCurrent as used.
	testutil.Equal(t, fields["quota_used"], 202.72)
	testutil.Equal(t, fields["quota_supported"], true)
	testutil.Equal(t, fields["quota_plan"], "Free Plan Subscription")
	testutil.Equal(t, fields["quota_unit"], "credit")
	testutil.Equal(t, fields["quota_consumed_units"], 52)
	_, ok := fields["quota_reset_at"]
	testutil.False(t, !ok, "quota_reset_at is missing")
}

func TestBuildQuotaResponseFields_WorkBuddyWithoutMeterIsUnsupported(t *testing.T) {
	t.Parallel()

	fields := buildQuotaResponseFields(&store.Account{AccountType: "workbuddy"})
	testutil.Equal(t, fields["quota_supported"], false)
	testutil.Equal(t, fields["quota_mode"], "unknown")
}

// workBuddyAPIServer stubs the three upstream endpoints an account sync touches:
// the cli model catalog, the credit meter and the account profile.
func workBuddyAPIServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/config":
			_, _ = w.Write([]byte(`{"code":0,"data":{"models":[
				{"id":"hy3","name":"HY3"},
				{"id":"default-model","name":"Default"}
			],"agents":[{"name":"cli","models":["default-model","hy3"]}]}}`))
		case "/v2/billing/meter/get-user-resource":
			_, _ = w.Write([]byte(`{"code":0,"data":{"Response":{"Data":{"TotalCount":1,"Accounts":[{
				"PackageName":"Free Plan Subscription",
				"CapacityUnit":"credit",
				"CapacitySize":250,"CapacityRemain":47,
				"CapacityRemainPrecise":"47.28",
				"CycleCapacitySize":250,"CycleCapacityRemain":47,
				"CycleCapacitySizePrecise":"250","CycleCapacityRemainPrecise":"47.28",
				"CycleEndTime":"2026-09-26 00:13:42","Status":0}]}}}}`))
		case "/v2/plugin/login/account":
			_, _ = w.Write([]byte(`{"code":0,"data":{"uid":"uid-abc","nickname":"operator@example.com"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

// TestRefreshAccountState_WorkBuddySyncsModelsAndQuota is the regression guard
// for the account table's 等级/配额 columns: without this sync the meter snapshot
// never reaches the API response and both columns render empty.
func TestRefreshAccountState_WorkBuddySyncsModelsAndQuota(t *testing.T) {
	srv := workBuddyAPIServer(t)
	defer srv.Close()

	a := New(nil, "", "", &config.Config{WorkBuddyBaseURL: srv.URL})
	acc := &store.Account{
		ID:                    3,
		AccountType:           "workbuddy",
		WorkBuddyAccessToken:  "access-token",
		WorkBuddyRefreshToken: "refresh-token",
		Enabled:               true,
	}

	status, httpStatus, err := a.refreshAccountState(context.Background(), acc)
	testutil.NoError(t, err, "refreshAccountState() error = %v")
	testutil.Equal(t, status, "")
	testutil.Equal(t, httpStatus, 0)

	testutil.Equal(t, len(acc.WorkBuddyModelIDs), 2)
	testutil.Equal(t, acc.UsageLimit, 250)
	testutil.Equal(t, acc.UsageCurrent, 47.28)
	testutil.Equal(t, acc.WorkBuddyQuota.PackageName, "Free Plan Subscription")
	testutil.False(t, acc.WorkBuddyQuota.SyncedAt.IsZero(), "quota snapshot has no timestamp; the UI cannot tell when to re-sync")

	// The columns are rendered from the API response, so assert on that shape.
	fields := buildQuotaResponseFields(acc)
	testutil.Equal(t, fields["quota_supported"], true)
	testutil.Equal(t, fields["quota_remaining"], 47.28)
	testutil.Equal(t, fields["quota_limit"], 250.0)
	testutil.Equal(t, fields["quota_plan"], "Free Plan Subscription")
	used, ok := fields["quota_used"].(float64)
	testutil.Falsef(t, !ok || math.Abs(used-202.72) > 0.01, "quota_used = %v, want the derived consumption (202.72)", fields["quota_used"])
}

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

// TestHandleAccounts_WorkBuddyRowCarriesTierAndQuota is the end-to-end guard for
// the account table: the list endpoint must expose the plan label, the meter
// numbers and the identity the 账号/邮箱 column renders.
func TestHandleAccounts_WorkBuddyRowCarriesTierAndQuota(t *testing.T) {
	s, _ := newTestStore(t, "wb-list:")
	ctx := context.Background()

	acc := &store.Account{
		AccountType:           "workbuddy",
		Name:                  "operator@example.com",
		Email:                 "operator@example.com",
		Enabled:               true,
		Weight:                1,
		WorkBuddyAccessToken:  "access-token",
		WorkBuddyRefreshToken: "refresh-token",
		WorkBuddyUID:          "uid-abc",
		UsageLimit:            250,
		UsageCurrent:          47.28,
		WorkBuddyQuota: store.WorkBuddyQuotaSnapshot{
			Limit:             250,
			Remaining:         47.28,
			Used:              202.72,
			LastConsumedUnits: 202,
			PackageName:       "Free Plan Subscription",
			Unit:              "credit",
			ResetAt:           time.Date(2026, 9, 26, 0, 13, 42, 0, time.UTC),
			SyncedAt:          time.Now(),
		},
	}
	testutil.NoError(t, s.CreateAccount(ctx, acc), "CreateAccount() error = %v")

	a := New(s, "", "", &config.Config{})
	rec := httptest.NewRecorder()
	a.HandleAccounts(rec, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	testutil.Equal(t, rec.Code, http.StatusOK)

	var rows []map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &rows)
	testutil.CheckNoError(t, err)
	testutil.Equal(t, len(rows), 1)
	row := rows[0]
	testutil.Equal(t, row["email"], "operator@example.com")
	for key, want := range map[string]interface{}{
		"quota_plan":      "Free Plan Subscription",
		"quota_supported": true,
		"quota_limit":     250.0,
		"quota_remaining": 47.28,
	} {
		testutil.Equal(t, row[key], want)
	}
	used, ok := row["quota_used"].(float64)
	testutil.Falsef(t, !ok || math.Abs(used-202.72) > 0.01, "quota_used = %v", row["quota_used"])
	quota, ok := row["workbuddy_quota"].(map[string]interface{})
	testutil.True(t, ok, "workbuddy_quota = %v, want the meter snapshot")
	testutil.Falsef(t, quota["package_name"] != "Free Plan Subscription" || quota["synced_at"] == nil, "workbuddy_quota = %v, want the package label and a sync timestamp", quota)
	if row["workbuddy_refresh_token"] != nil {
		t.Fatalf("the refresh token leaked into the account list: %v", row["workbuddy_refresh_token"])
	}
}

// TestHandleAccounts_PostWorkBuddyDocumentCapturesIdentityAndQuota covers the
// manual/import path: a pasted session document must populate the account
// identity (so 账号/邮箱 is filled) and carry the meter snapshot once synced.
func TestHandleAccounts_PostWorkBuddyDocumentCapturesIdentityAndQuota(t *testing.T) {
	srv := workBuddyAPIServer(t)
	defer srv.Close()

	s, _ := newTestStore(t, "wb-create:")
	a := New(s, "", "", &config.Config{WorkBuddyBaseURL: srv.URL})

	body, err := json.Marshal(map[string]interface{}{
		"account_type":  "workbuddy",
		"client_cookie": workBuddyAuthDocument,
		"enabled":       true,
		"weight":        1,
	})
	testutil.NoError(t, err, "marshal: %v")
	req := httptest.NewRequest(http.MethodPost, "/api/accounts", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.HandleAccounts(rec, req)
	testutil.Equal(t, rec.Code, http.StatusCreated)

	var row map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &row)
	testutil.CheckNoError(t, err)
	// The stubbed auth document embeds a JWT with sub/email claims.
	testutil.Equal(t, row["email"], "operator@example.com")
	testutil.Equal(t, row["workbuddy_uid"], "07ab88c8-5596-4257-8d21-e9fcbe3a3810")
	testutil.Equal(t, row["quota_plan"], "Free Plan Subscription")
	testutil.Equal(t, row["quota_remaining"], 47.28)
	testutil.Equal(t, row["quota_limit"], 250.0)
	testutil.False(t, row["workbuddy_refresh_token"] != nil, "the refresh token leaked in the create response")

	// The same values must come back from the list endpoint the table reads.
	listRec := httptest.NewRecorder()
	a.HandleAccounts(listRec, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	var rows []map[string]interface{}
	testutil.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &rows), "decode list: %v")
	testutil.Equal(t, len(rows), 1)
	testutil.Equal(t, rows[0]["quota_plan"], "Free Plan Subscription")
	testutil.Equal(t, rows[0]["email"], "operator@example.com")
}

// TestHandleAccounts_GrokRowCarriesSnapshotTimestamp covers the freshness signal
// the accounts page uses to decide whether a row needs an automatic re-sync:
// every channel that can report it must expose a synced_at the client can read.
func TestHandleAccounts_GrokRowCarriesSnapshotTimestamp(t *testing.T) {
	s, _ := newTestStore(t, "grok-list:")
	ctx := context.Background()
	syncedAt := time.Now().Add(-45 * time.Minute).UTC()

	oauth := &store.Account{
		AccountType:        "grok",
		CredentialType:     "oauth",
		GrokProvider:       "build",
		OAuthAccessToken:   "access",
		OAuthRefreshToken:  "refresh",
		Enabled:            true,
		GrokBilling:        store.GrokBillingSnapshot{SyncedAt: syncedAt},
		GrokModels:         []string{"grok-4.6"},
		GrokModelsSyncedAt: syncedAt,
	}
	for _, acc := range []*store.Account{oauth} {
		testutil.NoError(t, s.CreateAccount(ctx, acc), "CreateAccount() error = %v")
	}

	a := New(s, "", "", &config.Config{})
	rec := httptest.NewRecorder()
	a.HandleAccounts(rec, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	testutil.Equal(t, rec.Code, http.StatusOK)
	var rows []map[string]interface{}
	testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows), "decode: %v")
	testutil.Equal(t, len(rows), 1)
	row := rows[0]
	billing, ok := row["grok_billing"].(map[string]interface{})
	if !ok || billing["synced_at"] == nil {
		t.Fatalf("Grok Build row is missing grok_billing.synced_at: %v", row["grok_billing"])
	}
	testutil.False(t, row["grok_models_synced_at"] == nil, "Grok Build row is missing grok_models_synced_at")
}

// TestAccountStatusReasonIsExposed covers the "未授权 with no explanation" problem:
// the account list must carry the operator-facing reason next to the status code.
func TestAccountStatusReasonIsExposed(t *testing.T) {
	s, _ := newTestStore(t, "status-reason:")
	ctx := context.Background()

	acc := &store.Account{
		AccountType:    "grok",
		CredentialType: "oauth",
		GrokProvider:   "build",
		Enabled:        true,
		StatusCode:     "401",
		StatusMessage:  "上游已不接受该 OAuth 授权（refresh token 被拒绝），需要重新登录",
		LastAttempt:    time.Now(),
	}
	testutil.NoError(t, s.CreateAccount(ctx, acc), "CreateAccount() error = %v")

	a := New(s, "", "", &config.Config{})
	rec := httptest.NewRecorder()
	a.HandleAccounts(rec, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	testutil.Equal(t, rec.Code, http.StatusOK)
	var rows []map[string]interface{}
	testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows), "decode: %v")
	testutil.Equal(t, len(rows), 1)
	testutil.Equal(t, rows[0]["status_code"], "401")
	got, _ := rows[0]["status_message"].(string)
	testutil.False(t, got == "", "status_message is missing; the UI can only show a bare 401")

	// A successful refresh must clear both the code and the stale reason.
	acc.StatusCode = ""
	acc.StatusMessage = ""
	testutil.NoError(t, s.UpdateAccount(ctx, acc), "UpdateAccount() error = %v")
	rec2 := httptest.NewRecorder()
	a.HandleAccounts(rec2, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	// Decode into a fresh slice: reusing it would keep keys the new payload omits.
	var cleared []map[string]interface{}
	testutil.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &cleared), "decode: %v")
	testutil.Equal(t, len(cleared), 1)
	testutil.Equal(t, cleared[0]["status_code"], "")
	reason := cleared[0]["status_message"]
	testutil.Falsef(t, reason != nil && reason != "", "status_message = %v, want cleared with the status", reason)
}

func TestResolveCredentials_MatchesClientResolution(t *testing.T) {
	t.Parallel()

	acc := &store.Account{ClientCookie: workBuddyAuthDocument}
	apiCreds := resolveWorkBuddyCredentials(acc)
	clientCreds := workbuddy.ResolveCredentials(acc)

	testutil.Equal(t, apiCreds.AccessToken, clientCreds.AccessToken)
	testutil.Equal(t, apiCreds.RefreshToken, clientCreds.RefreshToken)
	testutil.Equal(t, apiCreds.UID, clientCreds.UID)
}
