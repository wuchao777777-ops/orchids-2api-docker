package grok

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"orchids-api/internal/audit"
	"orchids-api/internal/middleware"
	"orchids-api/internal/secureblob"
	"orchids-api/internal/util"
)

// SetCompactionCipher lets the deployment hand the handler the sealing key for
// gateway-owned compaction state. Without it the feature is off and compaction
// turns keep their previous behaviour (a plain upstream forward).
func (h *Handler) SetCompactionCipher(cipher *secureblob.Cipher) {
	if h == nil {
		return
	}
	codec := newGatewayCompactionCodec(cipher)
	h.compactionMu.Lock()
	h.compactionCode = codec
	h.compactionMu.Unlock()
}

func (h *Handler) compactionCodecSnapshot() *gatewayCompactionCodec {
	if h == nil {
		return nil
	}
	return util.ReadSnapshot(&h.compactionMu, &h.compactionCode)
}

// GatewayCompactionEnabled reports whether this deployment can own compaction
// state. Callers use it to route compaction turns through the gateway.
func (h *Handler) GatewayCompactionEnabled() bool { return h.compactionCodecSnapshot().available() }

// handleGatewayCompaction answers a compaction turn itself: it runs the canonical
// summary request upstream, seals the cleaned summary into a gateway blob, and
// returns a synthetic compaction response.
//
// The summary turn is retried on another account for transient failures only. A
// degenerate or malformed summary and any 4xx describe the request, so repeating
// them would burn another full summary generation for the same answer.
func (h *Handler) handleGatewayCompaction(w http.ResponseWriter, r *http.Request, modelID string, spec ModelSpec, payload map[string]interface{}, streaming bool) {
	codec := h.compactionCodecSnapshot()
	if !codec.available() {
		writeResponsesAPIError(w, http.StatusServiceUnavailable, "service_unavailable", "gateway compaction is not configured")
		return
	}
	if h == nil || h.buildClient() == nil {
		writeResponsesAPIError(w, http.StatusServiceUnavailable, "service_unavailable", "grok cli client not configured")
		return
	}
	sessionKey := sessionFromContext(r.Context()).Key
	sample := prepareGatewayCompactionSample(payload)
	sample["model"] = spec.UpstreamModel

	// lastErr is only used to decide whether another attempt is worthwhile; the
	// client never sees upstream prose from this path.
	var lastErr error
	for attempt := 1; attempt <= gatewayCompactionMaxAttempts; attempt++ {
		sess, err := h.openCLIAccountSession(r.Context(), nil, spec.UpstreamModel)
		if err != nil {
			// The pool's note names why it is empty, and this path used to put it in
			// the body: classify it the way every other entrance does, and let the
			// note stay in the log.
			writeGrokAccountUnavailable(w, err, "response_account_unavailable", grokResponseAccountUnavailableMessage)
			return
		}
		accountID := sess.acc.ID
		resp, callErr := h.doCLIWithAutoSwitchAt(r.Context(), sess, sample, spec.UpstreamModel, "/responses")
		if callErr != nil {
			sess.Close()
			lastErr = callErr
			if attempt < gatewayCompactionMaxAttempts && waitGatewayCompactionRetry(r.Context(), gatewayCompactionRetryPause) {
				continue
			}
			// The comment above says the client never sees upstream prose from this
			// path; that is true once the failure goes through the shared sanitizer
			// (err.Error() used to leak it here).
			slog.Warn("Reporting an upstream failure to the client", "error", callErr,
				"status", upstreamHTTPResponseStatus(callErr))
			writeResponsesAPIError(w, upstreamHTTPResponseStatus(callErr), "upstream_error", grokUpstreamFailureMessage(callErr))
			return
		}
		h.syncGrokQuota(sess.acc, resp.Header)
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxNativeResponsesBytes+1))
		status := resp.StatusCode
		_ = resp.Body.Close()
		sess.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < gatewayCompactionMaxAttempts && waitGatewayCompactionRetry(r.Context(), gatewayCompactionRetryPause) {
				continue
			}
			writeResponsesAPIError(w, http.StatusBadGateway, "upstream_error", "upstream compaction response could not be read")
			return
		}
		if status < 200 || status >= 300 {
			lastErr = fmt.Errorf("upstream status=%d", status)
			if attempt < gatewayCompactionMaxAttempts && compactionHTTPErrorIsTransient(status, string(data)) &&
				waitGatewayCompactionRetry(r.Context(), gatewayCompactionRetryPause) {
				continue
			}
			writeResponsesAPIError(w, upstreamHTTPResponseStatus(lastErr), "upstream_error", "upstream compaction request failed")
			return
		}
		if len(data) > maxNativeResponsesBytes {
			writeResponsesAPIError(w, http.StatusBadGateway, "compaction_failed", "upstream compaction response was too large")
			return
		}
		parsed, parseErr := parseGatewayCompactionStream(data)
		if parseErr == nil && isDegenerateGatewayCompactionSummary(parsed.summary) {
			parseErr = errGatewayCompactionDegenerate
		}
		if parseErr != nil {
			lastErr = parseErr
			if gatewayCompactionErrorIsTransient(parseErr) && attempt < gatewayCompactionMaxAttempts &&
				waitGatewayCompactionRetry(r.Context(), gatewayCompactionRetryPause) {
				continue
			}
			writeResponsesAPIError(w, http.StatusBadGateway, "compaction_failed", "Grok Build compaction failed")
			return
		}
		blob, encodeErr := codec.encode(sessionKey, gatewayCompactionContinuation(parsed.summary))
		if encodeErr != nil {
			writeResponsesAPIError(w, http.StatusBadGateway, "compaction_failed", "gateway compaction state could not be encoded")
			return
		}
		result := buildGatewayCompactionResponse(parsed.response, blob, modelID)
		h.auditGatewayCompaction(r.Context(), accountID, modelID, result)
		if !streaming {
			util.WriteJSONStatus(w, http.StatusOK, result)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		if err := writeGatewayCompactionStream(w, result); err != nil {
			slog.Debug("gateway compaction stream write failed", "error", err)
		}
		return
	}
	slog.Debug("gateway compaction exhausted its attempts", "model", modelID, "error", lastErr)
	writeResponsesAPIError(w, http.StatusBadGateway, "compaction_failed", "Grok Build compaction failed")
}

