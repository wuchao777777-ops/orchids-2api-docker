package errors

import (
	"net/http"
	"strings"
)

// PublicMessage converts an internal/upstream error into a stable, actionable
// message without exposing response bodies, credentials, cookies or provider
// implementation details. The request ID is returned separately by middleware.
func PublicMessage(errText string) string {
	return messageForCategory(ClassifyUpstreamError(errText).Category)
}

// StatusForCategory maps an error category to the HTTP status a client should
// see. It sits beside messageForCategory so the status and the text a client
// receives cannot disagree about what went wrong.
//
// The statuses follow the convention an OpenAI-compatible client already acts on:
// 429 is where it looks for a retryable capacity problem (and for a quota, where
// it stops), 401 for a credential it must replace, 400 for a request the upstream
// rejected on its merits, and 5xx for the gateway's or the upstream's own fault.
func StatusForCategory(category string) int {
	switch category {
	case "quota_exhausted", "rate_limit", "upstream_queue":
		// upstream_queue is a throttle the caller can wait out: the upstream put
		// the request in a queue and named a window (retryAfterSeconds). It is
		// answered as 429 + Retry-After so an OpenAI-compatible client backs off
		// and comes back, instead of reading 503 as a broken gateway.
		return http.StatusTooManyRequests
	case "upstream_unavailable":
		// The upstream's own service is down for this model and offered no
		// window. It is not the caller's rate limit and not this gateway's fault,
		// so it is answered as a temporarily unavailable upstream rather than as
		// a 429 that tells the caller to stop sending requests.
		return http.StatusServiceUnavailable
	case "auth", "auth_blocked":
		return http.StatusUnauthorized
	case "client":
		return http.StatusBadRequest
	case "model_unavailable":
		return http.StatusNotFound
	case "configuration":
		return http.StatusServiceUnavailable
	case "timeout":
		return http.StatusGatewayTimeout
	case "network", "server", "protocol", "local_overload":
		return http.StatusBadGateway
	default:
		return http.StatusBadGateway
	}
}

// messageForCategory is the single source of truth for the client-visible text
// of each category. An empty category is the caller that had no error text to
// classify, and keeps its shorter wording.
func messageForCategory(category string) string {
	switch category {
	case "configuration":
		return "This provider is not configured correctly. Contact the gateway administrator."
	case "auth":
		return "The upstream account session has expired. Re-authenticate the account and retry."
	case "auth_blocked":
		return "The upstream account is not allowed to use this feature. Check its plan and permissions."
	case "quota_exhausted":
		return "The available upstream accounts have exhausted their quota. Retry after the quota resets or add capacity."
	case "rate_limit":
		return "The available upstream accounts are rate-limited. Retry after the cooldown."
	case "upstream_queue":
		// This category exists so the caller is told what actually happened. It
		// replaces a 503 "the upstream service is temporarily unavailable" that
		// was published for a gate the same upstream served fine seconds later.
		return "The upstream is holding this model's requests in a queue and has not admitted this one yet. Retry after the indicated delay."
	case "upstream_unavailable":
		return "The upstream service for this model is temporarily unavailable. Retry after the indicated delay."
	case "model_unavailable":
		return "The requested model is unavailable for the selected upstream accounts."
	case "client":
		return "The upstream rejected the request parameters or model. Check the request and model selection."
	case "timeout":
		return "The upstream stream timed out while waiting for generated output."
	case "network":
		return "The gateway lost its connection to the upstream service. Retry later."
	case "server":
		return "The upstream service is temporarily unavailable. Retry later."
	case "protocol":
		return "The upstream returned an unsupported stream format. Use the request ID to inspect diagnostics."
	case "local_overload":
		return "The gateway is temporarily overloaded. Retry later."
	case "canceled":
		return "The request was canceled."
	default:
		if strings.TrimSpace(category) == "" {
			return "The upstream request failed."
		}
		return "The upstream request failed. Use the request ID to inspect diagnostics."
	}
}
