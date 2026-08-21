package grok

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/grok/egress"
	"orchids-api/internal/store"
	"orchids-api/internal/util"
)

// Build CLI (cli-chat-proxy.grok.com) upstream. It speaks the standard OpenAI
// Responses protocol authenticated with a Bearer OAuth access token, unlike the
// app-chat website protocol (SSO cookie) or console.x.ai (SSO + DPoP).

const (
	defaultCLIBaseURL    = "https://cli-chat-proxy.grok.com/v1"
	defaultCLITokenAuth  = "true"
	defaultCLIClientMode = "headless"
)

// CLIClient implements the Grok Build CLI upstream protocol.
type CLIClient struct {
	cfg        *config.Config
	httpClient *http.Client
	oauth      *CLIOAuth
	egress     *egress.Manager
}

// NewCLIClient builds a CLI upstream client from configuration.
func NewCLIClient(cfg *config.Config) *CLIClient {
	client := &CLIClient{cfg: cfg}
	// Shared browser client keeps the utls Chrome TLS fingerprint; the CLI
	// upstream tolerates browser-like TLS even though headers are CLI identity.
	client.httpClient = util.GetSharedBrowserHTTPClient("cli", 120*time.Second, nil)
	client.oauth = NewCLIOAuth(cfg, client.httpClient)
	client.egress = egress.NewManager(cfg)
	return client
}

// SetAccountStore wires a durable store so OAuth token refreshes performed on
// the request path are written back (including rotated refresh tokens).
func (c *CLIClient) SetAccountStore(s *store.Store) {
	if c == nil || c.oauth == nil {
		return
	}
	c.oauth.SetAccountStore(s)
}

// baseURL returns the CLI proxy base URL (defaults to the official gateway).
func (c *CLIClient) baseURL() string {
	if c != nil && c.cfg != nil {
		return c.cfg.GrokCLIBaseURLOrDefault()
	}
	return defaultCLIBaseURL
}

// OAuthAccessToken returns a valid access token for the account, refreshing it
// in memory when needed. Callers persist the mutated account fields.
func (c *CLIClient) OAuthAccessToken(ctx context.Context, acc *store.Account) (string, error) {
	if c == nil || c.oauth == nil {
		return "", fmt.Errorf("grok cli oauth not configured")
	}
	return c.oauth.AccessToken(ctx, acc)
}

// cliHeaders builds the Build CLI request headers for an OAuth account.
func (c *CLIClient) cliHeaders(acc *store.Account, accessToken string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+accessToken)
	h.Set("X-XAI-Token-Auth", defaultCLITokenAuth)
	h.Set("x-grok-client-version", c.clientVersion())
	h.Set("x-grok-client-identifier", c.clientIdentifier())
	h.Set("x-grok-client-mode", defaultCLIClientMode)
	h.Set("Accept", "application/json")
	h.Set("Accept-Encoding", "gzip")
	h.Set("User-Agent", c.userAgent())
	h.Set("x-xai-request-id", randomUUID())
	if acc != nil {
		if teamID := strings.TrimSpace(acc.TeamID); teamID != "" {
			h.Set("x-grok-team-id", teamID)
		}
		if userID := strings.TrimSpace(acc.UserID); userID != "" {
			h.Set("x-grok-user-id", userID)
		}
	}
	return h
}

func (c *CLIClient) userAgent() string {
	if c != nil && c.cfg != nil {
		return c.cfg.GrokCLIUserAgentOrDefault()
	}
	return "grok-cli/0.9.0"
}

func (c *CLIClient) clientVersion() string {
	if c != nil && c.cfg != nil {
		return c.cfg.GrokCLIClientVersionOrDefault()
	}
	return "0.9.0"
}

func (c *CLIClient) clientIdentifier() string {
	if c != nil && c.cfg != nil {
		return c.cfg.GrokCLIClientIdentifierOrDefault()
	}
	return "grok-cli"
}

// doResponses issues a standard Responses request to the CLI proxy. It ensures a
// valid access token (refreshing if needed) then POSTs the payload, returning
// the raw upstream response (SSE or JSON) for the caller to stream/collect. A
// confirmed Cloudflare challenge invalidates the egress clearance and retries at
// most once.
func (c *CLIClient) doResponses(ctx context.Context, acc *store.Account, payload map[string]interface{}) (*http.Response, error) {
	if acc == nil {
		return nil, fmt.Errorf("empty cli account")
	}
	challengeRetried := false
	for {
		resp, err := c.doResponsesOnce(ctx, acc, payload)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamBodyBytes+1))
		if len(raw) > maxUpstreamBodyBytes {
			raw = raw[:maxUpstreamBodyBytes]
		}
		headerCopy := resp.Header.Clone()
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			noteTeamRateLimit(resp.StatusCode, resp.Header, raw)
			recordCLIUpstreamStatus(resp.StatusCode)
			return nil, newCLIUpstreamError(resp.StatusCode, headerCopy, raw, "")
		}

		kind := ClassifyUpstreamResponse(resp.StatusCode, resp.Header, raw)
		if kind == UpstreamErrorCloudflareChallenge {
			recordUpstreamChallenge("cloudflare")
			if c.egress != nil && c.egress.Enabled() && !challengeRetried {
				challengeRetried = true
				c.egress.InvalidateAffinityClearance("cli", "cli-default")
				continue
			}
			if c.egress != nil && c.egress.Enabled() {
				c.egress.FeedbackAffinityOutcome("cli", "cli-default", egress.OutcomeChallenge)
			}
		} else if kind == UpstreamErrorDPoPChallenge {
			recordUpstreamChallenge("dpop")
		} else if kind == UpstreamErrorGenericForbidden {
			recordGenericForbidden()
		}

		recordCLIUpstreamStatus(resp.StatusCode)
		return nil, newCLIUpstreamError(resp.StatusCode, headerCopy, raw, "")
	}
}

