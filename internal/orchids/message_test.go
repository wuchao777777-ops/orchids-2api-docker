package orchids

import (
	"reflect"
	"strings"
	"testing"

	"orchids-api/internal/prompt"
)

func TestExtractOrchidsMessageContent_ToolResultOnly(t *testing.T) {
	t.Parallel()

	content := []interface{}{
		map[string]interface{}{
			"type":        "tool_result",
			"tool_use_id": "tool_1",
			"content":     "<tool_use_error>boom</tool_use_error>\ncontent line",
		},
	}

	text, toolResultOnly := extractOrchidsMessageContent(content, "slice")
	if !toolResultOnly {
		t.Fatal("toolResultOnly=false want true")
	}
	if strings.TrimSpace(text) != "" {
		t.Fatalf("text=%q want empty for raw tool-result-only extraction", text)
	}
}

func TestExtractOrchidsMessageContent_PreservesSystemReminderText(t *testing.T) {
	t.Parallel()

	text, toolResultOnly := extractOrchidsMessageContent("<system-reminder>keep me</system-reminder>", "string")
	if toolResultOnly {
		t.Fatal("toolResultOnly=true want false")
	}
	if text != "<system-reminder>keep me</system-reminder>" {
		t.Fatalf("text=%q want raw string content", text)
	}
}

func TestBuildOrchidsConversationHistory_ExcludesCurrentUserTurn(t *testing.T) {
	t.Parallel()

	messages := []prompt.Message{
		{Role: "user", Content: prompt.MessageContent{Text: "first"}},
		{Role: "assistant", Content: prompt.MessageContent{Text: "second"}},
		{Role: "user", Content: prompt.MessageContent{Text: "current"}},
	}

	conversation := buildOrchidsConversationMessages(messages)
	currentUserIdx := findCurrentOrchidsUserMessageIndex(conversation)
	history := buildOrchidsConversationHistory(conversation, currentUserIdx)

	if len(history) != 2 {
		t.Fatalf("len(history)=%d want 2", len(history))
	}
	if history[0]["content"] != "first" || history[1]["content"] != "second" {
		t.Fatalf("history=%#v want prior turns only", history)
	}
}

func TestExtractOrchidsMessageContent_DocumentUsesRawDataValue(t *testing.T) {
	t.Parallel()

	content := []interface{}{
		map[string]interface{}{
			"type": "document",
			"source": map[string]interface{}{
				"data": "raw-doc-data",
			},
		},
	}

	text, toolResultOnly := extractOrchidsMessageContent(content, "slice")
	if toolResultOnly {
		t.Fatal("toolResultOnly=true want false")
	}
	if text != "[document](raw-doc-data)" {
		t.Fatalf("text=%q want raw document data format", text)
	}
}

func TestExtractOrchidsToolResults_ParsesStructuredToolResults(t *testing.T) {
	t.Parallel()

	content := []interface{}{
		map[string]interface{}{
			"type":        "tool_result",
			"tool_use_id": "tool_1",
			"name":        "Read",
			"content": map[string]interface{}{
				"text": "demo result",
			},
			"is_error":  true,
			"has_input": true,
		},
	}

	got := extractOrchidsToolResults(content, "slice")
	want := []ToolResult{
		{
			Name:      "Read",
			ToolUseID: "tool_1",
			Content: map[string]interface{}{
				"text": "demo result",
			},
			IsError:  true,
			HasInput: true,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractOrchidsToolResults()=%#v want %#v", got, want)
	}
}

func TestExtractOrchidsHistoryContent_PreservesToolResultOrder(t *testing.T) {
	t.Parallel()

	content := []interface{}{
		map[string]interface{}{
			"type": "text",
			"text": "before",
		},
		map[string]interface{}{
			"type":        "tool_result",
			"tool_use_id": "tool_1",
			"name":        "Read",
			"content":     "demo result",
		},
		map[string]interface{}{
			"type": "text",
			"text": "after",
		},
	}

	got := extractOrchidsHistoryContent(content, "slice")
	want := "before\n<tool_result name=\"Read\" tool_use_id=\"tool_1\">\nafter"
	if got != want {
		t.Fatalf("extractOrchidsHistoryContent()=%q want %q", got, want)
	}
}

func TestBuildOrchidsConversationMessages_DropsMediaURLFields(t *testing.T) {
	t.Parallel()

	messages := []prompt.Message{
		{
			Role: "user",
			Content: prompt.MessageContent{
				Blocks: []prompt.ContentBlock{
					{
						Type: "image",
						Source: &prompt.ImageSource{
							MediaType: "image/png",
							Data:      "abc123",
							URL:       "https://example.com/a.png",
						},
						URL: "https://example.com/top-level.png",
					},
				},
			},
		},
	}

	conversation := buildOrchidsConversationMessages(messages)
	if len(conversation) != 1 {
		t.Fatalf("len(conversation)=%d want 1", len(conversation))
	}

	blocks, ok := conversation[0].Content.([]interface{})
	if !ok || len(blocks) != 1 {
		t.Fatalf("blocks=%#v want single block slice", conversation[0].Content)
	}
	block, ok := blocks[0].(map[string]interface{})
	if !ok {
		t.Fatalf("block=%T want map[string]interface{}", blocks[0])
	}
	if _, exists := block["url"]; exists {
		t.Fatalf("block=%#v unexpectedly contains top-level url", block)
	}
	source, ok := block["source"].(map[string]interface{})
	if !ok {
		t.Fatalf("source=%T want map[string]interface{}", block["source"])
	}
	if _, exists := source["url"]; exists {
		t.Fatalf("source=%#v unexpectedly contains url", source)
	}
}
