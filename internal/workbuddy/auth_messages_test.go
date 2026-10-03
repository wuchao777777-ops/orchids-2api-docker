package workbuddy

import (
	"strings"
	"testing"

	"orchids-api/internal/prompt"
	"orchids-api/internal/testutil"
	"orchids-api/internal/upstream"
)

func TestBuildMessages_RequiresSystemFirst(t *testing.T) {
	t.Parallel()

	messages := buildMessages(upstream.UpstreamRequest{Messages: []prompt.Message{userMessage(t, "hello")}})
	testutil.Equal(t, len(messages), 2)
	testutil.Equal(t, messages[0].Role, "system")
	testutil.Equal(t, messages[0].Content, defaultSystem)
	testutil.Equal(t, messages[1].Role, "user")
	testutil.Equal(t, messages[1].Content, "hello")
}

func TestBuildMessages_NormalizesDeveloperRole(t *testing.T) {
	t.Parallel()

	// `developer` is OpenAI's alias for the system role; the upstream rejects
	// it, and the rewrite must preserve content and position.
	messages := buildMessages(upstream.UpstreamRequest{Messages: []prompt.Message{roleMessage(t, "developer", "stay terse")}})
	testutil.Equal(t, len(messages), 1)
	testutil.Equal(t, messages[0].Role, "system")
	testutil.Equal(t, messages[0].Content, "stay terse")
}

func TestBuildMessages_KeepsSystemItemsAndToolResults(t *testing.T) {
	t.Parallel()

	messages := buildMessages(upstream.UpstreamRequest{
		System: []prompt.SystemItem{{Type: "text", Text: "be brief"}},
		Messages: []prompt.Message{{
			Role: "assistant",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
				{Type: "tool_use", ID: "toolu_1", Name: "run", Input: map[string]interface{}{}},
			}},
		}, {
			Role: "user",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
				{Type: "text", Text: "run it"},
				{Type: "tool_result", ToolUseID: "toolu_1", Content: "ok"},
			}},
		}},
	})
	testutil.Equal(t, len(messages), 4)
	testutil.Equal(t, messages[0].Role, "system")
	testutil.Equal(t, messages[0].Content, "be brief")
	testutil.Equal(t, messages[3].Role, "tool")
	testutil.Equal(t, messages[3].ToolCallID, "toolu_1")
	testutil.Equal(t, messages[3].Content, "ok")
}

// Claude Code declares itself in the system array. The upstream's policy gate
// answers code=11128 ("blocked by security policy") for such a request, so both
// markers must be dropped before the body is built.
func TestBuildMessages_DropsAnthropicClientMarkers(t *testing.T) {
	t.Parallel()

	messages := buildMessages(upstream.UpstreamRequest{
		System: []prompt.SystemItem{
			{Type: "text", Text: "x-anthropic-billing-header: cc_version=2.1.268.e0e; cc_entrypoint=claude-vscode;"},
			{Type: "text", Text: "You are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK."},
			{Type: "text", Text: "be brief"},
		},
		Messages: []prompt.Message{userMessage(t, "hello")},
	})

	testutil.Equal(t, len(messages), 2)
	testutil.Equal(t, messages[0].Role, "system")
	testutil.Equal(t, messages[0].Content, "be brief")
	for _, message := range messages {
		if strings.Contains(message.Content, "anthropic-billing-header") ||
			strings.Contains(message.Content, "official CLI for Claude") {
			t.Fatalf("client marker was forwarded: %+v", message)
		}
	}
}

func TestBuildMessages_ClientMarkerOnlyFallsBackToDefaultSystem(t *testing.T) {
	t.Parallel()

	messages := buildMessages(upstream.UpstreamRequest{
		System:   []prompt.SystemItem{{Type: "text", Text: "You are Claude Code"}},
		Messages: []prompt.Message{userMessage(t, "hello")},
	})

	// Dropping the persona line must not leave the upstream without a leading
	// system message.
	testutil.Equal(t, len(messages), 2)
	testutil.Equal(t, messages[0].Role, "system")
	testutil.Equal(t, messages[0].Content, defaultSystem)
}

func TestBuildMessages_DropsClientMarkerSystemMessage(t *testing.T) {
	t.Parallel()

	messages := buildMessages(upstream.UpstreamRequest{Messages: []prompt.Message{
		roleMessage(t, "system", "x-anthropic-billing-header: cc_version=2.1.268.e0e; cc_entrypoint=claude-vscode;"),
		userMessage(t, "hello"),
	}})

	for _, message := range messages {
		testutil.MustNotContain(t, message.Content, "anthropic-billing-header")
	}
	testutil.Equal(t, len(messages), 2)
	testutil.Equal(t, messages[0].Content, defaultSystem)
}

func TestBuildMessages_KeepsOrdinaryClaudeMention(t *testing.T) {
	t.Parallel()

	const instruction = "You are an interactive agent. Follow the Claude Code project conventions."

	messages := buildMessages(upstream.UpstreamRequest{
		System:   []prompt.SystemItem{{Type: "text", Text: instruction}},
		Messages: []prompt.Message{userMessage(t, "hello")},
	})
	testutil.Equal(t, messages[0].Content, instruction)
}

func TestBuildMessagesDropsDanglingToolResult(t *testing.T) {
	t.Parallel()
	messages := buildMessages(upstream.UpstreamRequest{Messages: []prompt.Message{{
		Role: "user",
		Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{{
			Type: "tool_result", ToolUseID: "missing", Content: "must not be sent",
		}}},
	}}})
	for _, message := range messages {
		testutil.NotEqual(t, message.Role, "tool")
	}
}

func TestBuildMessages_DefaultSystemPromptOnly(t *testing.T) {
	t.Parallel()

	// A request without any history still has to produce a system-first pair,
	// because the upstream rejects anything else with code=11128.
	messages := buildMessages(upstream.UpstreamRequest{})
	testutil.Equal(t, len(messages), 2)
	testutil.Equal(t, messages[0].Role, "system")
	testutil.Equal(t, messages[0].Content, defaultSystem)
	testutil.Equal(t, messages[1].Role, "user")
}
