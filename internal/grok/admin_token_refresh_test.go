package grok

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orchids-api/internal/store"
)

func TestCollectRefreshTokens(t *testing.T) {
	req := adminTokenRefreshRequest{
		Token:  "sso=t0; Path=/",
		Tokens: []string{"t1", "sso=t1", "  t2  "},
	}
	got := collectRefreshTokens(req)
	if len(got) != 3 {
		t.Fatalf("collectRefreshTokens len=%d want=3", len(got))
	}
	want := []string{"t0", "t1", "t2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("collectRefreshTokens[%d]=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestCollectRefreshTokens_Empty(t *testing.T) {
	if got := collectRefreshTokens(adminTokenRefreshRequest{}); len(got) != 0 {
		t.Fatalf("collectRefreshTokens empty len=%d want=0", len(got))
	}
}

func TestHandleAdminTokensRefresh_MethodNotAllowed(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tokens/refresh", strings.NewReader(""))
	rec := httptest.NewRecorder()
	h.HandleAdminTokensRefresh(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want=%d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleAdminTokensRefreshAsync_MethodNotAllowed(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tokens/refresh/async", strings.NewReader(""))
	rec := httptest.NewRecorder()
	h.HandleAdminTokensRefreshAsync(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want=%d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestUpdateGrokUsageAccount_SuccessClearsStatus(t *testing.T) {
	resetAt := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	acc := &store.Account{
		StatusCode:   "429",
		LastAttempt:  time.Now().Add(-time.Minute),
		UsageLimit:   1,
		UsageCurrent: 1,
	}
	info := &RateLimitInfo{
		Limit:        120,
		HasLimit:     true,
		Remaining:    25,
		HasRemaining: true,
		ResetAt:      resetAt,
	}

	updateGrokUsageAccount(acc, info, "")

	if acc.StatusCode != "" {
		t.Fatalf("status=%q want empty", acc.StatusCode)
	}
	if !acc.LastAttempt.IsZero() {
		t.Fatalf("last_attempt=%v want zero", acc.LastAttempt)
	}
	if acc.UsageLimit != 120 {
		t.Fatalf("usage_limit=%v want=120", acc.UsageLimit)
	}
	if acc.UsageCurrent != 25 {
		t.Fatalf("usage_current=%v want=25", acc.UsageCurrent)
	}
	if !acc.QuotaResetAt.Equal(resetAt) {
		t.Fatalf("quota_reset_at=%v want=%v", acc.QuotaResetAt, resetAt)
	}
}

func TestUpdateGrokUsageAccount_IncompleteInfoDoesNotOverwriteQuota(t *testing.T) {
	acc := &store.Account{
		UsageLimit:   80,
		UsageCurrent: 12,
	}
	info := &RateLimitInfo{
		Limit:    120,
		HasLimit: true,
	}

	updateGrokUsageAccount(acc, info, "")

	if acc.UsageLimit != 80 || acc.UsageCurrent != 12 {
		t.Fatalf("quota should remain unchanged on incomplete info, got limit=%v current=%v", acc.UsageLimit, acc.UsageCurrent)
	}
}

func TestUpdateGrokUsageAccount_FailureSetsStatusAndAttempt(t *testing.T) {
	acc := &store.Account{}
	updateGrokUsageAccount(acc, nil, "500")

	if acc.StatusCode != "500" {
		t.Fatalf("status=%q want=500", acc.StatusCode)
	}
	if acc.LastAttempt.IsZero() {
		t.Fatalf("last_attempt should be set on failure")
	}
}

func TestUpdateGrokUsageAccount_RemainingOnlyUsesDefaultQuota(t *testing.T) {
	acc := &store.Account{Subscription: "super"}
	info := &RateLimitInfo{
		Remaining:    118,
		HasRemaining: true,
	}

	updateGrokUsageAccount(acc, info, "")

	if acc.UsageLimit != 140 {
		t.Fatalf("usage_limit=%v want=140", acc.UsageLimit)
	}
	if acc.UsageCurrent != 118 {
		t.Fatalf("usage_current=%v want=118", acc.UsageCurrent)
	}
}
