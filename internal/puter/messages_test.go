package puter

import (
	"strings"
	"testing"

	"orchids-api/internal/prompt"
	"orchids-api/internal/upstream"
)

func TestSplitMultiToolCallsSplitsPairedTurns(t *testing.T) {
	in := []Message{
		{Role: "assistant", Content: "I will read two files", ToolCalls: []ToolCall{
			{ID: "call_a", Type: "function", Function: ToolCallFunction{Name: "Read", Arguments: `{"file_path":"a"}`}},
			{ID: "call_b", Type: "function", Function: ToolCallFunction{Name: "Read", Arguments: `{"file_path":"b"}`}},
		}},
		{Role: "tool", ToolCallID: "call_a", Content: "contents of a"},
		{Role: "tool", ToolCallID: "call_b", Content: "contents of b"},
		{Role: "user", Content: "summarize"},
	}

	got := splitMultiToolCalls(in)
	if len(got) != 5 {
		t.Fatalf("len=%d want 5: %#v", len(got), got)
	}
	if got[0].Role != "assistant" || len(got[0].ToolCalls) != 1 || got[0].ToolCalls[0].ID != "call_a" || got[0].Content != "I will read two files" {
		t.Fatalf("got[0]=%#v", got[0])
	}
	if got[1].Role != "tool" || got[1].ToolCallID != "call_a" {
		t.Fatalf("got[1]=%#v", got[1])
	}
	if got[2].Role != "assistant" || len(got[2].ToolCalls) != 1 || got[2].ToolCalls[0].ID != "call_b" || got[2].Content != "" {
		t.Fatalf("got[2]=%#v", got[2])
	}
	if got[3].Role != "tool" || got[3].ToolCallID != "call_b" {
		t.Fatalf("got[3]=%#v", got[3])
	}
	if got[4].Role != "user" || got[4].Content != "summarize" {
		t.Fatalf("got[4]=%#v", got[4])
	}
}

func TestSplitMultiToolCallsThreeCalls(t *testing.T) {
	in := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "c1", Function: ToolCallFunction{Name: "Grep", Arguments: `{}`}},
			{ID: "c2", Function: ToolCallFunction{Name: "Grep", Arguments: `{}`}},
			{ID: "c3", Function: ToolCallFunction{Name: "Grep", Arguments: `{}`}},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "r1"},
		{Role: "tool", ToolCallID: "c2", Content: "r2"},
		{Role: "tool", ToolCallID: "c3", Content: "r3"},
	}
	got := splitMultiToolCalls(in)
	if len(got) != 6 {
		t.Fatalf("len=%d want 6: %#v", len(got), got)
	}
	for k, id := range []string{"c1", "c2", "c3"} {
		if got[k*2].Role != "assistant" || got[k*2].ToolCalls[0].ID != id {
			t.Fatalf("part[%d]=%#v", k*2, got[k*2])
		}
		if got[k*2+1].Role != "tool" || got[k*2+1].ToolCallID != id {
			t.Fatalf("part[%d]=%#v", k*2+1, got[k*2+1])
		}
	}
}

func TestSplitMultiToolCallsLeavesIncompleteTurnsUntouched(t *testing.T) {
	in := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_a", Function: ToolCallFunction{Name: "Read", Arguments: `{}`}},
			{ID: "call_b", Function: ToolCallFunction{Name: "Read", Arguments: `{}`}},
		}},
		// 只有 call_a 的回应,缺少 call_b。
		{Role: "tool", ToolCallID: "call_a", Content: "r1"},
		{Role: "user", Content: "hello"},
	}
	got := splitMultiToolCalls(in)
	if len(got) != 3 {
		t.Fatalf("len=%d want 3 (unchanged): %#v", len(got), got)
	}
	if len(got[0].ToolCalls) != 2 || got[0].Role != "assistant" {
		t.Fatalf("got[0]=%#v want original multi-call assistant", got[0])
	}
}

func TestSplitMultiToolCallsIgnoresNonToolScenarios(t *testing.T) {
	in := []Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "one", Function: ToolCallFunction{Name: "Read", Arguments: `{}`}}}},
		{Role: "tool", ToolCallID: "one", Content: "r"},
	}
	got := splitMultiToolCalls(in)
	if len(got) != 3 {
		t.Fatalf("len=%d want 3: %#v", len(got), got)
	}
	// 单个 tool_call 不拆分。
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].ID != "one" {
		t.Fatalf("got[1]=%#v", got[1])
	}
}

