package util

import (
	"strings"

	"encoding/json"
)

// toolInputDepth bounds how many layers of argument wrapping are unwrapped. The
// upstream has been observed to double-encode arguments; three layers covers that
// with one spare without letting a hostile payload recurse indefinitely.
const toolInputDepth = 3

// NormalizeToolInput unwraps the OpenAI `{"arguments":"<json-string>"}` shape so
// downstream tool dispatch sees a plain JSON object.
//
// It lives here because both the Qoder and WorkBuddy stream readers need exactly
// this behaviour; the two channels each carried their own copy of the helpers
// below.
func NormalizeToolInput(raw string) string { return normalizeToolInputDepth(raw, toolInputDepth) }

// NormalizeToolInputDepth unwraps nested argument wrapping up to depth.
func normalizeToolInputDepth(input string, depth int) string {
	if depth <= 0 {
		return strings.TrimSpace(input)
	}
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || trimmed == "null" {
		return "{}"
	}
	var text string
	if json.Unmarshal([]byte(trimmed), &text) == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return "{}"
		}
		return normalizeToolInputDepth(text, depth-1)
	}
	if inner, ok := unwrapOpenAIArguments(trimmed); ok {
		return normalizeToolInputDepth(inner, depth-1)
	}
	return trimmed
}

// UnwrapOpenAIArguments reads the inner JSON text out of an OpenAI argument
// envelope. It reports false when the value is not that shape, or when the inner
// text is not valid JSON, so the caller can fall back to the original input.
func unwrapOpenAIArguments(input string) (string, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &obj); err != nil {
		return "", false
	}
	rawArgs, ok := obj["arguments"]
	if !ok {
		return "", false
	}
	var text string
	if err := json.Unmarshal(rawArgs, &text); err != nil {
		return "", false
	}
	text = strings.TrimSpace(text)
	if text == "" || text == "null" {
		return "{}", true
	}
	if !json.Valid([]byte(text)) {
		return "", false
	}
	return text, true
}

// NormalizeToolDefinitions renders the OpenAI `{"type":"function","function":{
// "name","description","parameters"}}` envelope from either that shape or the
// Anthropic `{"name", "input_schema", "description"}` shape. Anything that is
// not a JSON object is dropped, as is a declaration with no name.
//
// It lives here because the Qoder, WorkBuddy and Cline request builders all
// need the same envelope and each used to carry their own copy. It always
// returns a non-nil slice: callers differ only in what an empty result means,
// and each keeps that decision to itself.
func NormalizeToolDefinitions(tools []interface{}) []interface{} {
	out := make([]interface{}, 0, len(tools))
	for _, tool := range tools {
		raw, err := json.Marshal(tool)
		if err != nil {
			continue
		}
		var decoded map[string]interface{}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			continue
		}
		if fn, ok := decoded["function"].(map[string]interface{}); ok {
			if strings.TrimSpace(StringValue(fn["name"])) == "" {
				continue
			}
			decoded["type"] = "function"
			out = append(out, decoded)
			continue
		}
		name := strings.TrimSpace(StringValue(decoded["name"]))
		if name == "" {
			continue
		}
		parameters := decoded["input_schema"]
		if parameters == nil {
			parameters = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		out = append(out, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        name,
				"description": StringValue(decoded["description"]),
				"parameters":  parameters,
			},
		})
	}
	return out
}
