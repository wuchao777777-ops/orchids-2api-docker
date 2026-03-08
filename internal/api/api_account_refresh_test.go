package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goccy/go-json"

	"orchids-api/internal/config"
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
