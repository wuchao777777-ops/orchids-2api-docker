package grok

import (
	"strings"
	"testing"
)

func TestBuildResponsesNormalizerFlattensNamespaceAndNullableRoot(t *testing.T) {
	payload := map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{
				"type": "namespace", "name": "repo", "tools": []interface{}{
					map[string]interface{}{"type": "function", "name": "read", "defer_loading": true, "parameters": map[string]interface{}{"type": []interface{}{"object", "null"}}},
				},
			},
			map[string]interface{}{"type": "tool_search", "execution": "server"},
		},
		"tool_choice": map[string]interface{}{"type": "function", "name": "read", "namespace": "repo"},
	}
	if err := normalizeBuildResponsesPayload(payload); err != nil {
		t.Fatal(err)
	}
	tools := interfaceMaps(payload["tools"])
	if len(tools) != 1 || tools[0]["name"] != "repo__read" || tools[0]["defer_loading"] != nil {
		t.Fatalf("tools=%#v", tools)
	}
	parameters := tools[0]["parameters"].(map[string]interface{})
	if parameters["type"] != "object" {
		t.Fatalf("parameters=%#v", parameters)
	}
	choice := payload["tool_choice"].(map[string]interface{})
	if choice["name"] != "repo__read" {
		t.Fatalf("tool_choice=%#v", choice)
	}
}

func TestBuildResponsesNormalizerEmulatesClientToolSearch(t *testing.T) {
	payload := map[string]interface{}{
		"parallel_tool_calls": true,
		"tools": []interface{}{
			map[string]interface{}{"type": "function", "name": "deferred", "defer_loading": true, "parameters": map[string]interface{}{"type": "object"}},
			map[string]interface{}{"type": "function", "name": "visible", "parameters": map[string]interface{}{"type": "object"}},
			map[string]interface{}{"type": "tool_search", "execution": "client"},
		},
	}
	if err := normalizeBuildResponsesPayload(payload); err != nil {
		t.Fatal(err)
	}
	tools := interfaceMaps(payload["tools"])
	if len(tools) != 2 || tools[0]["name"] != "visible" || tools[1]["name"] != "tool_search" {
		t.Fatalf("tools=%#v", tools)
	}
	if parallel, _ := payload["parallel_tool_calls"].(bool); parallel {
		t.Fatalf("parallel_tool_calls=%#v", payload["parallel_tool_calls"])
	}
	warnings := takeBuildCompatibilityWarnings(payload)
	if !strings.Contains(warnings, "client_tool_search_emulated") || !strings.Contains(warnings, "client_tool_search_forced_serial") {
		t.Fatalf("warnings=%q", warnings)
	}
}

func TestBuildResponsesNormalizerWarnsAndRenamesCollisions(t *testing.T) {
	payload := map[string]interface{}{"tools": []interface{}{
		map[string]interface{}{"type": "function", "name": "a b", "parameters": map[string]interface{}{"type": "object"}},
		map[string]interface{}{"type": "function", "name": "a@b", "defer_loading": true, "parameters": map[string]interface{}{"type": []interface{}{"object", "null"}}},
	}}
	if err := normalizeBuildResponsesPayload(payload); err != nil {
		t.Fatal(err)
	}
	tools := interfaceMaps(payload["tools"])
	if tools[0]["name"] == tools[1]["name"] {
		t.Fatalf("collision not renamed: %#v", tools)
	}
	warnings := takeBuildCompatibilityWarnings(payload)
	for _, expected := range []string{"function_name_collision_renamed", "orphan_deferred_tool_loaded", "function_parameters_nullable_root_normalized"} {
		if !strings.Contains(warnings, expected) {
			t.Fatalf("warnings=%q missing %q", warnings, expected)
		}
	}
}

func TestResponsesPayloadFromChatPreservesMultimodalAndNormalizesConsoleState(t *testing.T) {
	req := &ChatCompletionsRequest{
		Model: "console/grok-4.5", PromptCacheKey: "session", SafetyIdentifier: "user-1",
		Messages: []ChatMessage{{Role: "user", Content: []interface{}{
			map[string]interface{}{"type": "text", "text": "inspect"},
			map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "data:image/png;base64,AA=="}},
		}}},
	}
	payload, err := (&Handler{}).responsesPayloadFromChat(ModelSpec{ConsoleModel: "grok-4.5"}, req, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["prompt_cache_key"]; exists || payload["store"] != false {
		t.Fatalf("stateful fields leaked: %#v", payload)
	}
	input := payload["input"].([]interface{})
	message := input[0].(map[string]interface{})
	parts := message["content"].([]interface{})
	if len(parts) != 2 || parts[1].(map[string]interface{})["type"] != "input_image" {
		t.Fatalf("input=%#v", input)
	}
	if payload["safety_identifier"] != "user-1" {
		t.Fatalf("safety_identifier=%#v", payload["safety_identifier"])
	}
}

func TestAnthropicRequestNormalizesMCPStrictAndOutputFormat(t *testing.T) {
	strict := true
	req := anthropicMessagesRequest{
		Model: "grok-4.6", MaxTokens: 1024, Messages: []anthropicMessage{{Role: "user", Content: "hello"}},
		Tools:        []anthropicTool{{Name: "read", InputSchema: map[string]interface{}{"type": "object"}, Strict: &strict}},
		MCPServers:   []map[string]interface{}{{"name": "docs", "url": "https://example.test/mcp", "authorization_token": "secret"}},
		OutputConfig: map[string]interface{}{"format": map[string]interface{}{"type": "json_schema", "schema": map[string]interface{}{"type": "object"}}},
		Metadata:     map[string]interface{}{"user_id": "user-1"},
	}
	chat, err := anthropicRequestToChat(req)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Tools[0].Function["strict"] != true || len(chat.ResponsesTools) != 1 {
		t.Fatalf("tools=%#v native=%#v", chat.Tools, chat.ResponsesTools)
	}
	if chat.ResponsesTools[0]["type"] != "mcp" || chat.ResponsesTools[0]["authorization"] != "secret" {
		t.Fatalf("mcp=%#v", chat.ResponsesTools[0])
	}
	if chat.ResponseText["format"] == nil || chat.SafetyIdentifier != "user-1" {
		t.Fatalf("text/safety=%#v %q", chat.ResponseText, chat.SafetyIdentifier)
	}
}
