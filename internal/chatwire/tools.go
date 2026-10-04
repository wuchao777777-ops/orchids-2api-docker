package chatwire

import (
	"fmt"
	"strings"
)

// NativeToolTypes are the hosted (server-side) tools an OpenAI-compatible
// client may declare in `tools`. They have no `function` object; they are
// forwarded to the upstream Responses plane as native tools.
var NativeToolTypes = map[string]string{
	"web_search":                    "web_search",
	"web_search_preview":            "web_search",
	"web_search_preview_2025_03_11": "web_search",
	"web_search_2025_08_26":         "web_search",
	"x_search":                      "x_search",
}

// ValidateToolDefinitions accepts the function and hosted tool shapes and
// rejects anything else, so a declaration the upstream cannot serve fails here
// rather than becoming a confusing upstream error.
func ValidateToolDefinitions(tools []ToolDef) error {
	seen := make(map[string]struct{}, len(tools))
	for i, tool := range tools {
		if normalized, native := NativeToolTypes[strings.ToLower(strings.TrimSpace(tool.Type))]; native {
			key := normalized
			if _, exists := seen[key]; exists {
				return fmt.Errorf("tools.%d.type duplicates %q", i, normalized)
			}
			seen[key] = struct{}{}
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(tool.Type), "function") {
			return fmt.Errorf("tools.%d.type must be function, web_search or x_search", i)
		}
		name := strings.TrimSpace(fmt.Sprint(tool.Function["name"]))
		if name == "" || name == "<nil>" {
			return fmt.Errorf("tools.%d.function.name is required", i)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("tools.%d.function.name duplicates %q", i, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}
