package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

// clineAuthServer stubs the two upstreams a Cline login touches: WorkOS for the
// device grant and the Cline API for the exchange and the model feed. It records
// no credential it was not asked for.
type clineAuthServer struct {
	*httptest.Server
	polls        int
	registerBody map[string]string
	host         string
}

func newClineAuthServer(t *testing.T, pollAnswers []func(w http.ResponseWriter)) *clineAuthServer {
	t.Helper()
	stub := &clineAuthServer{}
	stub.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user_management/authorize/device":
			testutil.CheckNotEqual(t, r.FormValue("client_id"), "")
			// The stub stands in for WorkOS, so it answers with its own host:
			// the client refuses to hand a browser a page on a foreign host,
			// and this test is about the console's response, not the check.
			page := "http://" + r.Host + "/device"
			_, _ = w.Write([]byte(`{"device_code":"dev-1","user_code":"ABCD-EFGH",` +
				`"verification_uri":"` + page + `",` +
				`"verification_uri_complete":"` + page + `?code=ABCD-EFGH",` +
				`"interval":5,"expires_in":900}`))
		case "/user_management/authenticate":
			index := stub.polls
			stub.polls++
			if index >= len(pollAnswers) {
				index = len(pollAnswers) - 1
			}
			pollAnswers[index](w)
		case "/api/v1/auth/register":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			stub.registerBody = body
			testutil.CheckNotEqual(t, strings.TrimSpace(body["accessToken"]), "")
			_, _ = w.Write([]byte(`{"data":{"accessToken":"cline-access-1",` +
				`"refreshToken":"cline-refresh-1","expiresAt":4102444800000,` +
				`"userInfo":{"email":"operator@example.com"}}}`))
		case "/api/v1/ai/cline/recommended-models":
			_, _ = w.Write([]byte(`{"free":[{"id":"x-ai/grok-4.1-fast","name":"Grok 4.1 Fast"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	stub.host = strings.TrimPrefix(stub.URL, "http://")
	return stub
}

func clineLoginConfig(baseURL string) *config.Config {
	return &config.Config{
		ClineAPIBaseURL:         baseURL + "/api/v1",
		ClineWorkOSAuthorizeURL: baseURL + "/user_management/authorize/device",
		ClineWorkOSTokenURL:     baseURL + "/user_management/authenticate",
	}
}

// TestHandleClineLogin_StartHidesDeviceCode proves the start response carries
// only what a browser needs: the official page and the user code. The device
// code is the half that can be exchanged for a credential and must never reach
// the console.
func TestHandleClineLogin_StartHidesDeviceCode(t *testing.T) {
	s, _ := newTestStore(t, "cline-login:")
	auth := newClineAuthServer(t, []func(w http.ResponseWriter){
		func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) },
	})
	defer auth.Close()
	a := New(s, "", "", clineLoginConfig(auth.URL))
	rec := httptest.NewRecorder()
	a.HandleClineLogin(rec, channelLoginRequest(t, http.MethodPost, "/api/cline/login", `{"enabled":true}`))
	testutil.Equal(t, rec.Code, http.StatusOK)
	var response struct {
		ID                 string `json:"id"`
		VerifyURI          string `json:"verification_uri"`
		VerifyFull         string `json:"verification_uri_complete"`
		UserCode           string `json:"user_code"`
		DeviceCode         string `json:"device_code"`
		SessionFingerprint string `json:"session_fingerprint"`
	}
	testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response), "decode response: %v")
	testutil.NotEqual(t, response.ID, "")
	if response.VerifyURI == "" || response.VerifyFull == "" {
		t.Fatalf("start returned no verification page: %+v", response)
	}
	testutil.NotEqual(t, response.UserCode, "")
	testutil.CheckEqual(t, response.DeviceCode, "")
	testutil.CheckNotContain(t, rec.Body.String(), "dev-1")
}

// TestHandleClineLogin_PollPersistsOAuthAccount drives the flow from start to a
// stored account and pins what the record holds: the Cline pair in its own
// fields, the feed snapshot, and no generic credential slot.
func TestHandleClineLogin_PollPersistsOAuthAccount(t *testing.T) {
	s, _ := newTestStore(t, "cline-login:")
	auth := newClineAuthServer(t, []func(w http.ResponseWriter){
		func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
		},
		func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"access_token":"workos-access","refresh_token":"workos-refresh"}`))
		},
	})
	defer auth.Close()
	a := New(s, "", "", clineLoginConfig(auth.URL))
	start := httptest.NewRecorder()
	a.HandleClineLogin(start, channelLoginRequest(t, http.MethodPost, "/api/cline/login", ""))
	var started struct {
		ID string `json:"id"`
	}
	testutil.NoError(t, json.Unmarshal(start.Body.Bytes(), &started), "decode start: %v")
	a.clineLogins.update(started.ID, func(login *clineLoginTransaction) { login.interval = time.Millisecond })

	var stored *store.Account
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec := httptest.NewRecorder()
		a.HandleClineLogin(rec, channelLoginRequest(t, http.MethodGet, "/api/cline/login/"+started.ID, ""))
		var state struct {
			Status string `json:"status"`
		}
		testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &state), "decode poll: %v")
		accounts, err := s.ListAccounts(t.Context())
		if err != nil {
			t.Fatalf("list accounts: %v", err)
		}
		for _, acc := range accounts {
			if strings.EqualFold(acc.AccountType, "cline") {
				stored = acc
			}
		}
		if state.Status != "pending" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stored == nil {
		t.Fatal("login completed without storing a cline account")
	}
	if stored.ClineAccessToken == "" || stored.ClineRefreshToken == "" {
		t.Fatalf("stored account is missing the cline pair: %+v", stored)
	}
	testutil.CheckEqual(t, stored.ClineEmail, "operator@example.com")
	if len(stored.ClineModelIDs) != 1 || !strings.Contains(stored.ClineModelIDs[0], "x-ai/grok-4.1-fast") {
		t.Errorf("model ids = %v, want the recommended-models feed", stored.ClineModelIDs)
	}
	if stored.ClineModelsSyncedAt.IsZero() {
		t.Error("model sync timestamp is zero after successful login catalog read")
	}
	if stored.Token != "" || stored.RefreshToken != "" || stored.ClientCookie != "" {
		t.Errorf("stored account wrote a generic credential slot: %+v", stored)
	}
	testutil.CheckEqual(t, auth.registerBody["accessToken"], "workos-access")
}

