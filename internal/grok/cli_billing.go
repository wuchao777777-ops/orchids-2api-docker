package grok

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/store"
)

// CLIBillingInfo is the precise weekly Build-credit window exposed by xAI's
// official CLI billing endpoint. The API supplies percentages, not a fixed
// request allowance, so callers must not fabricate one.
type CLIBillingInfo struct {
	UsagePercent    float64
	HasUsagePercent bool
	PeriodEnd       time.Time
	Subscription    string
}

// FetchBilling reads the current Build weekly-credit window. A missing billing
// response is non-fatal to account authentication and should be represented as
// an unavailable quota rather than an invented plan or allowance.
func (c *CLIClient) FetchBilling(ctx context.Context, acc *store.Account) (*CLIBillingInfo, error) {
	if c == nil || c.oauth == nil || acc == nil {
		return nil, fmt.Errorf("grok cli billing is not configured")
	}
	ApplyCLIOAuthIdentity(acc)
	token, err := c.oauth.AccessToken(ctx, acc)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+"/billing?format=credits", nil)
	if err != nil {
		return nil, err
	}
	req.Header = c.cliHeaders(acc, token)
	// Billing uses the non-streaming CLI metadata names. Sending both aliases
	// is harmless, while x-userid is required by the official endpoint.
	if userID := strings.TrimSpace(acc.UserID); userID != "" {
		req.Header.Set("x-userid", userID)
	}
	resp, err := c.doCLIRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := decodeHTTPResponseBody(resp); err != nil {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("decode grok cli billing response: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, cliOAuthMaxBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, newCLIUpstreamError(resp.StatusCode, resp.Header, body, "")
	}
	var payload struct {
		SubscriptionTier string `json:"subscriptionTier"`
		Config           struct {
			CreditUsagePercent *float64 `json:"creditUsagePercent"`
			SubscriptionTier   string   `json:"subscriptionTier"`
			CurrentPeriod      struct {
				End string `json:"end"`
			} `json:"currentPeriod"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode grok cli billing response: %w", err)
	}
	info := &CLIBillingInfo{Subscription: strings.TrimSpace(firstNonEmpty(payload.SubscriptionTier, payload.Config.SubscriptionTier))}
	if payload.Config.CreditUsagePercent != nil {
		info.HasUsagePercent = true
		info.UsagePercent = *payload.Config.CreditUsagePercent
		if info.UsagePercent < 0 {
			info.UsagePercent = 0
		}
		if info.UsagePercent > 100 {
			info.UsagePercent = 100
		}
	}
	if rawEnd := strings.TrimSpace(payload.Config.CurrentPeriod.End); rawEnd != "" {
		if parsed, err := time.Parse(time.RFC3339, rawEnd); err == nil {
			info.PeriodEnd = parsed.UTC()
		}
	}
	// The billing response does not consistently include the user's paid plan.
	// The official CLI exposes it separately; it is useful account metadata but
	// never a substitute for a numeric quota.
	if tier, tierErr := c.fetchSubscriptionTier(ctx, acc, token); tierErr == nil && tier != "" {
		info.Subscription = tier
	}
	if !info.HasUsagePercent && info.PeriodEnd.IsZero() {
		return nil, fmt.Errorf("grok cli billing response contains no weekly quota")
	}
	return info, nil
}

// ApplyCLIBillingInfo records a real weekly percentage window. UsageCurrent
// retains this project's established "remaining" convention for Grok.
func ApplyCLIBillingInfo(acc *store.Account, info *CLIBillingInfo) bool {
	if acc == nil || info == nil {
		return false
	}
	changed := false
	// The plan comes from the official CLI identity endpoint. It is metadata,
	// not an inferred allowance: some paid accounts intentionally do not expose
	// a numeric Build-credit window.
	subscription := strings.TrimSpace(info.Subscription)
	if subscription == "" {
		subscription = "unknown"
	}
	if acc.Subscription != subscription {
		acc.Subscription = subscription
		changed = true
	}
	if info.HasUsagePercent {
		remaining := 100 - info.UsagePercent
		if acc.UsageLimit != 100 {
			acc.UsageLimit = 100
			changed = true
		}
		if acc.UsageCurrent != remaining {
			acc.UsageCurrent = remaining
			changed = true
		}
	}
	if !info.PeriodEnd.IsZero() && !acc.QuotaResetAt.Equal(info.PeriodEnd) {
		acc.QuotaResetAt = info.PeriodEnd
		changed = true
	}
	return changed
}

func (c *CLIClient) fetchSubscriptionTier(ctx context.Context, acc *store.Account, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+"/user?include=subscription", nil)
	if err != nil {
		return "", err
	}
	req.Header = c.cliHeaders(acc, token)
	if userID := strings.TrimSpace(acc.UserID); userID != "" {
		req.Header.Set("x-userid", userID)
	}
	resp, err := c.doCLIRequest(ctx, req)
	if err != nil {
		return "", err
	}
	if err := decodeHTTPResponseBody(resp); err != nil {
		_ = resp.Body.Close()
		return "", fmt.Errorf("decode grok cli subscription response: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, cliOAuthMaxBodyBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", newCLIUpstreamError(resp.StatusCode, resp.Header, body, "")
	}
	var payload struct {
		SubscriptionTier string `json:"subscriptionTier"`
		User             struct {
			SubscriptionTier string `json:"subscriptionTier"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode grok cli subscription response: %w", err)
	}
	return firstNonEmpty(payload.SubscriptionTier, payload.User.SubscriptionTier), nil
}
