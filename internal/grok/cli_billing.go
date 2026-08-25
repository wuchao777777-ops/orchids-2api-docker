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
		Config struct {
			CreditUsagePercent *float64 `json:"creditUsagePercent"`
			CurrentPeriod      struct {
				End string `json:"end"`
			} `json:"currentPeriod"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode grok cli billing response: %w", err)
	}
	info := &CLIBillingInfo{}
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
	// Build OAuth describes the credential, not the subscription tier. xAI's
	// current JWT and Billing payload do not carry a trustworthy tier, so show
	// it as unknown instead of inventing a plan name.
	if acc.Subscription != "unknown" {
		acc.Subscription = "unknown"
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
