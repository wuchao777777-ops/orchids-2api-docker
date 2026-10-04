package grok

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"orchids-api/internal/util"
	"strings"

	"encoding/json"

	"orchids-api/internal/middleware"
	"orchids-api/internal/responses"
	"orchids-api/internal/store"
)

// bridgedResponseProvider labels records written by the chat bridge. Grok's
// resource handler serves any non-Build record straight from the shared store,
// so this label is what keeps a bridged response apart from a native one.
const bridgedResponseProvider = "chat-bridge"

// ResponsesStore and ResponsesBridgeOptions are aliases onto internal/responses:
// the stored-response contract is protocol-level, not provider-level.
type (
	ResponsesStore         = responses.Store
	ResponsesBridgeOptions = responses.BridgeOptions
)

// responsesChatPath maps a Responses endpoint onto the Chat Completions
// endpoint of the same channel prefix, so "/workbuddy/v1/responses" is served
// by "/workbuddy/v1/chat/completions" and the channel keeps deciding which
// upstream pool the request uses. The unified "/v1/responses" keeps its own
// path, which leaves channel selection to the model.
func responsesChatPath(path string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(path), "/")
	for _, suffix := range []string{"/responses/compact", "/responses"} {
		if strings.HasSuffix(trimmed, suffix) {
			return strings.TrimSuffix(trimmed, suffix) + "/chat/completions"
		}
	}
	return "/v1/chat/completions"
}

