package grok

import (
	"net/http"
	"strings"
	"time"

	"encoding/json"

	"orchids-api/internal/responses"
	"orchids-api/internal/store"
	"orchids-api/internal/util"
)

// writeCancelledRecord flips a stored response to `cancelled` and echoes the
// response object, which is what the Responses SDK expects from cancel.
//
// Every response the gateway stores is written once it is terminal, so a cancel
// that arrives after the fact changes nothing and is answered with the record as
// it stands. That is what makes the endpoint idempotent instead of an error: a
// client that cancels defensively must not be told its response does not exist.
func writeCancelledRecord(w http.ResponseWriter, r *http.Request, st ResponsesStore, record *store.StoredResponse, fallbackTTL time.Duration) {
	var response map[string]interface{}
	if err := json.Unmarshal(record.Body, &response); err != nil || response == nil {
		writeResponsesAPIError(w, http.StatusBadGateway, "invalid_stored_response", "stored response is not a JSON object")
		return
	}
	if !strings.EqualFold(interfaceString(response["status"]), "cancelled") {
		response["status"] = "cancelled"
		markOutputItemsCancelled(response)
		encoded, encodeErr := json.Marshal(response)
		if encodeErr != nil {
			writeResponsesAPIError(w, http.StatusInternalServerError, "server_error", "failed to update response")
			return
		}
		updated := *record
		updated.Body = encoded
		updated.ContentType = firstNonEmpty(strings.TrimSpace(record.ContentType), "application/json")
		ttl := time.Until(record.ExpiresAt)
		if ttl <= 0 {
			ttl = fallbackTTL
		}
		if err := st.SaveStoredResponse(r.Context(), &updated, ttl); err != nil {
			writeStoredResponseLookupError(w, err, "response not found")
			return
		}
	}
	util.WriteJSON(w, response)
}

// writeSyntheticCancelledResponse answers cancel for a record whose body lives
// upstream. The upstream resource is never mutated: the gateway did not create
// it, cannot read it, and a cancel it cannot verify is worse than an honest
// object carrying the identity it does know.
func writeSyntheticCancelledResponse(w http.ResponseWriter, record *store.StoredResponse) {
	response := map[string]interface{}{
		"id":     record.ResponseID,
		"object": "response",
		"status": "cancelled",
	}
	if model := strings.TrimSpace(record.Model); model != "" {
		response["model"] = model
	}
	if !record.CreatedAt.IsZero() {
		response["created_at"] = record.CreatedAt.Unix()
	}
	response["output"] = []interface{}{}
	util.WriteJSON(w, response)
}

// markOutputItemsCancelled keeps the item statuses consistent with the response
// status. An item that already finished keeps its own status, so the record
// still says what the upstream actually produced.
func markOutputItemsCancelled(response map[string]interface{}) {
	for _, raw := range interfaceSlice(response["output"]) {
		item, _ := raw.(map[string]interface{})
		if item == nil {
			continue
		}
		switch strings.ToLower(interfaceString(item["status"])) {
		case "", "in_progress", "queued":
			item["status"] = "cancelled"
		}
	}
}

// writeStoredInputItems serves GET /responses/{id}/input_items from what the
// gateway persisted when it wrote the response. A record written before the
// gateway persisted input items reports an empty list rather than failing: the
// response exists, and the caller can still read its output.
func writeStoredInputItems(w http.ResponseWriter, record *store.StoredResponse) {
	items := []interface{}{}
	if len(record.InputItems) > 0 {
		var decoded []interface{}
		if err := json.Unmarshal(record.InputItems, &decoded); err == nil {
			items = decoded
		}
	}
	payload := map[string]interface{}{
		"object":   "list",
		"data":     items,
		"has_more": false,
	}
	if len(items) > 0 {
		if first, ok := items[0].(map[string]interface{}); ok {
			payload["first_id"] = parseLooseStringAny(first["id"])
		}
		if last, ok := items[len(items)-1].(map[string]interface{}); ok {
			payload["last_id"] = parseLooseStringAny(last["id"])
		}
	}
	util.WriteJSON(w, payload)
}

// responsesInputItemsJSON normalizes a Responses `input` into the item array the
// resource endpoint reports.
//
// Every item carries an id and a terminal status because that is the shape the
// official clients round-trip: a caller that reads the list and sends it back as
// the next turn's `input` must not be rejected for fields the gateway elided.
// Items the client already labelled keep their own id, so a replay stays
// byte-stable.
func responsesInputItems(input interface{}) []interface{} {
	switch value := input.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return []interface{}{map[string]interface{}{
			"id":      "msg_" + randomHex(12),
			"type":    "message",
			"role":    "user",
			"status":  "completed",
			"content": []interface{}{map[string]interface{}{"type": "input_text", "text": value}},
		}}
	case []interface{}:
		out := make([]interface{}, 0, len(value))
		for _, raw := range value {
			item, _ := raw.(map[string]interface{})
			if item == nil {
				continue
			}
			copied := cloneStringInterfaceMap(item)
			if copied == nil {
				continue
			}
			if interfaceString(copied["id"]) == "" {
				copied["id"] = "item_" + randomHex(12)
			}
			if interfaceString(copied["status"]) == "" {
				copied["status"] = "completed"
			}
			out = append(out, copied)
		}
		return out
	default:
		return nil
	}
}

// writeResponsesMethodNotAllowed answers a wrong method with the Responses
// envelope and the Allow header, so a client that probes the endpoint learns the
// contract instead of receiving Go's plain-text 405.
func writeResponsesMethodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeResponsesAPIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
}

// The Responses sub-resource endpoints (/cancel, /input_items) are
// protocol-level: they answer the same way for every channel, so they now live
// in internal/responses. These aliases keep the names the grok resource handler
// and cmd/server/routes.go already use.
var (
	responsesSubResourceAction  = responses.SubResourceAction
	responsesSubResourceHandler = responses.SubResourceHandler
)

// Exported wrappers: cmd/server/routes.go registers these by name. They are
// protocol-level handlers that now live in internal/responses.
var (
	ResponsesCancelHandler     = responses.CancelHandler
	ResponsesInputItemsHandler = responses.InputItemsHandler
)

// ResponsesUnifiedResource hands a stored response to whichever plane owns it.
// isNativeProvider is injected because "build" is a Grok provider label, not a
// Responses protocol concept.
func ResponsesUnifiedResource(nativeBuild http.HandlerFunc, opts ResponsesBridgeOptions) http.HandlerFunc {
	return responses.UnifiedResource(nativeBuild, opts, func(provider string) bool {
		return strings.EqualFold(strings.TrimSpace(provider), ProviderBuild)
	})
}

// Test-facing aliases onto internal/responses.
var (
	parseResponsesResourcePath = responses.ParseResourcePath
	responsesActionCancel      = responses.ActionCancel
	responsesActionInputItems  = responses.ActionInputItems
)
