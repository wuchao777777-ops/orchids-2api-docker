package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"encoding/json"
	"github.com/alicebob/miniredis/v2"

	"orchids-api/internal/config"
	"orchids-api/internal/loadbalancer"
	"orchids-api/internal/store"
)

// planCooldownHandler builds the smallest live pool that can answer a Qoder
// request: one enabled account carrying a cooldown for the requested model, and a
// store that publishes that model.
func planCooldownHandler(t *testing.T, model string, cool func(acc *store.Account)) (*Handler, int) {
	t.Helper()
	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{RedisAddr: mini.Addr(), RedisDB: 0, RedisPrefix: "plancooldown:"})
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		mini.Close()
	})

	ctx := context.Background()
	acc := &store.Account{Name: "qoder-1", AccountType: "qoder", QoderRefreshToken: "rt", Enabled: true, Weight: 1}
	if err := s.CreateAccount(ctx, acc); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	cool(acc)
	if err := s.UpdateAccount(ctx, acc); err != nil {
		t.Fatalf("UpdateAccount() error = %v", err)
	}

	publishModel(t, s, &store.Model{Channel: "Qoder", ModelID: model, BillingTier: "free", BillingSource: "qoder_price_factor"})

	h := NewWithLoadBalancer(&config.Config{DebugEnabled: false, RequestTimeout: 10, MaxRetries: 1}, loadbalancer.NewWithCacheTTL(s, 0))
	calls := 0
	h.SetClientFactory(func(*store.Account, *config.Config) UpstreamClient {
		calls++
		return &errorUpstreamEdge{err: errors.New("the request should never reach the upstream")}
	})
	t.Cleanup(func() { _ = s.Close() })
	return h, calls
}

func requestModel(t *testing.T, h *Handler, model string) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{
		"model":    model,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   false,
	}
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/qoder/v1/chat/completions", bytes.NewReader(body))
	h.HandleMessages(rec, req)
	return rec
}

// TestPlanCooldownAnswersModelUnavailable is the regression test for the pool
// that answered a plan verdict with "the requested model is temporarily
// rate-limited".
//
// The cooldown was recorded by an entitlement refusal (Qoder business code 112:
// the account's plan does not cover this model) and held for a day. The selection
// layer could see the deadline but not the verdict behind it, so every request
// for that model was answered with an invitation to retry a condition that no
// amount of waiting changes.
func TestPlanCooldownAnswersModelUnavailable(t *testing.T) {
	h, upstreamCalls := planCooldownHandler(t, "glm-5.3", func(acc *store.Account) {
		store.RecordModelCooldownWithReason(acc, "glm-5.3", time.Now().Add(24*time.Hour), store.ModelCooldownUnavailable)
	})

	rec := requestModel(t, h, "glm-5.3")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not available on this channel's accounts") {
		t.Fatalf("body = %q, want the model-unavailable answer", rec.Body.String())
	}
	if upstreamCalls != 0 {
		t.Fatalf("upstreamCalls = %d, want 0: the request must be refused at selection", upstreamCalls)
	}
}

// TestLegacyPlanCooldownIsReadFromItsDeadline covers the accounts that already
// carry a cooldown recorded before the reason was stored. The 27h deadline on
// production account 16 is exactly this shape, and the deadline is the only
// signal left: the two verdicts that create a model cooldown hold for 30 seconds
// and for a day, so a day-long hold cannot be a throttle.
func TestLegacyPlanCooldownIsReadFromItsDeadline(t *testing.T) {
	h, upstreamCalls := planCooldownHandler(t, "ultimate", func(acc *store.Account) {
		acc.ModelCooldowns = map[string]time.Time{"ultimate": time.Now().Add(27 * time.Hour)}
	})

	rec := requestModel(t, h, "ultimate")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if upstreamCalls != 0 {
		t.Fatalf("upstreamCalls = %d, want 0", upstreamCalls)
	}
}

// TestThrottledModelCooldownStaysRetryable guards the other direction: a
// short throttle must keep the retryable answer, or a model that is merely busy
// would be reported as permanently unavailable.
func TestThrottledModelCooldownStaysRetryable(t *testing.T) {
	h, upstreamCalls := planCooldownHandler(t, "efficient", func(acc *store.Account) {
		store.RecordModelCooldownWithReason(acc, "efficient", time.Now().Add(30*time.Second), store.ModelCooldownThrottled)
	})

	rec := requestModel(t, h, "efficient")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cooling down") {
		t.Fatalf("body = %q, want the cooling-down answer", rec.Body.String())
	}
	if upstreamCalls != 0 {
		t.Fatalf("upstreamCalls = %d, want 0", upstreamCalls)
	}
}
