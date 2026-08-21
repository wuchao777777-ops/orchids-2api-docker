package grok

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
)

func TestCLIOAuthAccessTokenUnexpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("token endpoint must not be called when access token is unexpired")
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.GrokCLIOAuthTokenURL = server.URL
	oauth := NewCLIOAuth(cfg, server.Client())
	acc := &store.Account{
		OAuthAccessToken:  "existing-token",
		OAuthRefreshToken: "refresh",
		OAuthExpiresAt:    time.Now().Add(time.Hour),
	}
	token, err := oauth.AccessToken(context.Background(), acc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "existing-token" {
		t.Fatalf("token = %q", token)
	}
}

func TestCLIOAuthAccessTokenRefreshes(t *testing.T) {
	var called int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Fatalf("grant_type = %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("refresh_token") != "old-refresh" {
			t.Fatalf("refresh_token = %q", r.Form.Get("refresh_token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.GrokCLIOAuthTokenURL = server.URL
	oauth := NewCLIOAuth(cfg, server.Client())
	acc := &store.Account{
		OAuthAccessToken:  "stale-token",
		OAuthRefreshToken: "old-refresh",
		OAuthExpiresAt:    time.Now().Add(-time.Hour), // expired → forces refresh
	}
	token, err := oauth.AccessToken(context.Background(), acc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != 1 {
		t.Fatalf("expected exactly 1 refresh call, got %d", called)
	}
	if token != "new-access" {
		t.Fatalf("token = %q", token)
	}
	if acc.OAuthAccessToken != "new-access" {
		t.Fatalf("persisted access = %q", acc.OAuthAccessToken)
	}
	if acc.OAuthRefreshToken != "new-refresh" {
		t.Fatalf("persisted refresh (rotated) = %q", acc.OAuthRefreshToken)
	}
}

func TestCLIOAuthRefreshDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"token expired"}`))
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.GrokCLIOAuthTokenURL = server.URL
	oauth := NewCLIOAuth(cfg, server.Client())
	acc := &store.Account{
		OAuthAccessToken:  "stale",
		OAuthRefreshToken: "dead-refresh",
		OAuthExpiresAt:    time.Now().Add(-time.Minute),
	}
	_, err := oauth.AccessToken(context.Background(), acc)
	if err == nil {
		t.Fatal("expected error for denied refresh")
	}
	if !IsCLIPermanentOAuthError(err) {
		t.Fatalf("invalid_grant should be permanent (401): %v", err)
	}
}

func TestCLIOAuthMissingRefreshToken(t *testing.T) {
	cfg := &config.Config{}
	oauth := NewCLIOAuth(cfg, nil)
	acc := &store.Account{OAuthAccessToken: "", OAuthRefreshToken: "", OAuthExpiresAt: time.Time{}}
	if _, err := oauth.AccessToken(context.Background(), acc); err == nil {
		t.Fatal("expected error when no token present")
	}
}

func TestCLIOAuthRefreshServerErrorTransient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.GrokCLIOAuthTokenURL = server.URL
	oauth := NewCLIOAuth(cfg, server.Client())
	acc := &store.Account{OAuthRefreshToken: "r", OAuthExpiresAt: time.Now().Add(-time.Minute)}
	_, err := oauth.AccessToken(context.Background(), acc)
	if err == nil {
		t.Fatal("expected error")
	}
	if IsCLIPermanentOAuthError(err) {
		t.Fatalf("5xx should be transient, got permanent: %v", err)
	}
}

func TestCLIOAuthErrorStatus(t *testing.T) {
	e := &cliOAuthError{status: http.StatusUnauthorized, message: "denied"}
	if e.Status() != "401" {
		t.Fatalf("status = %q", e.Status())
	}
	zero := &cliOAuthError{}
	if zero.Status() != "" {
		t.Fatalf("zero status = %q", zero.Status())
	}
	if !strings.Contains(e.Error(), "denied") {
		t.Fatalf("error message = %q", e.Error())
	}
}

func TestCLIOAuthAccessTokenPersistsToStore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"stored-access","refresh_token":"stored-refresh","expires_in":3600}`))
	}))
	defer server.Close()

	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{StoreMode: "redis", RedisAddr: mini.Addr(), RedisDB: 0, RedisPrefix: "test:"})
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		mini.Close()
	})

	acc := &store.Account{
		AccountType:       "grok",
		CredentialType:    "oauth",
		OAuthAccessToken:  "stale",
		OAuthRefreshToken: "old-refresh",
		OAuthExpiresAt:    time.Now().Add(-time.Minute),
		Enabled:           true,
	}
	if err := s.CreateAccount(context.Background(), acc); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	cfg := &config.Config{}
	cfg.GrokCLIOAuthTokenURL = server.URL
	oauth := NewCLIOAuth(cfg, server.Client())
	oauth.SetAccountStore(s)

	token, err := oauth.AccessToken(context.Background(), acc)
	if err != nil {
		t.Fatalf("AccessToken() error = %v", err)
	}
	if token != "stored-access" {
		t.Fatalf("token=%q", token)
	}

	got, err := s.GetAccount(context.Background(), acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if got.OAuthAccessToken != "stored-access" {
		t.Fatalf("stored access=%q", got.OAuthAccessToken)
	}
	if got.OAuthRefreshToken != "stored-refresh" {
		t.Fatalf("stored refresh=%q", got.OAuthRefreshToken)
	}
}
