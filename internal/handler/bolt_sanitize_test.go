package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goccy/go-json"

	"orchids-api/internal/prompt"
	"orchids-api/internal/upstream"
)

func TestSanitizeBoltMessages_TrimsHistoricalHeavyToolResults(t *testing.T) {
	longRead := strings.Repeat("history line from memory file\n", 80)
	messages := []prompt.Message{
		{Role: "user", Content: prompt.MessageContent{Text: "old request"}},
		{
			Role: "assistant",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
				{Type: "tool_use", ID: "tool_web_1", Name: "web_fetch"},
			}},
		},
		{
			Role: "user",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
				{Type: "tool_result", ToolUseID: "tool_web_1", Content: "BBC article body\n" + strings.Repeat("news ", 200)},
			}},
		},
		{
			Role: "assistant",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
				{Type: "tool_use", ID: "tool_read_1", Name: "Read"},
			}},
		},
		{
			Role: "user",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
				{Type: "tool_result", ToolUseID: "tool_read_1", Content: "/home/zhangdailin/.openclaw/workspace/memory/2026-03-30.md\n" + longRead},
			}},
		},
		{Role: "assistant", Content: prompt.MessageContent{Text: "recent assistant 1"}},
		{Role: "user", Content: prompt.MessageContent{Text: "recent user 1"}},
		{Role: "assistant", Content: prompt.MessageContent{Text: "recent assistant 2"}},
		{Role: "user", Content: prompt.MessageContent{Text: "recent user 2"}},
	}

	got, stats := sanitizeBoltMessages(messages)
	if !stats.Changed {
		t.Fatalf("expected sanitize to report changes")
	}
	if len(got) != len(messages) {
		t.Fatalf("messages len=%d want=%d", len(got), len(messages))
	}

	oldWebResult := strings.TrimSpace(extractBoltBlockContentText(got[2].Content.Blocks[0].Content))
	if !strings.Contains(oldWebResult, "historical web_fetch result omitted") {
		t.Fatalf("unexpected web result summary: %q", oldWebResult)
	}

	oldReadResult := strings.TrimSpace(extractBoltBlockContentText(got[4].Content.Blocks[0].Content))
	if !strings.Contains(strings.ToLower(oldReadResult), "historical read result omitted") {
		t.Fatalf("unexpected read result summary: %q", oldReadResult)
	}

	if got[8].ExtractText() != "recent user 2" {
		t.Fatalf("recent message should stay intact, got %q", got[8].ExtractText())
	}
}

func TestSanitizeBoltMessages_KeepsRecentToolResultTurnUntouched(t *testing.T) {
	recentResult := strings.Repeat("recent output ", 100)
	messages := []prompt.Message{
		{Role: "user", Content: prompt.MessageContent{Text: "turn1"}},
		{Role: "assistant", Content: prompt.MessageContent{Text: "turn2"}},
		{Role: "user", Content: prompt.MessageContent{Text: "turn3"}},
		{Role: "assistant", Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{{Type: "tool_use", ID: "tool_recent", Name: "Read"}}}},
		{Role: "user", Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{{Type: "tool_result", ToolUseID: "tool_recent", Content: recentResult}}}},
	}

	got, _ := sanitizeBoltMessages(messages)
	if got[4].Content.Blocks[0].Content.(string) != recentResult {
		t.Fatalf("recent tool result should remain untouched")
	}
}

func TestHandleMessages_BoltSanitizesBeforeForwarding(t *testing.T) {
	client := &fakePayloadClient{
		eventsByOp: [][]upstream.SSEMessage{
			puterDirectTextEvents("msg_bolt_sanitize", "claude-sonnet-4-6", "ok", 3),
		},
	}
	h := newTestHandler(client)

	heavyHistory := strings.Repeat("web snippet\n", 200)
	body, err := json.Marshal(map[string]any{
		"model":  "claude-sonnet-4-6",
		"stream": false,
		"messages": []map[string]any{
			{"role": "user", "content": "old"},
			{"role": "assistant", "content": []map[string]any{
				{"type": "tool_use", "id": "tool_web_1", "name": "web_fetch", "input": map[string]any{"url": "https://example.com"}},
			}},
			{"role": "user", "content": []map[string]any{
				{"type": "tool_result", "tool_use_id": "tool_web_1", "content": heavyHistory},
			}},
			{"role": "assistant", "content": "intermediate"},
			{"role": "user", "content": "another recent turn"},
			{"role": "assistant", "content": "recent assistant turn"},
			{"role": "user", "content": "latest prompt"},
		},
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bolt/v1/messages", bytes.NewReader(body))
	h.HandleMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	calls := client.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(calls))
	}
	if len(calls[0].Messages) != 7 {
		t.Fatalf("expected 7 forwarded messages, got %d", len(calls[0].Messages))
	}

	got := strings.TrimSpace(extractBoltBlockContentText(calls[0].Messages[2].Content.Blocks[0].Content))
	if !strings.Contains(got, "historical web_fetch result omitted") {
		t.Fatalf("expected forwarded bolt history to be sanitized, got %q", got)
	}
}
