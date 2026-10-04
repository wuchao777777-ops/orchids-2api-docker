package responses

import (
	"fmt"
	"strings"
)

// The response object's status takes
// precedence over the event label. A terminal label cannot finish queued work.
func TerminalFinish(kind string, response map[string]interface{}) (string, error) {
	if kind == "response.failed" || kind == "error" || response["error"] != nil {
		return "error", Failure(response)
	}
	status := ParseLooseStringAny(response["status"])
	if status == "" {
		status = "completed"
		if kind == "response.incomplete" {
			status = "incomplete"
		}
	}
	switch status {
	case "completed":
		return "stop", nil
	case "incomplete":
		return "length", nil
	case "failed", "cancelled":
		return "error", fmt.Errorf("upstream response %s: %w", status, Failure(response))
	case "queued", "in_progress":
		if kind == "" {
			return status, nil
		} // Valid asynchronous JSON response.
	}
	return "error", fmt.Errorf("upstream terminal response has invalid status %q", status)
}

func ExtractRefusal(response map[string]interface{}) string {
	var result strings.Builder
	for _, raw := range InterfaceSlice(response["output"]) {
		item, _ := raw.(map[string]interface{})
		if StreamString(item["type"]) != "message" {
			continue
		}
		for _, raw := range InterfaceSlice(item["content"]) {
			part, _ := raw.(map[string]interface{})
			if StreamString(part["type"]) == "refusal" {
				result.WriteString(StreamString(part["refusal"]))
			}
		}
	}
	return result.String()
}

func StatusFromFinish(finish string) (string, interface{}) {
	switch finish {
	case "length":
		return "incomplete", map[string]interface{}{"reason": "max_output_tokens"}
	case "content_filter":
		return "incomplete", map[string]interface{}{"reason": "content_filter"}
	default:
		return "completed", nil
	}
}

func Annotations(raw interface{}) []interface{} {
	out := []interface{}{}
	for _, raw := range InterfaceSlice(raw) {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if nested, ok := item["url_citation"].(map[string]interface{}); ok {
			item = CloneStringInterfaceMap(nested)
			item["type"] = "url_citation"
		}
		out = append(out, item)
	}
	return out
}
