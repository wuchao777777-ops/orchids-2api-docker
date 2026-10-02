package grok

import (
	"fmt"
	"orchids-api/internal/testutil"
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
	testutil.NoError(t, normalizeBuildResponsesPayload(payload))
	tools := interfaceMaps(payload["tools"])
	testutil.Equal(t, len(tools), 1)
	testutil.Equal(t, tools[0]["name"], "repo__read")
	testutil.Equal(t, tools[0]["defer_loading"], nil)
	parameters := tools[0]["parameters"].(map[string]interface{})
	testutil.Equal(t, parameters["type"], "object")
	choice := payload["tool_choice"].(map[string]interface{})
	testutil.Equal(t, choice["name"], "repo__read")
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
	testutil.NoError(t, normalizeBuildResponsesPayload(payload))
	tools := interfaceMaps(payload["tools"])
	testutil.Equal(t, len(tools), 2)
	testutil.Equal(t, tools[0]["name"], "visible")
	testutil.Equal(t, tools[1]["name"], "tool_search")
	parallel, _ := payload["parallel_tool_calls"].(bool)
	testutil.Falsef(t, parallel, "parallel_tool_calls=%#v", payload["parallel_tool_calls"])
	warnings := takeBuildCompatibilityWarnings(payload)
	testutil.MustContainAll(t, warnings, "client_tool_search_emulated", "client_tool_search_forced_serial")
}

func TestBuildResponsesNormalizerWarnsAndRenamesCollisions(t *testing.T) {
	payload := map[string]interface{}{"tools": []interface{}{
		map[string]interface{}{"type": "function", "name": "a b", "parameters": map[string]interface{}{"type": "object"}},
		map[string]interface{}{"type": "function", "name": "a@b", "defer_loading": true, "parameters": map[string]interface{}{"type": []interface{}{"object", "null"}}},
	}}
	testutil.NoError(t, normalizeBuildResponsesPayload(payload))
	tools := interfaceMaps(payload["tools"])
	testutil.NotEqual(t, tools[0]["name"], tools[1]["name"])
	warnings := takeBuildCompatibilityWarnings(payload)
	for _, expected := range []string{"function_name_collision_renamed", "orphan_deferred_tool_loaded", "function_parameters_nullable_root_normalized"} {
		testutil.MustContain(t, warnings, expected)
	}
}

func TestBuildResponsesNormalizerPreservesNativeHistoryAndNormalizesExtensions(t *testing.T) {
	native := map[string]interface{}{"type": "shell_call", "call_id": "native", "action": map[string]interface{}{"type": "exec", "commands": []interface{}{"pwd"}}, "future": "keep"}
	payload := map[string]interface{}{"input": []interface{}{
		native,
		map[string]interface{}{"type": "agent_message", "author": "worker", "recipient": "manager", "content": "done", "encrypted_content": "must-not-leak"},
		map[string]interface{}{"type": "local_shell_call", "call_id": "call_1", "action": map[string]interface{}{"type": "exec", "command": []interface{}{"printf", "hello world"}}},
		map[string]interface{}{"type": "mcp_tool_call_output", "call_id": "mcp_1", "output": map[string]interface{}{"ok": true}, "secret": "drop"},
	}}
	testutil.NoError(t, normalizeBuildResponsesPayload(payload))
	items := payload["input"].([]interface{})
	first := items[0].(map[string]interface{})
	testutil.Equal(t, first["future"], "keep")
	testutil.Equal(t, first["type"], "shell_call")
	agent := items[1].(map[string]interface{})
	testutil.Falsef(t, agent["type"] != "message" || strings.Contains(agent["content"].([]interface{})[0].(map[string]interface{})["text"].(string), "must-not-leak"), "agent history=%#v", agent)
	shell := items[2].(map[string]interface{})
	testutil.Equal(t, shell["type"], "shell_call")
	testutil.Equal(t, shell["call_id"], "call_1")
	mcp := items[3].(map[string]interface{})
	testutil.Falsef(t, mcp["type"] != "message" || strings.Contains(fmt.Sprint(mcp), "secret"), "mcp history=%#v", mcp)
}

func TestBuildResponsesNormalizerOpaqueAgentMessageUsesBoundary(t *testing.T) {
	payload := map[string]interface{}{"input": []interface{}{map[string]interface{}{
		"type": "agent_message", "content": map[string]interface{}{"ciphertext": "opaque-secret"},
	}}}
	testutil.NoError(t, normalizeBuildResponsesPayload(payload))
	encoded := fmt.Sprint(payload["input"])
	testutil.Falsef(t, strings.Contains(encoded, "opaque-secret") || !strings.Contains(encoded, "not portable"), "boundary=%s", encoded)
}

func TestResponsesPayloadFromChatPreservesMultimodalAndNormalizesBuildState(t *testing.T) {
	req := &ChatCompletionsRequest{
		Model: "grok-4.5", PromptCacheKey: "session", SafetyIdentifier: "user-1",
		Messages: []ChatMessage{{Role: "user", Content: []interface{}{
			map[string]interface{}{"type": "text", "text": "inspect"},
			map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "data:image/png;base64,AA=="}},
		}}},
	}
	payload, err := (&Handler{}).responsesPayloadFromChat(ModelSpec{UpstreamModel: "grok-4.5"}, req, true)
	testutil.NoError(t, err)
	testutil.Equal(t, payload["prompt_cache_key"], "session")
	input := payload["input"].([]interface{})
	message := input[0].(map[string]interface{})
	parts := message["content"].([]interface{})
	testutil.Equal(t, len(parts), 2)
	testutil.Equal(t, parts[1].(map[string]interface{})["type"], "input_image")
	testutil.Equal(t, payload["safety_identifier"], "user-1")
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
	testutil.NoError(t, err)
	testutil.Equal(t, chat.Tools[0].Function["strict"], true)
	testutil.Equal(t, len(chat.ResponsesTools), 1)
	testutil.Equal(t, chat.ResponsesTools[0]["type"], "mcp")
	testutil.Equal(t, chat.ResponsesTools[0]["authorization"], "secret")
	testutil.Falsef(t, chat.ResponseText["format"] == nil || chat.SafetyIdentifier != "user-1", "text/safety=%#v %q", chat.ResponseText, chat.SafetyIdentifier)
}
