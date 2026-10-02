package grok

import (
	"io"
	"orchids-api/internal/testutil"
	"strings"
	"testing"
)

func TestBuildNamespaceAliasRestoredInJSONAndSSE(t *testing.T) {
	payload := map[string]interface{}{"tools": []interface{}{
		map[string]interface{}{"type": "namespace", "name": "crm", "tools": []interface{}{
			map[string]interface{}{"type": "function", "name": "lookup", "parameters": map[string]interface{}{"type": "object"}},
		}},
	}}
	aliases := collectBuildToolAliases(payload)
	testutil.Equal(t, aliases["crm__lookup"].Namespace, "crm")

	jsonSource := io.NopCloser(strings.NewReader(`{"output":[{"type":"function_call","name":"crm__lookup","arguments":"{}"}]}`))
	converted, err := io.ReadAll(rewriteBuildToolAliasResponse(jsonSource, "application/json", aliases))
	testutil.NoError(t, err)
	testutil.MustContainAll(t, string(converted), `"name":"lookup"`, `"namespace":"crm"`)

	sseSource := io.NopCloser(strings.NewReader("event: response.output_item.added\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"name\":\"crm__lookup\"}}\n\n"))
	converted, err = io.ReadAll(rewriteBuildToolAliasResponse(sseSource, "text/event-stream", aliases))
	testutil.NoError(t, err)
	testutil.MustContainAll(t, string(converted), `"namespace":"crm"`, `event: response.output_item.added`)
}

func TestAnthropicServerSearchHistoryUsesNativeResponsesItem(t *testing.T) {
	req := anthropicMessagesRequest{
		Model: "grok-4.5", MaxTokens: 64,
		Tools: []anthropicTool{{Type: "web_search_20250305", Name: "web_search"}},
		Messages: []anthropicMessage{{Role: "assistant", Content: []interface{}{
			map[string]interface{}{"type": "server_tool_use", "id": "srv_1", "name": "web_search", "input": map[string]interface{}{"query": "orchids"}},
			map[string]interface{}{"type": "web_search_tool_result", "tool_use_id": "srv_1", "content": []interface{}{
				map[string]interface{}{"type": "web_search_result", "url": "https://example.com/source"},
			}},
		}}, {Role: "user", Content: "continue"}},
	}
	chat, err := anthropicRequestToChat(req)
	testutil.NoError(t, err)
	payload, err := (&Handler{}).responsesPayloadFromChat(ModelSpec{UpstreamModel: "grok-4.5"}, &chat, true)
	testutil.NoError(t, err)
	input := payload["input"].([]interface{})
	call, _ := input[0].(map[string]interface{})
	testutil.Equal(t, call["type"], "web_search_call")
	testutil.Equal(t, call["status"], "completed")
	action := call["action"].(map[string]interface{})
	testutil.Equal(t, action["query"], "orchids")
	testutil.Equal(t, len(action["sources"].([]interface{})), 1)
}

func TestBuildToolSearchStreamHidesInternalArgumentEvents(t *testing.T) {
	payload := map[string]interface{}{"tools": []interface{}{
		map[string]interface{}{"type": "namespace", "name": "crm", "tools": []interface{}{
			map[string]interface{}{"type": "function", "name": "lookup", "parameters": map[string]interface{}{"type": "object"}},
		}},
		map[string]interface{}{"type": "tool_search", "execution": "client", "parameters": map[string]interface{}{"type": "object"}},
	}}
	aliases := collectBuildToolAliases(payload)
	source := strings.Join([]string{
		"event: response.output_item.added",
		`data: {"type":"response.output_item.added","item":{"id":"item_1","type":"function_call","call_id":"call_1","name":"tool_search","arguments":""}}`,
		"",
		"event: response.function_call_arguments.delta",
		`data: {"type":"response.function_call_arguments.delta","item_id":"item_1","delta":"{\"goal\":"}`,
		"",
		"event: response.function_call_arguments.done",
		`data: {"type":"response.function_call_arguments.done","item_id":"item_1","arguments":"{\"goal\":\"crm\"}"}`,
		"",
		"event: response.output_item.done",
		`data: {"type":"response.output_item.done","item":{"id":"item_1","type":"function_call","call_id":"call_1","name":"tool_search","arguments":"{\"goal\":\"crm\"}"}}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"tools":[{"type":"function","name":"crm__lookup"},{"type":"function","name":"tool_search"}],"output":[{"type":"function_call","name":"tool_search","arguments":"{\"goal\":\"crm\"}"}]}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	converted, err := io.ReadAll(rewriteBuildToolAliasResponse(io.NopCloser(strings.NewReader(source)), "text/event-stream", aliases))
	testutil.NoError(t, err)
	text := string(converted)
	testutil.MustNotContainAny(t, text, "response.function_call_arguments", `"name":"tool_search"`)
	for _, expected := range []string{`"type":"tool_search_call"`, `"goal":"crm"`, `"type":"namespace"`, `"name":"crm"`, "data: [DONE]"} {
		testutil.MustContain(t, text, expected)
	}
}
