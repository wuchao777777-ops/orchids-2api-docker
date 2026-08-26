package grok

import (
	"bufio"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/middleware"
	"orchids-api/internal/store"
)

const defaultStoredResponseTTL = 30 * 24 * time.Hour
const maxStoredResponseIDCaptureBytes = 4 << 20

func (h *Handler) handleNativeCLIResponsesAt(w http.ResponseWriter, r *http.Request, modelID string, spec ModelSpec, payload map[string]interface{}, upstreamPath string, saveOwnership bool) {
	if err := h.ensureModelEnabled(r.Context(), modelID); err != nil {
		writeResponsesAPIError(w, http.StatusBadRequest, "invalid_request_error", modelValidationMessage(modelID, err))
		return
	}
	if h == nil || h.cliClient == nil {
		writeResponsesAPIError(w, http.StatusServiceUnavailable, "service_unavailable", "grok cli client not configured")
		return
	}

	ownerHash := middleware.APIKeyFingerprint(r.Context())
	previousID := strings.TrimSpace(parseLooseStringAny(payload["previous_response_id"]))
	var (
		sess   *chatAccountSession
		pinned bool
		err    error
	)
	if previousID != "" && ownerHash != "" {
		ownership, lookupErr := h.getStoredResponse(r, previousID, ownerHash)
		if lookupErr != nil {
			writeStoredResponseLookupError(w, lookupErr, "previous response not found")
			return
		}
		if ownership.Provider != ProviderBuild {
			writeResponsesAPIError(w, http.StatusBadRequest, "invalid_request_error", "previous response provider is incompatible")
			return
		}
		sess, err = h.openCLIAccountSessionByID(r.Context(), ownership.AccountID, spec.UpstreamModel)
		pinned = true
	} else {
		sess, err = h.openCLIAccountSession(r.Context(), nil, spec.UpstreamModel)
	}
	if err != nil {
		writeResponsesAPIError(w, http.StatusServiceUnavailable, "response_account_unavailable", err.Error())
		return
	}
	defer sess.Close()

	payload["model"] = spec.UpstreamModel
	var resp *http.Response
	if pinned {
		resp, err = h.cliClient.doResponsesAt(r.Context(), sess.acc, upstreamPath, payload)
	} else {
		resp, err = h.doCLIWithAutoSwitchAt(r.Context(), sess, payload, spec.UpstreamModel, upstreamPath)
	}
	if err != nil {
		if markAllGrokAccountStatuses(err) {
			h.markAccountStatus(r.Context(), sess.acc, err)
		}
		writeResponsesAPIError(w, upstreamHTTPResponseStatus(err), "upstream_error", err.Error())
		return
	}
	defer resp.Body.Close()
	h.syncGrokQuota(sess.acc, resp.Header)
	copyNativeCLIResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	responseID := copyNativeCLIResponseAndCaptureID(w, resp.Body, resp.Header.Get("Content-Type"))

	if !saveOwnership || ownerHash == "" || responseID == "" || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}
	if err := h.saveStoredResponse(r, &store.StoredResponse{
		ResponseID: responseID,
		OwnerHash:  ownerHash,
		AccountID:  sess.acc.ID,
		Model:      spec.UpstreamModel,
		Provider:   ProviderBuild,
	}); err != nil {
		slog.Error("failed to save response ownership", "response_id", responseID, "account_id", sess.acc.ID, "error", err)
	}
}

// HandleResponsesCompact forwards the native Build Responses compaction API.
// Compaction is deliberately non-streaming and is not stored as a normal
// response resource.
func (h *Handler) HandleResponsesCompact(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var payload map[string]interface{}
	if !decodeJSONBody(w, r, &payload) || payload == nil {
		return
	}
	modelID := normalizeModelID(parseLooseStringAny(payload["model"]))
	if modelID == "" {
		writeResponsesAPIError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	if !requireAPIKeyModel(w, r, modelID) {
		return
	}
	spec, ok := ResolveModel(modelID)
	if !ok || !modelRoutedToCLI(spec, h.cfg) {
		writeResponsesAPIError(w, http.StatusBadRequest, "invalid_request_error", "responses compact requires a Grok Build model")
		return
	}
	payload["stream"] = false
	h.handleNativeCLIResponsesAt(w, r, modelID, spec, payload, "/responses/compact", false)
}

// HandleResponseResource retrieves or deletes a stored Build Responses
// resource through the exact account that created it.
func (h *Handler) HandleResponseResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "GET, DELETE")
		writeResponsesAPIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	responseID := responseIDFromResourcePath(r.URL.Path)
	if responseID == "" {
		writeResponsesAPIError(w, http.StatusBadRequest, "invalid_request_error", "response_id is required")
		return
	}
	ownerHash := middleware.APIKeyFingerprint(r.Context())
	if ownerHash == "" {
		ownerHash = "anonymous"
	}
	ownership, err := h.getStoredResponse(r, responseID, ownerHash)
	if err != nil {
		writeStoredResponseLookupError(w, err, "response not found")
		return
	}
	if ownership.Provider != ProviderBuild {
		writeResponsesAPIError(w, http.StatusNotFound, "response_not_found", "response not found")
		return
	}
	sess, err := h.openCLIAccountSessionByID(r.Context(), ownership.AccountID, ownership.Model)
	if err != nil {
		writeResponsesAPIError(w, http.StatusServiceUnavailable, "response_account_unavailable", err.Error())
		return
	}
	defer sess.Close()

	path := "/responses/" + url.PathEscape(responseID)
	resp, err := h.cliClient.doResponseResource(r.Context(), sess.acc, r.Method, path, r.URL.RawQuery)
	if err != nil {
		writeResponsesAPIError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	defer resp.Body.Close()
	copyNativeCLIResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	streamNativeCLIResponse(w, resp.Body)
	if (r.Method == http.MethodDelete && resp.StatusCode >= 200 && resp.StatusCode < 300) || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		_ = h.deleteStoredResponse(r, responseID, ownerHash)
	}
}