// ResponsesBridgeHandler serves the OpenAI Responses API on top of a channel
// that only implements Chat Completions.
//
// Codex defaults to the Responses wire API. Grok speaks it natively, but the
// WorkBuddy, Qoder and Cline only expose /v1/chat/completions, so
// without this bridge every request from Codex to those channels is a 404. The
// bridge reuses the channel's chat handler verbatim: account selection,
// retries, tool handling and streaming all stay where they already live.
func ResponsesBridgeHandler(chat http.HandlerFunc, opts ResponsesBridgeOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		body, err := readBoundedJSONBody(w, r)
		if err != nil {
			return
		}
		var req ResponsesCreateRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeGrokError(w, http.StatusBadRequest, "invalid json")
			return
		}
		req.Model = normalizeModelID(req.Model)
		r = r.WithContext(middleware.WithRequestModel(r.Context(), req.Model))
		if !requireAPIKeyModel(w, r, req.Model) {
			return
		}
		// This is an optional request for an output projection, not encrypted
		// input. Chat backends cannot supply portable encrypted reasoning.
		include := make([]string, 0, len(req.Include))
		for _, field := range req.Include {
			if field == "reasoning.encrypted_content" {
				w.Header().Set("X-Grok2API-Compatibility-Warnings", "chat bridge does not produce encrypted reasoning content")
				continue
			}
			include = append(include, field)
		}
		req.Include = include
		// The caller explicitly selected chat compatibility without hosted
		// search. Do not advertise a client function that Codex cannot execute.
		tools := make([]map[string]interface{}, 0, len(req.Tools))
		searchDisabled := false
		for _, tool := range req.Tools {
			kind, _ := tool["type"].(string)
			if kind == "web_search" || kind == "web_search_preview" || kind == "web_search_preview_2025_03_11" {
				searchDisabled = true
				continue
			}
			tools = append(tools, tool)
		}
		if searchDisabled {
			if choice, ok := req.ToolChoice.(string); ok && choice == "required" && len(tools) == 0 {
				writeResponsesAPIError(w, http.StatusBadRequest, "invalid_request_error", "web search is disabled for chat bridge; no executable required tool remains")
				return
			}
			if choice, ok := req.ToolChoice.(map[string]interface{}); ok {
				kind, _ := choice["type"].(string)
				if strings.HasPrefix(kind, "web_search") {
					writeResponsesAPIError(w, http.StatusBadRequest, "invalid_request_error", "web search is disabled for chat bridge; a forced search cannot be fulfilled")
					return
				}
			}
			w.Header().Add("X-Grok2API-Compatibility-Warnings", "web search disabled for chat bridge")
			req.Instructions += "\nHosted web search is unavailable for this request. Do not claim to search the web or invent search results."
		}
		req.Tools = tools
		// The bridge can always persist a response, streamed or not, because it
		// writes the terminal object the client saw rather than the raw stream.
		if err := validateResponsesCompatibilityFor(req, true); err != nil {
			writeGrokUpstreamError(w, err)
			return
		}
		if !expandBridgedPreviousResponse(w, r, &req, opts) {
			return
		}
		if err := expandBridgedCompaction(r, &req, opts); err != nil {
			writeBridgeCompactionError(w, err)
			return
		}
		// Grouped and emulated tool declarations (namespace, custom, apply_patch)
		// are flattened here instead of being rejected: a chat upstream only
		// understands flat function names, and rejecting them outright is what
		// made Codex unusable on every non-Grok channel. The rewrite runs after a
		// stored conversation was replayed so the calls it echoes back are renamed
		// too.
		aliases, err := normalizeBridgedTools(&req)
		if err != nil {
			writeResponsesAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		chatReq, err := chatRequestFromResponses(req)
		if err != nil {
			writeGrokUpstreamError(w, err)
			return
		}
		raw, err := json.Marshal(chatReq)
		if err != nil {
			writeGrokError(w, http.StatusInternalServerError, "failed to build chat request")
			return
		}

		subReq := r.Clone(context.WithValue(r.Context(), chatSourceOperationKey{}, "responses"))
		subReq.Method = http.MethodPost
		subReq.URL.Path = responsesChatPath(r.URL.Path)
		// Keep the inbound headers: the inner handler must observe the same
		// request identity (and must not lose the credential that authorized
		// this request when per-key auth is enabled).
		subReq.Header = r.Header.Clone()
		subReq.Header.Set("Content-Type", "application/json")
		subReq.Body = io.NopCloser(bytes.NewReader(raw))
		subReq.ContentLength = int64(len(raw))

		if chatReq.Stream {
			streamThroughChat(subReq, chat, func(status int, header http.Header, reader io.Reader) {
				if status < 200 || status >= 300 {
					for key, values := range header {
						w.Header()[key] = values
					}
					w.WriteHeader(status)
					_, _ = io.Copy(w, reader)
					return
				}
				writeResponsesStreamFromChatReaderRequest(w, req, reader, chatStreamOptions{
					ToolAliases: aliases,
					OnComplete:  bridgedResponseRecorder(r, req, opts),
				})
			})
			return
		}

		rec := newCaptureResponseWriter()
		chat(rec, subReq)
		if rec.code < 200 || rec.code >= 300 {
			copyCapturedResponse(w, rec)
			return
		}
		var chatBody map[string]interface{}
		if err := json.Unmarshal(rec.body.Bytes(), &chatBody); err != nil {
			writeGrokUpstreamError(w, err)
			return
		}
		response := responsesObjectFromChat(req.Model, chatBody)
		restoreBridgeToolIdentity(response, aliases)
		applyBridgedResponseExtras(response, req)
		// Ownership is recorded for any successful response: the caller's `store`
		// asks the upstream to retain, not this gateway.
		if err := saveBridgedResponse(r, req, response, opts); err != nil {
			writeGrokError(w, http.StatusServiceUnavailable, "failed to store response")
			return
		}
		util.WriteJSON(w, response)
	}
}

// ResponsesResourceHandler retrieves or deletes a stored response. Records are
// served from the bridge's store, which is the shared Redis store when one is
// configured and the in-process fallback otherwise. A miss answers with the
// Responses response_not_found envelope instead of Go's plain-text 404, so a
// client can tell "not stored here" from "no such route".
//
// The path is parsed before the method so the sibling endpoints below a response
// id (/cancel, /input_items) never look like a response id themselves.
func ResponsesResourceHandler(opts ResponsesBridgeOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if action := responsesSubResourceAction(r.URL.Path); action != "" {
			responsesSubResourceHandler(action, opts)(w, r)
			return
		}
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
		st := opts.StoreFor()
		owner := responsesOwnerHash(r.Context())
		record, err := st.GetStoredResponse(r.Context(), responseID, owner)
		if err != nil {
			writeStoredResponseLookupError(w, err, "response not found")
			return
		}
		if r.Method == http.MethodDelete {
			if err := st.DeleteStoredResponse(r.Context(), responseID, owner); err != nil {
				writeGrokError(w, http.StatusServiceUnavailable, "failed to delete response")
				return
			}
			util.WriteJSON(w, map[string]interface{}{"id": responseID, "object": "response.deleted", "deleted": true})
			return
		}
		if len(record.Body) == 0 {
			writeResponsesAPIError(w, http.StatusNotFound, "response_not_found", "response not found")
			return
		}
		contentType := strings.TrimSpace(record.ContentType)
		if contentType == "" {
			contentType = "application/json"
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(record.Body)
	}
}

