package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"encoding/json"

	"orchids-api/internal/config"
	"orchids-api/internal/loadbalancer"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

func TestHandleMessages_403MarksAccountBlocked(t *testing.T) {
	s := newTestRedisStore(t, "test:")

	acc := &store.Account{
		Name:         "workbuddy-1",
		AccountType:  "workbuddy",
		RefreshToken: "rt",
		Enabled:      true,
		Weight:       1,
	}
	testutil.NoError(t, s.CreateAccount(context.Background(), acc), "CreateAccount() error = %v")

	publishModel(t, s, &store.Model{Channel: "WorkBuddy", ModelID: "claude-opus-5"})

	lb := loadbalancer.NewWithCacheTTL(s, 0)
	h := NewWithLoadBalancer(&config.Config{
		DebugEnabled:   false,
		RequestTimeout: 10,
		MaxRetries:     0,
	}, lb)
	upstreamCalls := 0
	h.SetClientFactory(func(acc *store.Account, cfg *config.Config) UpstreamClient {
		upstreamCalls++
		return &errorUpstreamEdge{err: errors.New("workbuddy stream request failed: HTTP 403")}
	})

	payload := map[string]any{
		"model":    "claude-opus-5",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"system":   []any{},
		"stream":   false,
	}
	body, _ := json.Marshal(payload)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/workbuddy/v1/messages", bytes.NewReader(body))
	h.HandleMessages(rec, req)

	updated, err := s.GetAccount(context.Background(), acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	testutil.Equal(t, updated.StatusCode, "403")
	if updated.LastAttempt.IsZero() {
		t.Fatal("expected last_attempt to be set")
	}
	testutil.Equal(t, upstreamCalls, 1)

	payload["messages"] = []map[string]any{{"role": "user", "content": "hi again"}}
	body2, _ := json.Marshal(payload)
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "http://x/workbuddy/v1/messages", bytes.NewReader(body2))
	h.HandleMessages(rec2, req2)
	// The pool's own note ("no enabled accounts available for channel: workbuddy") is a
	// diagnostic and stays in the log: the client is told the pool cannot serve the
	// request, in words that do not read like the channel is empty or the caller
	// sent something wrong.
	testutil.Equal(t, rec2.Code, http.StatusServiceUnavailable)
	secondBody := rec2.Body.String()
	testutil.MustContain(t, secondBody, "no account in this channel can serve the request")
	testutil.MustNotContain(t, secondBody, "no enabled accounts available for channel")
	testutil.Equal(t, upstreamCalls, 1)
}
