package grok

import (
	"context"
	"fmt"
	"log/slog"
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

	resp, err := h.doCLIWithAutoSwitch(ctx, sess, payload)
	if err != nil {
		slog.Error("cli chat upstream failed", "url", h.cliBaseURL(), "status", parseUpstreamStatus(err), "error", err)
		if logger != nil {
			logger.LogUpstreamHTTPError(h.cliBaseURL(), parseUpstreamStatus(err), "", err)
		}
		if markAllGrokAccountStatuses(err) {
			h.markAccountStatus(ctx, sess.acc, err)
		}
		http.Error(w, err.Error(), upstreamHTTPResponseStatus(err))
		return
	}
	defer resp.Body.Close()
	if logger != nil {
		logger.LogUpstreamRequest(h.cliBaseURL()+"/responses", debugHeaderMap(h.cliHeaders(sess.acc, sess.token)), payload)
	}
	h.syncGrokQuota(sess.acc, resp.Header)
	if req.Stream {
		h.streamConsoleChat(w, req, resp.Body)
		return
	}
	h.collectConsoleChat(w, req, resp.Body)
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
func (h *Handler) doCLIWithAutoSwitch(ctx context.Context, sess *chatAccountSession, payload map[string]interface{}) (*http.Response, error) {
	if sess == nil || sess.acc == nil {
		return nil, fmt.Errorf("empty cli chat session")
	}
	if h == nil || h.cliClient == nil {
		return nil, fmt.Errorf("grok cli client not configured")
	}
	client := h.cliClient
	switchDeadline := time.Now().Add(10 * time.Second)
	if h.cfg != nil && h.cfg.AccountSwitchCount > 0 {
		switchDeadline = time.Now().Add(time.Duration(h.cfg.AccountSwitchCount) * time.Second)
	}
	const switchPace = 1500 * time.Millisecond

	used := make([]int64, 0)
	for {
		if sess.acc != nil && sess.acc.ID != 0 {
			used = append(used, sess.acc.ID)
		}
		resp, err := client.doResponses(ctx, sess.acc, payload)
		if err == nil {
			return resp, nil
		}
		if markAllGrokAccountStatuses(err) {
			h.markAccountStatus(ctx, sess.acc, err)
		}
		if !shouldSwitchGrokAccount(err) || time.Now().After(switchDeadline) {
			return nil, err
		}

		sess.Close()
		time.Sleep(switchPace)
		next, switchErr := h.openCLIAccountSession(ctx, used)
		if switchErr != nil {
			return nil, err
		}
		sess.acc = next.acc
		sess.token = next.token
		sess.poolCandidates = next.poolCandidates
		sess.release = next.release
	}
}

// openCLIAccountSession selects the next available Build CLI OAuth account.
func (h *Handler) openCLIAccountSession(ctx context.Context, excludeIDs []int64) (*chatAccountSession, error) {
	if h == nil || h.lb == nil {
		return nil, fmt.Errorf("load balancer not configured")
	}
	acc, err := h.lb.GetNextAccountExcludingByChannelWithTrackerFilter(ctx, excludeIDs, "grok", h.connTracker, func(acc *store.Account) bool {
		return acc != nil && strings.EqualFold(strings.TrimSpace(acc.CredentialType), "oauth")
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
