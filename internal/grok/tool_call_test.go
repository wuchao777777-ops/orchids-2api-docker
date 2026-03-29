package grok

import "testing"

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
	if want := `<tool_call>{"name":"weather","arguments":{"city":"shanghai"}}</tool_call>`; !contains(text, want) {
		t.Fatalf("missing formatted assistant tool call: %q", text)
	}
	if !contains(text, "tool (weather, call_1): sunny") {
		t.Fatalf("missing formatted tool result: %q", text)
	}
}



func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()))
}