// auditGatewayCompaction records the gateway-owned compaction turn. Token counts
// come from the summary turn the gateway itself ran, so the source is upstream.
func (h *Handler) auditGatewayCompaction(ctx context.Context, accountID int64, modelID string, result map[string]interface{}) {
	if h == nil || h.auditLogger == nil {
		return
	}
	usage, _ := result["usage"].(map[string]interface{})
	h.auditLogger.Log(ctx, audit.Event{
		Kind: audit.KindRequest, RequestID: middleware.GetRequestID(ctx), Action: "grok_compaction",
		APIKeyID: middleware.APIKeyID(ctx), AccountID: accountID, Model: modelID,
		Channel: "grok", Provider: ProviderBuild, Status: "success",
		InputTokens:  int(nonNegativeJSONInteger(usage["input_tokens"])),
		OutputTokens: int(nonNegativeJSONInteger(usage["output_tokens"])),
		TotalTokens:  int(nonNegativeJSONInteger(usage["total_tokens"])),
		UsageSource:  audit.UsageSourceUpstream,
	})
}

// waitGatewayCompactionRetry pauses between summary attempts, giving up as soon
// as the caller goes away.
func waitGatewayCompactionRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// compactionErrorParam exposes the input index an expansion failure points at.
func compactionErrorParam(err error) string {
	var blobErr *compactionBlobError
	if errors.As(err, &blobErr) {
		return blobErr.Param()
	}
	return "input"
}

// responsesPayloadStreaming reports whether the caller asked for SSE. The parsed
// request is authoritative when the raw payload omitted the field.
func responsesPayloadStreaming(payload map[string]interface{}, fallback bool) bool {
	if value, ok := payload["stream"].(bool); ok {
		return value
	}
	return fallback
}