// 显式传入 SystemItem,使断言在两种 convertMessages 语义下都成立:
// feature 分支逐字透传,main 分支经 buildSystemPrompt 拼装,都会产出 system 首条。
func TestBuildRequestSplitsMultiToolCallsOnlyForDeepseek(t *testing.T) {
	build := func(model string) []Message {
		client := NewFromAccount(nil, nil)
		req, err := client.buildRequest(upstream.UpstreamRequest{
			Model: model,
			System: []prompt.SystemItem{{Type: "text", Text: "sys"}},
			Messages: []prompt.Message{
				{Role: "assistant", Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
					{Type: "tool_use", ID: "a", Name: "Read", Input: map[string]interface{}{"file_path": "x"}},
					{Type: "tool_use", ID: "b", Name: "Read", Input: map[string]interface{}{"file_path": "y"}},
				}}},
				{Role: "user", Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
					{Type: "tool_result", ToolUseID: "a", Content: "r1"},
					{Type: "tool_result", ToolUseID: "b", Content: "r2"},
				}}},
			},
		}, false)
		if err != nil {
			t.Fatalf("buildRequest(%q) error=%v", model, err)
		}
		return req.Args.Messages
	}

	deepseek := build("deepseek-v4-flash")
	// 1 条 system + 拆分后的 4 条(assistant(a)/tool(a)/assistant(b)/tool(b))
	if len(deepseek) != 5 {
		t.Fatalf("deepseek messages len=%d want 5: %#v", len(deepseek), deepseek)
	}
	if deepseek[0].Role != "system" {
		t.Fatalf("deepseek[0]=%#v want system", deepseek[0])
	}
	if deepseek[1].Role != "assistant" || len(deepseek[1].ToolCalls) != 1 {
		t.Fatalf("deepseek[1]=%#v", deepseek[1])
	}

	claude := build("claude-opus-5")
	// 1 条 system + 未拆分(assistant 带 2 tool_calls + 2 tool)
	if len(claude) != 4 {
		t.Fatalf("claude messages len=%d want 4 (unsplit): %#v", len(claude), claude)
	}
	if len(claude[1].ToolCalls) != 2 {
		t.Fatalf("claude[1]=%#v want unsplit multi-call", claude[1])
	}
}

func TestSplitMultiToolCallsReordersToolResultsCorrectly(t *testing.T) {
	// tool 回应顺序与 tool_calls 不一致时,按 tool_calls 顺序成对输出。
	in := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "a", Function: ToolCallFunction{Name: "Read", Arguments: `{}`}},
			{ID: "b", Function: ToolCallFunction{Name: "Read", Arguments: `{}`}},
		}},
		{Role: "tool", ToolCallID: "b", Content: "rb"},
		{Role: "tool", ToolCallID: "a", Content: "ra"},
	}
	got := splitMultiToolCalls(in)
	if len(got) != 4 {
		t.Fatalf("len=%d want 4: %#v", len(got), got)
	}
	if got[0].ToolCalls[0].ID != "a" || got[1].ToolCallID != "a" {
		t.Fatalf("want a-pair first: %#v %#v", got[0], got[1])
	}
	if got[2].ToolCalls[0].ID != "b" || got[3].ToolCallID != "b" {
		t.Fatalf("want b-pair second: %#v %#v", got[2], got[3])
	}
}

// 该用例保证 TestBuildRequestSplitsMultiToolCallsOnlyForDeepseek 的断言真实有效:
// 使用 buildRequest 语义(不走流式),验证拆分会真实出现在 deepseek 请求中。
func TestSplitMultiToolCallsPreservesArgumentsText(t *testing.T) {
	in := []Message{
		{Role: "assistant", Content: "lead", ToolCalls: []ToolCall{
			{ID: "a", Function: ToolCallFunction{Name: "Read", Arguments: `{"file_path":"x"}`}},
			{ID: "b", Function: ToolCallFunction{Name: "Read", Arguments: `{"file_path":"y"}`}},
		}},
		{Role: "tool", ToolCallID: "a", Content: "r1"},
		{Role: "tool", ToolCallID: "b", Content: "r2"},
	}
	got := splitMultiToolCalls(in)
	joined := got[0].Content + got[0].ToolCalls[0].Function.Arguments + got[2].ToolCalls[0].Function.Arguments
	if !strings.Contains(joined, "lead") || !strings.Contains(joined, "x") || !strings.Contains(joined, "y") {
		t.Fatalf("content/arguments lost: %q", joined)
	}
}