func (h *Handler) getStoredResponse(r *http.Request, responseID, ownerHash string) (*store.StoredResponse, error) {
	if h == nil || h.lb == nil || h.lb.Store == nil {
		return nil, errors.New("response store not configured")
	}
	return h.lb.Store.GetStoredResponse(r.Context(), responseID, ownerHash)
}

func (h *Handler) saveStoredResponse(r *http.Request, response *store.StoredResponse) error {
	if h == nil || h.lb == nil || h.lb.Store == nil {
		return errors.New("response store not configured")
	}
	ttl := defaultStoredResponseTTL
	if h.cfg != nil && h.cfg.ResponseStoreTTL > 0 {
		ttl = time.Duration(h.cfg.ResponseStoreTTL) * time.Hour
	}
	return h.lb.Store.SaveStoredResponse(r.Context(), response, ttl)
}

func (h *Handler) deleteStoredResponse(r *http.Request, responseID, ownerHash string) error {
	if h == nil || h.lb == nil || h.lb.Store == nil {
		return errors.New("response store not configured")
	}
	return h.lb.Store.DeleteStoredResponse(r.Context(), responseID, ownerHash)
}

func responseIDFromResourcePath(path string) string {
	marker := "/responses/"
	index := strings.LastIndex(path, marker)
	if index < 0 {
		return ""
	}
	value := strings.Trim(strings.TrimSpace(path[index+len(marker):]), "/")
	if value == "" || strings.Contains(value, "/") || strings.EqualFold(value, "compact") {
		return ""
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(decoded)
}

func copyNativeCLIResponseAndCaptureID(w http.ResponseWriter, body io.Reader, contentType string) string {
	if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		capture := newBoundedResponseCapture(maxStoredResponseIDCaptureBytes)
		if _, err := io.Copy(io.MultiWriter(w, capture), body); err != nil || capture.overflow {
			return ""
		}
		return responseIDFromJSON(capture.data)
	}

	reader := bufio.NewReaderSize(body, 64*1024)
	responseID := ""
	flusher, _ := w.(http.Flusher)
	line := newBoundedResponseCapture(maxStoredResponseIDCaptureBytes)
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			_, _ = w.Write(fragment)
			_, _ = line.Write(fragment)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if !line.overflow {
			if id := responseIDFromSSELine(line.data); id != "" {
				responseID = id
			}
		}
		line.Reset()
		if err != nil {
			break
		}
	}
	return responseID
}

type boundedResponseCapture struct {
	data     []byte
	limit    int
	overflow bool
}

func newBoundedResponseCapture(limit int) *boundedResponseCapture {
	return &boundedResponseCapture{limit: limit, data: make([]byte, 0, min(limit, 64*1024))}
}

func (c *boundedResponseCapture) Write(p []byte) (int, error) {
	if remaining := c.limit - len(c.data); remaining > 0 {
		if len(p) > remaining {
			c.data = append(c.data, p[:remaining]...)
			c.overflow = true
		} else {
			c.data = append(c.data, p...)
		}
	} else if len(p) > 0 {
		c.overflow = true
	}
	return len(p), nil
}

func (c *boundedResponseCapture) Reset() {
	c.data = c.data[:0]
	c.overflow = false
}

func responseIDFromSSELine(line []byte) string {
	trimmed := strings.TrimSpace(string(line))
	if !strings.HasPrefix(trimmed, "data:") {
		return ""
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "" || payload == "[DONE]" {
		return ""
	}
	return responseIDFromJSON([]byte(payload))
}

func responseIDFromJSON(raw []byte) string {
	var payload map[string]interface{}
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	if response, _ := payload["response"].(map[string]interface{}); response != nil {
		if id := strings.TrimSpace(parseLooseStringAny(response["id"])); id != "" {
			return id
		}
	}
	return strings.TrimSpace(parseLooseStringAny(payload["id"]))
}

func writeResponsesAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    "invalid_request_error",
			"code":    code,
		},
	})
}

func writeStoredResponseLookupError(w http.ResponseWriter, err error, notFoundMessage string) {
	if errors.Is(err, store.ErrNoRows) {
		writeResponsesAPIError(w, http.StatusNotFound, "response_not_found", notFoundMessage)
		return
	}
	writeResponsesAPIError(w, http.StatusServiceUnavailable, "response_store_unavailable", "response store unavailable")
}