// doResponsesOnce issues a single CLI Responses request (token, headers, egress
// lease, decompress) without retry logic.
func (c *CLIClient) doResponsesOnce(ctx context.Context, acc *store.Account, payload map[string]interface{}) (*http.Response, error) {
	if c == nil || c.oauth == nil {
		return nil, fmt.Errorf("grok cli client not configured")
	}
	token, err := c.oauth.AccessToken(ctx, acc)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("grok cli account access token is empty")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := c.baseURL() + "/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = c.cliHeaders(acc, token)

	resp, err := c.doCLIRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := decodeHTTPResponseBody(resp); err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("grok cli decode failed: %w", err)
	}
	return resp, nil
}

// VerifyAccount checks a Build CLI OAuth account by minting a token and probing
// the CLI proxy models endpoint. Returns an upstream status string ("401",
// "403", ...) alongside the error so callers can mark the account. A confirmed
// Cloudflare challenge invalidates egress clearance and retries once.
func (c *CLIClient) VerifyAccount(ctx context.Context, acc *store.Account) (string, error) {
	if c == nil || c.oauth == nil {
		return "", fmt.Errorf("grok cli client not configured")
	}
	if acc == nil {
		return "", fmt.Errorf("missing cli account")
	}
	challengeRetried := false
	for {
		token, err := c.oauth.AccessToken(ctx, acc)
		if err != nil {
			if oauthErr, ok := err.(*cliOAuthError); ok {
				return oauthErr.Status(), err
			}
			return "", err
		}
		endpoint := c.baseURL() + "/models"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return "", err
		}
		req.Header = c.cliHeaders(acc, token)

		resp, err := c.doCLIRequest(ctx, req)
		if err != nil {
			return "", err
		}
		if resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return "", nil
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamBodyBytes+1))
		if len(raw) > maxUpstreamBodyBytes {
			raw = raw[:maxUpstreamBodyBytes]
		}
		headerCopy := resp.Header.Clone()
		_ = resp.Body.Close()

		kind := ClassifyUpstreamResponse(resp.StatusCode, resp.Header, raw)
		if kind == UpstreamErrorCloudflareChallenge && c.egress != nil && c.egress.Enabled() && !challengeRetried {
			challengeRetried = true
			recordUpstreamChallenge("cloudflare")
			c.egress.InvalidateAffinityClearance("cli", "cli-default")
			continue
		}
		return classifyAccountStatusFromHTTP(resp.StatusCode, raw), newCLIUpstreamError(resp.StatusCode, headerCopy, raw, "")
	}
}

// doCLIRequest issues an HTTP request through the CLI client, routing via the
// egress lease when enabled (bound UA + clearance on a proxy-pool node). Egress
// is fail-closed: when enabled, an acquire failure is returned rather than
// silently falling back to the direct client. Node health is fed from the
// response, and the lease is released when the response body is closed.
func (c *CLIClient) doCLIRequest(ctx context.Context, req *http.Request) (*http.Response, error) {
	if c.egress == nil || !c.egress.Enabled() {
		return c.httpClient.Do(req)
	}
	lease, err := c.egress.Acquire(ctx, "cli", "cli-default")
	if err != nil {
		recordEgressAcquireError()
		return nil, fmt.Errorf("grok cli egress unavailable: %w", err)
	}
	if lease.UserAgent != "" {
		req.Header.Set("User-Agent", lease.UserAgent)
	}
	if lease.CFCookies != "" {
		mergeCFCookies(req.Header, lease.CFCookies)
	}
	resp, err := lease.Do(req)
	if err != nil {
		c.egress.FeedbackOutcome(lease.NodeID, egress.OutcomeTransportError)
		lease.Release()
		return nil, err
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		c.egress.FeedbackOutcome(lease.NodeID, egress.OutcomeSuccess)
	case resp.StatusCode == http.StatusTooManyRequests:
		c.egress.FeedbackOutcome(lease.NodeID, egress.OutcomeRateLimited)
	case resp.StatusCode >= 500:
		c.egress.FeedbackOutcome(lease.NodeID, egress.OutcomeServerError)
	default:
		c.egress.FeedbackOutcome(lease.NodeID, egress.OutcomeForbidden)
	}
	if resp != nil && resp.Body != nil {
		resp.Body = &leaseResponseBody{ReadCloser: resp.Body, release: lease.Release}
	} else {
		lease.Release()
	}
	return resp, nil
}

func classifyAccountStatusFromHTTP(status int, raw []byte) string {
	switch status {
	case http.StatusUnauthorized:
		return "401"
	case http.StatusForbidden:
		return "403"
	case http.StatusTooManyRequests:
		return "429"
	case http.StatusPaymentRequired:
		return "402"
	default:
		return ""
	}
}

// cliOAuthError is a lightweight wrapper so VerifyAccount can surface the
// upstream status of a failed refresh.
type cliOAuthError struct {
	status  int
	message string
}

func (e *cliOAuthError) Error() string { return e.message }
func (e *cliOAuthError) Status() string {
	if e == nil || e.status == 0 {
		return ""
	}
	return fmt.Sprintf("%d", e.status)
}
