package grok

import (
	"fmt"
	"strings"
)

func validateBridgeTools(tools []map[string]interface{}) error {
	for index, tool := range tools {
		kind := strings.ToLower(parseLooseStringAny(tool["type"]))
		if kind != "function" {
			return fmt.Errorf("tools[%d]: tool type %q requires a native Responses provider", index, kind)
		}
		name := parseLooseStringAny(tool["name"])
		if function, ok := tool["function"].(map[string]interface{}); ok {
			name = parseLooseStringAny(function["name"])
		}
		if name == "" {
			return fmt.Errorf("tools[%d].name is required", index)
		}
	}
	return nil
}
