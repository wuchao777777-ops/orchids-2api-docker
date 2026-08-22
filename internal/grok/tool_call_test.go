package grok

import (
	"strings"
	"testing"
)

func TestExtractMessageAndAttachmentsWithToolsFormatsHistory(t *testing.T) {
	parallel := true
	text, attachments, err := extractMessageAndAttachmentsWithTools([]ChatMessage{
		{Role: "system", Content: "you are helpful"},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: map[string]interface{}{
				"name":      "weather",
				"arguments": `{"city":"shanghai"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call_1", Name: "weather", Content: "sunny"},
		{Role: "user", Content: "what should I wear"},
	}, false, []ToolDef{{
		Type: "function",
		Function: map[string]interface{}{
			"name": "weather",
		},
	}}, "auto", parallel)
	if err != nil {
		t.Fatalf("extractMessageAndAttachmentsWithTools() error = %v", err)
	}
	if len(attachments) != 0 {
		t.Fatalf("attachments=%d want 0", len(attachments))
	}
	if text == "" || text[:17] != "# Available Tools" {
		t.Fatalf("tool prompt missing: %q", text)
	}
	if want := `<tool_call>{"name":"weather","arguments":{"city":"shanghai"}}</tool_call>`; !strings.Contains(text, want) {
		t.Fatalf("missing formatted assistant tool call: %q", text)
	}
	if !strings.Contains(text, "tool (weather, call_1): sunny") {
		t.Fatalf("missing formatted tool result: %q", text)
	}
}

func TestParseToolCalls(t *testing.T) {
	text, toolCalls := newToolCallParser([]ToolDef{{
		Type: "function",
		Function: map[string]interface{}{
			"name": "weather",
		},
	}}, "auto").parseCalls("before\n<tool_call>{\"name\":\"weather\",\"arguments\":{\"city\":\"shanghai\"}}</tool_call>\nafter")
	if len(toolCalls) != 1 {
		t.Fatalf("toolCalls=%d want 1", len(toolCalls))
	}
	if got := toolCalls[0]["function"].(map[string]interface{})["name"]; got != "weather" {
		t.Fatalf("tool name=%v want weather", got)
	}
	if !strings.Contains(text, "before") || !strings.Contains(text, "after") {
		t.Fatalf("text=%q want surrounding text preserved", text)
	}
}

func TestParseToolCalls_RepairsJSONAndHonorsForcedTool(t *testing.T) {
	parser := newToolCallParser([]ToolDef{{
		Type: "function",
		Function: map[string]interface{}{
			"name": "weather",
		},
	}}, map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name": "weather",
		},
	})
	text, toolCalls := parser.parseCalls("before\n<tool_call>```json\n{\"name\":\"weather\",\"arguments\":{\"city\":\"shanghai\",}}\n```</tool_call>\nafter")
	if len(toolCalls) != 1 {
		t.Fatalf("toolCalls=%d want 1", len(toolCalls))
	}
	if !strings.Contains(text, "before") || !strings.Contains(text, "after") {
		t.Fatalf("text=%q want surrounding text preserved", text)
	}

	_, rejected := parser.parseCalls("<tool_call>{\"name\":\"search\",\"arguments\":{}}</tool_call>")
	if len(rejected) != 0 {
		t.Fatalf("forced tool choice should reject mismatched tool, got=%v", rejected)
	}
}
