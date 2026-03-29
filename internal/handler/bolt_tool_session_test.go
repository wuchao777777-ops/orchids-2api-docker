package handler

import (
	"testing"

	"orchids-api/internal/prompt"
)

func TestInferBoltToolsFromMessages_UsesAssistantToolHistory(t *testing.T) {
	t.Parallel()

	messages := []prompt.Message{
		{
			Role: "assistant",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
				{Type: "text", Text: "Looking around"},
				{Type: "tool_use", ID: "tool_1", Name: "read"},
				{Type: "tool_use", ID: "tool_2", Name: "subagents"},
			}},
		},
		{
			Role: "user",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
				{Type: "tool_result", ToolUseID: "tool_1", Content: "skill body"},
			}},
		},
	}

	got := supportedToolNames(inferBoltToolsFromMessages(messages))
	want := []string{"Read", "Task"}
	if len(got) != len(want) {
		t.Fatalf("supportedToolNames(inferBoltToolsFromMessages) len=%d want=%d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("supportedToolNames(inferBoltToolsFromMessages)[%d]=%q want %q (%#v)", i, got[i], want[i], got)
		}
	}
}

func TestInferBoltToolsFromMessages_IgnoresNonAssistantBlocks(t *testing.T) {
	t.Parallel()

	messages := []prompt.Message{
		{
			Role: "user",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
				{Type: "tool_result", ToolUseID: "tool_1", Content: "result"},
			}},
		},
	}

	if got := inferBoltToolsFromMessages(messages); got != nil {
		t.Fatalf("inferBoltToolsFromMessages(user-only) = %#v want nil", got)
	}
}

func TestSupportedDeclaredToolNames_PreservesTavilyAlias(t *testing.T) {
	t.Parallel()

	tools := []interface{}{
		map[string]interface{}{"name": "web_search"},
		map[string]interface{}{"name": "mcp__tavily__web_search"},
	}

	got := supportedDeclaredToolNames(tools)
	want := []string{"web_search", "mcp__tavily__web_search"}
	if len(got) != len(want) {
		t.Fatalf("supportedDeclaredToolNames len=%d want=%d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("supportedDeclaredToolNames[%d]=%q want %q (%#v)", i, got[i], want[i], got)
		}
	}
}