// ResponsesChannelSubpath serves the Responses endpoints that hang off
// /responses for a chat-completions-only channel:
//
//   - POST /responses/            behaves like POST /responses (trailing slash)
//   - POST /responses/compact     creates a caller-owned summary reference for
//     subsequent Responses input; it does not create an ordinary response
//   - GET|DELETE /responses/{id}  served from the response store
//   - POST /responses/{id}/cancel and GET /responses/{id}/input_items
//     served from the response store (see responses_subresource.go)
func ResponsesChannelSubpath(chat http.HandlerFunc, opts ResponsesBridgeOptions) http.HandlerFunc {
	create := ResponsesBridgeHandler(chat, opts)
	compact := ResponsesBridgeCompactHandler(chat, opts)
	resource := ResponsesResourceHandler(opts)
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimRight(strings.TrimSpace(r.URL.Path), "/")
		if strings.HasSuffix(path, "/responses/compact") {
			compact(w, r)
			return
		}
		if strings.HasSuffix(path, "/responses") {
			create(w, r)
			return
		}
		resource(w, r)
	}
}

func applyBridgedResponseExtras(response map[string]interface{}, req ResponsesCreateRequest) {
	if response == nil {
		return
	}
	if len(req.Metadata) > 0 {
		response["metadata"] = req.Metadata
	}
	if strings.TrimSpace(req.Truncation) != "" {
		response["truncation"] = req.Truncation
	}
}

func saveBridgedResponse(r *http.Request, req ResponsesCreateRequest, response map[string]interface{}, opts ResponsesBridgeOptions) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return opts.StoreFor().SaveStoredResponse(r.Context(), &store.StoredResponse{
		ResponseID:  parseLooseStringAny(response["id"]),
		OwnerHash:   responsesOwnerHash(r.Context()),
		Model:       req.Model,
		Provider:    bridgedResponseProvider,
		ContentType: "application/json",
		Body:        encoded,
		InputItems:  responsesInputItemsJSON(req.Input),
	}, opts.TTLOrDefault())
}

// bridgedResponseRecorder persists the response the stream just finished with.
// A stream cannot report a storage failure to the client any more, so the
// failure is logged and the next turn sees response_not_found.
func bridgedResponseRecorder(r *http.Request, req ResponsesCreateRequest, opts ResponsesBridgeOptions) func(map[string]interface{}) {
	return func(response map[string]interface{}) {
		// Only a completed response is a resource a client can continue from; a
		// failed or partial one has no id worth owning.
		if !strings.EqualFold(parseLooseStringAny(response["status"]), "completed") {
			return
		}
		applyBridgedResponseExtras(response, req)
		if err := saveBridgedResponse(r, req, response, opts); err != nil {
			slog.Warn("Failed to store bridged response", "model", req.Model, "error", err)
		}
	}
}

// expandBridgedPreviousResponse prepends the stored conversation to the current
// input when the client continues a stored response. It writes the error
// response and returns false when the request cannot be continued.
func expandBridgedPreviousResponse(w http.ResponseWriter, r *http.Request, req *ResponsesCreateRequest, opts ResponsesBridgeOptions) bool {
	previousID := strings.TrimSpace(req.PreviousResponseID)
	if previousID == "" {
		return true
	}
	previous, err := opts.StoreFor().GetStoredResponse(r.Context(), previousID, responsesOwnerHash(r.Context()))
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			writeResponsesAPIError(w, http.StatusNotFound, "response_not_found", "previous response not found")
			return false
		}
		writeStoredResponseLookupError(w, err, "previous response not found")
		return false
	}
	expanded, err := expandStoredResponseInput(previous.Body, req.Input)
	if err != nil {
		writeResponsesAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return false
	}
	req.Input = expanded
	return true
}
