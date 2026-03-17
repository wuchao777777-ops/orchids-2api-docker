package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/orchids"
	"orchids-api/internal/store"
)

func TestRefreshAccountState_GrokSyncsRemainingQuota(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/rate-limits" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"remainingQueries": 0,
			"totalQueries":     0,
			"remainingTokens":  80,
			"totalTokens":      80,
		})
	}))
	defer srv.Close()

	cfg := &config.Config{GrokAPIBaseURL: srv.URL}
	a := New(nil, "", "", cfg)
	acc := &store.Account{
		ID:           1,
		AccountType:  "grok",
		ClientCookie: "token-abc",
		AgentMode:    "grok-3",
	}

	status, httpStatus, err := a.refreshAccountState(context.Background(), acc)
	if err != nil {
		t.Fatalf("refreshAccountState() error: %v", err)
	}
	if status != "" || httpStatus != 0 {
		t.Fatalf("unexpected status=%q httpStatus=%d", status, httpStatus)
	}
	if acc.UsageCurrent != 80 || acc.UsageLimit != 80 {
		t.Fatalf("unexpected quota current=%v limit=%v", acc.UsageCurrent, acc.UsageLimit)
	}
}

func TestRefreshAccountState_GrokMissingToken(t *testing.T) {
	t.Parallel()

	a := New(nil, "", "", &config.Config{})
	acc := &store.Account{AccountType: "grok"}

	status, httpStatus, err := a.refreshAccountState(context.Background(), acc)
	if err == nil {
		t.Fatalf("expected error")
	}
	if status != "" {
		t.Fatalf("unexpected status=%q", status)
	}
	if httpStatus != http.StatusBadRequest {
		t.Fatalf("httpStatus=%d want=%d", httpStatus, http.StatusBadRequest)
	}
}

func TestRefreshAccountState_OrchidsUsesTokenGetterAndCreditsFetcher(t *testing.T) {
	prevGetToken := orchidsGetAccountToken
	prevFetchCredits := orchidsFetchCredits
	t.Cleanup(func() {
		orchidsGetAccountToken = prevGetToken
		orchidsFetchCredits = prevFetchCredits
	})

	orchidsGetAccountToken = func(acc *store.Account, cfg *config.Config) (string, error) {
		if acc.ClientCookie != "client-cookie" {
			t.Fatalf("unexpected client cookie: %q", acc.ClientCookie)
		}
		return "header." + encodeJWTClaims(`{"sid":"sess_123","sub":"user_123","exp":4102444800}`) + ".sig", nil
	}
	orchidsFetchCredits = func(ctx context.Context, sessionJWT string, userID string, proxyFunc func(*http.Request) (*url.URL, error)) (*orchids.CreditsInfo, error) {
		if userID != "user_123" {
			t.Fatalf("userID=%q want user_123", userID)
		}
		return &orchids.CreditsInfo{Credits: 42, Plan: "PRO"}, nil
	}

	a := New(nil, "", "", &config.Config{})
	acc := &store.Account{AccountType: "orchids", ClientCookie: "client-cookie"}

	status, httpStatus, err := a.refreshAccountState(context.Background(), acc)
	if err != nil {
		t.Fatalf("refreshAccountState() error: %v", err)
	}
	if status != "" || httpStatus != 0 {
		t.Fatalf("unexpected status=%q httpStatus=%d", status, httpStatus)
	}
	if acc.SessionID != "sess_123" {
		t.Fatalf("SessionID=%q want sess_123", acc.SessionID)
	}
	if acc.UserID != "user_123" {
		t.Fatalf("UserID=%q want user_123", acc.UserID)
	}
	if acc.Subscription != "pro" || acc.UsageCurrent != 42 || acc.UsageLimit != orchids.PlanCreditLimit("PRO") {
		t.Fatalf("unexpected orchids quota sync: subscription=%q current=%v limit=%v", acc.Subscription, acc.UsageCurrent, acc.UsageLimit)
	}
}