// TestHandleClineLogin_RejectsManualCredential pins the OAuth-only contract: a
// Cline account cannot be created by pasting a token.
func TestHandleClineLogin_RejectsManualCredential(t *testing.T) {
	s, _ := newTestStore(t, "cline-manual:")
	a := New(s, "", "", &config.Config{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/accounts", strings.NewReader(
		`{"account_type":"cline","name":"manual","cline_access_token":"access","cline_refresh_token":"refresh"}`))
	req.Header.Set("Content-Type", "application/json")
	a.HandleAccounts(rec, req)
	testutil.Equal(t, rec.Code, http.StatusBadRequest)
	testutil.CheckContain(t, strings.ToLower(rec.Body.String()), "cline/login")
	accounts, err := s.ListAccounts(t.Context())
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	testutil.Equal(t, len(accounts), 0)
}

// TestHandleClineLogin_AccountOutputHidesRefreshToken proves the durable
// credential is write-only: the account API may show that a credential exists
// but must not return it.
func TestHandleClineLogin_AccountOutputHidesRefreshToken(t *testing.T) {
	s, _ := newTestStore(t, "cline-output:")
	acc := &store.Account{
		Name:              "cline@example.com",
		AccountType:       "cline",
		ClineAccessToken:  "access-secret",
		ClineRefreshToken: "refresh-secret",
	}
	testutil.NoError(t, s.CreateAccount(t.Context(), acc), "create account: %v")
	out := normalizeAccountOutput(acc)
	if out == nil || out.Account == nil {
		t.Fatal("normalizeAccountOutput returned nothing")
	}
	testutil.CheckEqual(t, out.Account.ClineRefreshToken, "")
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	testutil.CheckNotContain(t, string(raw), "refresh-secret")
}
