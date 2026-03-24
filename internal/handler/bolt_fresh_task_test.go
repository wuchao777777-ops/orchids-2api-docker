package handler

import (
	"testing"

	"orchids-api/internal/prompt"
)

func TestShouldForceFreshBoltTask_FalseForMultiTurnEditFollowup(t *testing.T) {
	req := ClaudeRequest{
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
					{Type: "tool_use", ID: "tool_1", Name: "Write", Input: map[string]interface{}{"file_path": "calculator.py"}},
				}},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
					{Type: "tool_result", ToolUseID: "tool_1", Content: "File created successfully at: calculator.py"},
				}},
			},
			{Role: "assistant", Content: prompt.MessageContent{Text: "完成！计算器已创建在项目目录中。"}},
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
		},
	}

	if shouldForceFreshBoltTask(req) {
		t.Fatalf("expected multi-turn edit follow-up to keep prior bolt history")
	}
}

func TestResetBoltMessagesForFreshTask_UsesLatestUserTurn(t *testing.T) {
	messages := []prompt.Message{
		{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
		{
			Role: "assistant",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
				{Type: "tool_use", ID: "tool_1", Name: "Write", Input: map[string]interface{}{"file_path": "calculator.py"}},
			}},
		},
		{
			Role: "user",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
				{Type: "tool_result", ToolUseID: "tool_1", Content: "File created successfully at: calculator.py"},
			}},
		},
		{Role: "assistant", Content: prompt.MessageContent{Text: "完成！计算器已创建在项目目录中。"}},
		{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
	}

	reset := resetBoltMessagesForFreshTask(messages)
	if len(reset) != 1 {
		t.Fatalf("reset messages len=%d want 1", len(reset))
	}
	if got := reset[0].ExtractText(); got != "帮我添加科学计数法" {
		t.Fatalf("reset latest user text=%q want 帮我添加科学计数法", got)
	}
}

func TestShouldForceFreshBoltTask_TrueForSingleStandalonePrompt(t *testing.T) {
	req := ClaudeRequest{
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
		},
	}

	if !shouldForceFreshBoltTask(req) {
		t.Fatalf("expected single standalone bolt prompt to force a fresh task")
	}
}

func TestShouldForceFreshBoltTask_FalseForSingleContinuationPrompt(t *testing.T) {
	req := ClaudeRequest{
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "继续"}},
		},
	}

	if shouldForceFreshBoltTask(req) {
		t.Fatalf("expected continuation-only prompt to reuse current bolt task")
	}
}

func TestShouldForceFreshBoltTask_FalseForToolResultFollowup(t *testing.T) {
	req := ClaudeRequest{
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "上传到 git"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
					{Type: "tool_use", ID: "tool_git", Name: "Bash", Input: map[string]interface{}{"command": "git status --short"}},
				}},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
					{Type: "tool_result", ToolUseID: "tool_git", Content: "M internal/bolt/client.go"},
					{Type: "text", Text: "继续"},
				}},
			},
		},
	}

	if shouldForceFreshBoltTask(req) {
		t.Fatalf("expected tool_result follow-up to stay on the current bolt task")
	}
}

func TestShouldForceFreshBoltTask_FalseForTopicClassifier(t *testing.T) {
	req := ClaudeRequest{
		System: []prompt.SystemItem{
			{Type: "text", Text: `Determine whether this starts a new conversation topic.
Return a JSON object with fields isNewTopic and title.`},
		},
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
		},
	}

	if shouldForceFreshBoltTask(req) {
		t.Fatalf("expected topic-classifier request to avoid forcing a fresh bolt task")
	}
}

func TestShouldForceFreshBoltTask_FalseForTitleGeneration(t *testing.T) {
	req := ClaudeRequest{
		System: []prompt.SystemItem{
			{Type: "text", Text: "You are Claude Code, Anthropic's official CLI for Claude."},
			{
				Type: "text",
				Text: "Generate a concise, sentence-case title (3-7 words) that captures the main topic or goal of this coding session.\n\nReturn JSON with a single \"title\" field.",
			},
		},
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "添加科学计数法"}},
		},
	}

	if shouldForceFreshBoltTask(req) {
		t.Fatalf("expected title-generation request to avoid forcing a fresh bolt task")
	}
}
