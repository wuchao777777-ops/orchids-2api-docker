package grok

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"orchids-api/internal/debug"
	"orchids-api/internal/store"
)

// serveCLIChat serves a chat completion through the Build CLI upstream
// (cli-chat-proxy.grok.com/v1/responses) using an OAuth account. It reuses the
// console Responses payload builder and the console stream/collect parsers,
// which already understand standard Responses SSE.

func (h *Handler) serveCLIChat(ctx context.Context, w http.ResponseWriter, req *ChatCompletionsRequest, spec ModelSpec, sess *chatAccountSession, logger *debug.Logger) {
	payload, err := h.consolePayload(spec, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Override the console endpoint model with the CLI model name.
	payload["model"] = spec.UpstreamModel
	resp, err := h.doCLIWithAutoSwitch(ctx, sess, payload, spec.UpstreamModel)
	h.finishUpstreamChat(ctx, w, req, sess, logger, "cli", h.cliBaseURL()+"/responses",
		func() http.Header { return h.cliHeaders(sess.acc, sess.token) }, payload, resp, err)
}

func (h *Handler) cliBaseURL() string {
	if h != nil && h.cfg != nil {
		return h.cfg.GrokCLIBaseURLOrDefault()
	}
	return defaultCLIBaseURL
}

func (h *Handler) cliHeaders(acc *store.Account, token string) http.Header {
	if h == nil || h.cliClient == nil {
		return nil
	}
	return h.cliClient.cliHeaders(acc, token)
}

// doCLIWithAutoSwitch issues a CLI request, switching to another OAuth account
// on transient failures (401 after refresh, 5xx) while treating team-level 429
// as shared (no switch).
func (h *Handler) doCLIWithAutoSwitch(ctx context.Context, sess *chatAccountSession, payload map[string]interface{}, modelID string) (*http.Response, error) {
	if sess == nil || sess.acc == nil {
		return nil, fmt.Errorf("empty cli chat session")
	}
	if h == nil || h.cliClient == nil {
		return nil, fmt.Errorf("grok cli client not configured")
	}
	client := h.cliClient
	return h.retryWithAccountSwitch(ctx, sess, 1500*time.Millisecond,
		func() (*http.Response, error) { return client.doResponses(ctx, sess.acc, payload) },
		func(used []int64) (*chatAccountSession, error) { return h.openCLIAccountSession(ctx, used, modelID) }, nil)
}

// openCLIAccountSession selects the next available Build CLI OAuth account.
func (h *Handler) openCLIAccountSession(ctx context.Context, excludeIDs []int64, modelID string) (*chatAccountSession, error) {
	if h == nil || h.lb == nil {
		return nil, fmt.Errorf("load balancer not configured")
	}
	acc, err := h.lb.GetNextAccountExcludingByChannelWithTrackerFilter(ctx, excludeIDs, "grok", h.connTracker, func(acc *store.Account) bool {
		return acc != nil && ProviderForAccount(acc) == ProviderBuild && AccountSupportsModel(acc, modelID)
	})
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(acc.OAuthAccessToken)
	if token == "" {
		token = strings.TrimSpace(acc.OAuthRefreshToken)
	}
	if token == "" {
		return nil, fmt.Errorf("grok cli account token is empty")
	}
	return &chatAccountSession{
		acc:            acc,
		token:          token,
		poolCandidates: nil,
		release:        h.trackAccount(acc),
	}, nil
}

// openConsoleAccountSession selects a Console SSO account. Console sessions
// cannot be substituted with Web SSO cookies even when both belong to the
// same person, because their endpoints and quotas are independent.
func (h *Handler) openConsoleAccountSession(ctx context.Context, excludeIDs []int64) (*chatAccountSession, error) {
	if h == nil || h.lb == nil {
		return nil, fmt.Errorf("load balancer not configured")
	}
	acc, err := h.lb.GetNextAccountExcludingByChannelWithTrackerFilter(ctx, excludeIDs, "grok", h.connTracker, isGrokConsoleAccount)
	if err != nil {
		return nil, err
	}
	token := grokSSOTokenRaw(acc)
	if NormalizeSSOToken(token) == "" {
		return nil, fmt.Errorf("grok console account token is empty")
	}
	return &chatAccountSession{acc: acc, token: token, release: h.trackAccount(acc)}, nil
}
