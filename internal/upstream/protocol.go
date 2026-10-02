package upstream

import (
	"fmt"
	"strings"
)

// ValidateProtocolControls rejects controls with no equivalent in the currently
// supported chat transports. Providers must call this before making a request.
func (r UpstreamRequest) ValidateProtocolControls(provider string) error {
	if len(r.ResponsesTools) > 0 {
		return fmt.Errorf("%s does not support Responses hosted tools", provider)
	}
	if len(r.Include) > 0 {
		return fmt.Errorf("%s does not support Responses include=%v", provider, r.Include)
	}
	for key := range r.ResponseText {
		if key != "format" {
			return fmt.Errorf("%s does not support text.%s", provider, key)
		}
	}
	format := r.ChatResponseFormat()
	if len(format) > 0 {
		switch strings.TrimSpace(fmt.Sprint(format["type"])) {
		case "text", "json_object":
		case "json_schema":
			schema, ok := format["json_schema"].(map[string]interface{})
			if !ok {
				return fmt.Errorf("response_format.json_schema must be an object")
			}
			if _, ok := schema["schema"].(map[string]interface{}); !ok {
				return fmt.Errorf("response_format.json_schema.schema must be an object")
			}
		default:
			return fmt.Errorf("unsupported response format type %q", format["type"])
		}
	}
	if provider == "qoder" && strings.TrimSpace(r.PromptCacheKey) != "" {
		return fmt.Errorf("qoder does not support prompt_cache_key")
	}
	return nil
}

// ChatResponseFormat lowers Responses text.format to the chat spelling without
// changing the client's schema or strictness. It also accepts chat-shaped input.
func (r UpstreamRequest) ChatResponseFormat() map[string]interface{} {
	f := r.ResponseFormat
	if v, ok := r.ResponseText["format"].(map[string]interface{}); ok {
		f = v
	}
	if len(f) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(f))
	for k, v := range f {
		out[k] = v
	}
	if f["type"] == "json_schema" && f["json_schema"] == nil {
		schema := make(map[string]interface{})
		for _, key := range []string{"name", "schema", "strict", "description"} {
			if v, ok := f[key]; ok {
				schema[key] = v
				delete(out, key)
			}
		}
		out["json_schema"] = schema
		if schema["name"] == nil {
			schema["name"] = "response"
		}
	}
	return out
}
