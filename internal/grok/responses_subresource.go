package grok

import (
	"net/http"
	"strings"

	"orchids-api/internal/responses"
)

// responsesInputItems normalizes a Responses `input` into the item array the
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