func TestRefreshAccountState_OrchidsTokenRefreshFailureMarksUnauthorized(t *testing.T) {
	prevGetToken := orchidsGetAccountToken
	t.Cleanup(func() {
		orchidsGetAccountToken = prevGetToken
	})

	orchidsGetAccountToken = func(acc *store.Account, cfg *config.Config) (string, error) {
		return "", errors.New("no active sessions found")
	}

	a := New(nil, "", "", &config.Config{})
	acc := &store.Account{AccountType: "orchids", SessionCookie: "session-cookie"}

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

func TestRefreshAccountState_OrchidsTokenRefreshFailureWithQuotaFallbackReturnsSuccess(t *testing.T) {
	prevGetToken := orchidsGetAccountToken
	prevFetchCredits := orchidsFetchCredits
	t.Cleanup(func() {
		orchidsGetAccountToken = prevGetToken
		orchidsFetchCredits = prevFetchCredits
	})

	sessionJWT := encodeJWT(`{"sid":"sess_quota","sub":"user_quota","exp":4102444800}`)
	orchidsGetAccountToken = func(acc *store.Account, cfg *config.Config) (string, error) {
		return "", errors.New("unexpected status code 429: too many requests")
	}
	orchidsFetchCredits = func(ctx context.Context, sessionJWTArg string, userID string, proxyFunc func(*http.Request) (*url.URL, error)) (*orchids.CreditsInfo, error) {
		if sessionJWTArg != sessionJWT {
			t.Fatalf("sessionJWT=%q want %q", sessionJWTArg, sessionJWT)
		}
		if userID != "user_quota" {
			t.Fatalf("userID=%q want user_quota", userID)
		}
		return &orchids.CreditsInfo{Credits: 321, Plan: "PRO"}, nil
	}

	a := New(nil, "", "", &config.Config{})
	acc := &store.Account{
		AccountType:   "orchids",
		SessionCookie: sessionJWT,
	}

	status, httpStatus, err := a.refreshAccountState(context.Background(), acc)
	if err != nil {
		t.Fatalf("refreshAccountState() error: %v", err)
	}
	if status != "" {
		t.Fatalf("status=%q want empty", status)
	}
	if httpStatus != 0 {
		t.Fatalf("httpStatus=%d want 0", httpStatus)
	}
	if acc.SessionID != "sess_quota" {
		t.Fatalf("SessionID=%q want sess_quota", acc.SessionID)
	}
	if acc.UserID != "user_quota" {
		t.Fatalf("UserID=%q want user_quota", acc.UserID)
	}
	if acc.Subscription != "pro" || acc.UsageCurrent != 321 || acc.UsageLimit != orchids.PlanCreditLimit("PRO") {
		t.Fatalf("unexpected orchids quota sync: subscription=%q current=%v limit=%v", acc.Subscription, acc.UsageCurrent, acc.UsageLimit)
	}
}

func TestShouldSyncAccountOnCreate(t *testing.T) {
	t.Parallel()

	if shouldSyncAccountOnCreate(&store.Account{AccountType: "orchids"}) {
		t.Fatal("expected Orchids account create to skip initial sync")
	}
	if !shouldSyncAccountOnCreate(&store.Account{AccountType: "grok"}) {
		t.Fatal("expected non-Orchids account create to keep initial sync")
	}
	if shouldSyncAccountOnCreate(nil) {
		t.Fatal("nil account should not sync on create")
	}
}

func TestApplyAccountStatusFromError(t *testing.T) {
	t.Parallel()

	acc := &store.Account{}
	applyAccountStatusFromError(acc, errors.New("unexpected status code 429: too many requests"))
	if acc.StatusCode != "429" {
		t.Fatalf("StatusCode=%q want 429", acc.StatusCode)
	}
	if acc.LastAttempt.IsZero() {
		t.Fatal("LastAttempt should be set")
	}

	unknown := &store.Account{}
	applyAccountStatusFromError(unknown, errors.New("plain failure"))
	if unknown.StatusCode != "" {
		t.Fatalf("StatusCode=%q want empty", unknown.StatusCode)
	}

	noActiveSession := &store.Account{}
	applyAccountStatusFromError(noActiveSession, errors.New("no active sessions found"))
	if noActiveSession.StatusCode != "401" {
		t.Fatalf("StatusCode=%q want 401", noActiveSession.StatusCode)
	}
}

func TestNormalizeOrchidsCredentialInput_SessionJWT(t *testing.T) {
	t.Parallel()

	sessionJWT := encodeJWT(`{"sid":"sess_abc","sub":"user_xyz","exp":4102444800}`)
	acc := &store.Account{
		AccountType:  "orchids",
		ClientCookie: sessionJWT,
	}

	if err := normalizeOrchidsCredentialInput(acc); err != nil {
		t.Fatalf("normalizeOrchidsCredentialInput() error = %v", err)
	}
	if acc.Token != sessionJWT {
		t.Fatalf("Token=%q want %q", acc.Token, sessionJWT)
	}
	if acc.SessionCookie != sessionJWT {
		t.Fatalf("SessionCookie=%q want %q", acc.SessionCookie, sessionJWT)
	}
	if acc.ClientCookie != "" {
		t.Fatalf("ClientCookie=%q want empty", acc.ClientCookie)
	}
	if acc.SessionID != "sess_abc" || acc.UserID != "user_xyz" {
		t.Fatalf("unexpected session info sid=%q user=%q", acc.SessionID, acc.UserID)
	}
}

func TestNormalizeOrchidsCredentialInput_CookieJSON(t *testing.T) {
	t.Parallel()

	sessionJWT := encodeJWT(`{"sid":"sess_json","sub":"user_json","exp":4102444800}`)
	acc := &store.Account{
		AccountType: "orchids",
		ClientCookie: `[
			{"name":"__session","value":"` + sessionJWT + `"},
			{"name":"__client_uat","value":"1773712060"}
		]`,
	}

	if err := normalizeOrchidsCredentialInput(acc); err != nil {
		t.Fatalf("normalizeOrchidsCredentialInput() error = %v", err)
	}
	if acc.Token != sessionJWT {
		t.Fatalf("Token=%q want %q", acc.Token, sessionJWT)
	}
	if acc.SessionCookie != sessionJWT {
		t.Fatalf("SessionCookie=%q want %q", acc.SessionCookie, sessionJWT)
	}
	if acc.ClientUat != "1773712060" {
		t.Fatalf("ClientUat=%q want %q", acc.ClientUat, "1773712060")
	}
	if acc.SessionID != "sess_json" || acc.UserID != "user_json" {
		t.Fatalf("unexpected session info sid=%q user=%q", acc.SessionID, acc.UserID)
	}
}

func encodeJWTClaims(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func encodeJWT(raw string) string {
	return encodeJWTClaims(`{"alg":"none","typ":"JWT"}`) + "." + encodeJWTClaims(raw) + ".sigpayload"
}
