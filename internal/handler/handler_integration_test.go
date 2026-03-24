package handler

import (
	"bytes"
	"context"
	"github.com/goccy/go-json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/upstream"
)

type mockUpstream struct {
	events       []upstream.SSEMessage
	eventBatches [][]upstream.SSEMessage
	capturedReqs []upstream.UpstreamRequest
}

type panicUpstream struct{}

func (m *mockUpstream) SendRequest(ctx context.Context, prompt string, chatHistory []interface{}, model string, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	for _, e := range m.events {
		onMessage(e)
	}
	return nil
}

func (m *mockUpstream) SendRequestWithPayload(ctx context.Context, req upstream.UpstreamRequest, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	m.capturedReqs = append(m.capturedReqs, req)
	events := m.events
	if len(m.eventBatches) > 0 {
		idx := len(m.capturedReqs) - 1
		if idx >= len(m.eventBatches) {
			idx = len(m.eventBatches) - 1
		}
		events = m.eventBatches[idx]
	}
	for _, e := range events {
		onMessage(e)
	}
	return nil
}

func (p *panicUpstream) SendRequest(ctx context.Context, prompt string, chatHistory []interface{}, model string, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	panic("unexpected upstream request")
}

func (p *panicUpstream) SendRequestWithPayload(ctx context.Context, req upstream.UpstreamRequest, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	panic("unexpected upstream request")
}

func TestHandleMessages_Orchids_StreamAndJSON(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &mockUpstream{events: []upstream.SSEMessage{
		{Type: "model", Event: map[string]any{"type": "text-start"}},
		{Type: "model", Event: map[string]any{"type": "text-delta", "delta": "hello"}},
		{Type: "model", Event: map[string]any{"type": "text-end"}},
		{Type: "model", Event: map[string]any{"type": "finish", "finishReason": "stop"}},
	}}

	mkBody := func(stream bool) []byte {
		payload := map[string]any{
			"model":    "claude-3-5-sonnet",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
			"system":   []any{},
			"stream":   stream,
		}
		b, _ := json.Marshal(payload)
		return b
	}

	// non-stream JSON
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://x/orchids/v1/messages", bytes.NewReader(mkBody(false)))
		h.HandleMessages(rec, req)
		if rec.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		ct := rec.Header().Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Fatalf("expected json content-type, got %q", ct)
		}
		if !strings.Contains(rec.Body.String(), "\"type\":\"message\"") {
			t.Fatalf("expected message json, got: %s", rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "hello") {
			t.Fatalf("expected upstream text in response")
		}
	}

	// stream SSE
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://x/orchids/v1/messages", bytes.NewReader(mkBody(true)))
		h.HandleMessages(rec, req)
		if rec.Code != 200 {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		ct := rec.Header().Get("Content-Type")
		if !strings.Contains(ct, "text/event-stream") {
			t.Fatalf("expected sse content-type, got %q", ct)
		}
		out := rec.Body.String()
		if !strings.Contains(out, "event: message_start") {
			t.Fatalf("expected message_start, got: %s", out)
		}
		if !strings.Contains(out, "hello") {
			t.Fatalf("expected text delta in SSE")
		}
		if !strings.Contains(out, "event: message_stop") {
			t.Fatalf("expected message_stop, got: %s", out)
		}
	}
}

func TestHandleMessages_CurrentWorkdir_LocalAnthropicJSON(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &panicUpstream{}

	body, _ := json.Marshal(map[string]any{
		"model":    "claude-3-5-sonnet",
		"messages": []map[string]any{{"role": "user", "content": "当前运行的目录"}},
		"system":   []any{},
		"stream":   false,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/orchids/v1/messages", bytes.NewReader(body))
	req.Header.Set("X-Workdir", `C:\Users\zhangdailin\Desktop\新建文件夹`)
	h.HandleMessages(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("expected json content type, got %q", rec.Header().Get("Content-Type"))
	}
	out := rec.Body.String()
	if !strings.Contains(out, `"type":"message"`) {
		t.Fatalf("expected anthropic message payload, got: %s", out)
	}
	if !strings.Contains(out, `C:\\Users\\zhangdailin\\Desktop\\新建文件夹`) {
		t.Fatalf("expected exact workdir in response, got: %s", out)
	}
}

func TestHandleMessages_CurrentWorkdir_LocalOpenAIStream(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &panicUpstream{}

	body, _ := json.Marshal(map[string]any{
		"model":    "claude-opus-4-6",
		"messages": []map[string]any{{"role": "user", "content": "workspace path"}},
		"system":   []any{},
		"stream":   true,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/bolt/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("X-Workdir", `C:\Users\zhangdailin\Desktop\新建文件夹 (2)`)
	h.HandleMessages(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected sse content type, got %q", rec.Header().Get("Content-Type"))
	}
	out := rec.Body.String()
	if !strings.Contains(out, "chat.completion.chunk") {
		t.Fatalf("expected openai stream chunk, got: %s", out)
	}
	if !strings.Contains(out, `C:\\Users\\zhangdailin\\Desktop\\新建文件夹 (2)`) {
		t.Fatalf("expected exact workdir in stream response, got: %s", out)
	}
	if !strings.Contains(out, "[DONE]") {
		t.Fatalf("expected done marker, got: %s", out)
	}
}

func TestHandleMessages_Orchids_DoesNotFilterToolCallsByDeclaredTools(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &mockUpstream{events: []upstream.SSEMessage{
		{Type: "model.tool-call", Event: map[string]any{
			"toolCallId": "tool_edit_1",
			"toolName":   "Edit",
			"input":      `{"file_path":"/tmp/demo.txt","old_string":"hello","new_string":"world"}`,
		}},
		{Type: "model", Event: map[string]any{"type": "finish", "finishReason": "tool_use"}},
	}}

	body, _ := json.Marshal(map[string]any{
		"model":    "claude-3-5-sonnet",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"system":   []any{},
		"stream":   false,
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name": "Read",
					"parameters": map[string]any{
						"type": "object",
					},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/orchids/v1/messages", bytes.NewReader(body))
	h.HandleMessages(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if !strings.Contains(out, `"type":"tool_use"`) {
		t.Fatalf("expected tool_use block, got: %s", out)
	}
	if !strings.Contains(out, `"name":"Edit"`) {
		t.Fatalf("expected Edit tool call to pass through, got: %s", out)
	}
}

func TestHandleMessages_Warp_StreamAndJSON(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &mockUpstream{events: []upstream.SSEMessage{
		{Type: "model", Event: map[string]any{"type": "conversation_id", "id": "conv1"}},
		{Type: "model", Event: map[string]any{"type": "text-start"}},
		{Type: "model", Event: map[string]any{"type": "text-delta", "delta": "warp-hi"}},
		{Type: "model", Event: map[string]any{"type": "finish", "finishReason": "stop"}},
	}}

	mkBody := func(stream bool) []byte {
		payload := map[string]any{
			"model":    "claude-3-5-sonnet",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
			"system":   []any{},
			"stream":   stream,
			// include stable conversation_id so handler will store upstream conv id
			"conversation_id": "c1",
		}
		b, _ := json.Marshal(payload)
		return b
	}

	// non-stream JSON
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://x/warp/v1/messages", bytes.NewReader(mkBody(false)))
		h.HandleMessages(rec, req)
		if rec.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "warp-hi") {
			t.Fatalf("expected upstream text in response")
		}
	}

	// stream SSE
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://x/warp/v1/messages", bytes.NewReader(mkBody(true)))
		h.HandleMessages(rec, req)
		out := rec.Body.String()
		if !strings.Contains(out, "warp-hi") {
			t.Fatalf("expected text delta in SSE")
		}
	}

	// ensure upstream conversation id stored via SessionStore
	convKey := conversationKeyForRequest(httptest.NewRequest(http.MethodPost, "http://x/warp/v1/messages", nil), ClaudeRequest{ConversationID: "c1"})
	got, _ := h.sessionStore.GetConvID(context.Background(), convKey)
	if got != "conv1" {
		t.Fatalf("expected stored upstream conversation id conv1, got %q", got)
	}
}

func TestHandleMessages_Bolt_OpenAINonStreamJSON(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &mockUpstream{events: []upstream.SSEMessage{
		{Type: "model", Event: map[string]any{"type": "text-start"}},
		{Type: "model", Event: map[string]any{"type": "text-delta", "delta": "hello from bolt"}},
		{Type: "model.tool-call", Event: map[string]any{
			"toolCallId": "call_1",
			"toolName":   "Read",
			"input":      `{"file_path":"/tmp/demo.txt"}`,
		}},
		{Type: "model", Event: map[string]any{"type": "finish", "finishReason": "tool_use"}},
	}}

	body, _ := json.Marshal(map[string]any{
		"model":    "claude-opus-4-6",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"system":   []any{},
		"tools": []map[string]any{{
			"name":        "Read",
			"description": "read a file",
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string"},
				},
			},
		}},
		"stream": false,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/bolt/v1/chat/completions", bytes.NewReader(body))
	h.HandleMessages(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("expected json content-type, got %q", ct)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v; body=%s", err, rec.Body.String())
	}
	if got["object"] != "chat.completion" {
		t.Fatalf("expected chat.completion object, got %#v", got["object"])
	}
	choices, ok := got["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("expected one choice, got %#v", got["choices"])
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		t.Fatalf("expected choice object, got %#v", choices[0])
	}
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("expected tool_calls finish_reason, got %#v", choice["finish_reason"])
	}
	message, ok := choice["message"].(map[string]any)
	if !ok {
		t.Fatalf("expected message object, got %#v", choice["message"])
	}
	if message["role"] != "assistant" {
		t.Fatalf("expected assistant role, got %#v", message["role"])
	}
	if message["content"] != "hello from bolt" {
		t.Fatalf("expected text content, got %#v", message["content"])
	}
	toolCalls, ok := message["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", message["tool_calls"])
	}
	toolCall, ok := toolCalls[0].(map[string]any)
	if !ok {
		t.Fatalf("expected tool call object, got %#v", toolCalls[0])
	}
	if toolCall["type"] != "function" {
		t.Fatalf("expected function tool type, got %#v", toolCall["type"])
	}
	function, ok := toolCall["function"].(map[string]any)
	if !ok {
		t.Fatalf("expected function object, got %#v", toolCall["function"])
	}
	if function["name"] != "Read" {
		t.Fatalf("expected Read tool name, got %#v", function["name"])
	}
	if function["arguments"] != `{"file_path":"/tmp/demo.txt"}` {
		t.Fatalf("unexpected tool arguments: %#v", function["arguments"])
	}
	usage, ok := got["usage"].(map[string]any)
	if !ok {
		t.Fatalf("expected usage object, got %#v", got["usage"])
	}
	if _, ok := usage["prompt_tokens"]; !ok {
		t.Fatalf("expected prompt_tokens in usage, got %#v", usage)
	}
	if _, ok := usage["completion_tokens"]; !ok {
		t.Fatalf("expected completion_tokens in usage, got %#v", usage)
	}
	if _, ok := usage["total_tokens"]; !ok {
		t.Fatalf("expected total_tokens in usage, got %#v", usage)
	}
}

func TestHandleMessages_BoltRestoresSessionToolsWhenFollowupOmitsTools(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	up := &mockUpstream{
		eventBatches: [][]upstream.SSEMessage{
			{
				{Type: "model", Event: map[string]any{"type": "text-start"}},
				{Type: "model", Event: map[string]any{"type": "text-delta", "delta": "created"}},
				{Type: "model", Event: map[string]any{"type": "finish", "finishReason": "stop"}},
			},
			{
				{Type: "model.tool-call", Event: map[string]any{
					"toolCallId": "tool_read_1",
					"toolName":   "Read",
					"input":      `{"file_path":"calculator.py"}`,
				}},
				{Type: "model", Event: map[string]any{"type": "finish", "finishReason": "tool_use"}},
			},
		},
	}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = up

	firstBody, _ := json.Marshal(map[string]any{
		"model":    "claude-opus-4-6",
		"messages": []map[string]any{{"role": "user", "content": "帮我用python写一个计算器"}},
		"system":   []any{},
		"tools": []map[string]any{
			{"name": "Read"},
			{"name": "Write"},
			{"name": "Edit"},
		},
		"metadata": map[string]any{"user_id": "bolt-user-1"},
		"stream":   false,
	})

	firstRec := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodPost, "http://x/bolt/v1/messages", bytes.NewReader(firstBody))
	firstReq.Header.Set("X-Workdir", `C:\Users\zhangdailin\Desktop\新建文件夹 (2)`)
	h.HandleMessages(firstRec, firstReq)
	if firstRec.Code != 200 {
		t.Fatalf("expected first request 200, got %d: %s", firstRec.Code, firstRec.Body.String())
	}

	secondBody, _ := json.Marshal(map[string]any{
		"model":    "claude-opus-4-6",
		"messages": []map[string]any{{"role": "user", "content": "帮我添加科学计数法"}},
		"system":   []any{},
		"metadata": map[string]any{"user_id": "bolt-user-1"},
		"stream":   false,
	})

	secondRec := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, "http://x/bolt/v1/messages", bytes.NewReader(secondBody))
	secondReq.Header.Set("X-Workdir", `C:\Users\zhangdailin\Desktop\新建文件夹 (2)`)
	h.HandleMessages(secondRec, secondReq)
	if secondRec.Code != 200 {
		t.Fatalf("expected second request 200, got %d: %s", secondRec.Code, secondRec.Body.String())
	}

	if len(up.capturedReqs) != 2 {
		t.Fatalf("capturedReqs len=%d want 2", len(up.capturedReqs))
	}
	restored := supportedToolNames(up.capturedReqs[1].Tools)
	want := []string{"Read", "Write", "Edit"}
	if len(restored) != len(want) {
		t.Fatalf("restored tools len=%d want %d (%#v)", len(restored), len(want), restored)
	}
	for i := range want {
		if restored[i] != want[i] {
			t.Fatalf("restored tools[%d]=%q want %q (%#v)", i, restored[i], want[i], restored)
		}
	}
	if up.capturedReqs[1].NoTools {
		t.Fatal("expected restored bolt follow-up to keep tools enabled")
	}
	if !strings.Contains(secondRec.Body.String(), `"type":"tool_use"`) {
		t.Fatalf("expected second response to include tool_use after restoring tools, got: %s", secondRec.Body.String())
	}
	if !strings.Contains(secondRec.Body.String(), `"name":"Read"`) {
		t.Fatalf("expected restored tool call to pass through, got: %s", secondRec.Body.String())
	}
}

func TestHandleMessages_Puter_StreamAndJSON(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &mockUpstream{events: []upstream.SSEMessage{
		{Type: "model", Event: map[string]any{"type": "text-start"}},
		{Type: "model", Event: map[string]any{"type": "text-delta", "delta": "puter-hi"}},
		{Type: "model", Event: map[string]any{"type": "finish", "finishReason": "stop"}},
	}}

	mkBody := func(stream bool) []byte {
		payload := map[string]any{
			"model":    "claude-opus-4-5",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
			"system":   []any{},
			"stream":   stream,
		}
		b, _ := json.Marshal(payload)
		return b
	}

	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://x/puter/v1/messages", bytes.NewReader(mkBody(false)))
		h.HandleMessages(rec, req)
		if rec.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "puter-hi") {
			t.Fatalf("expected upstream text in response")
		}
	}

	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://x/puter/v1/messages", bytes.NewReader(mkBody(true)))
		h.HandleMessages(rec, req)
		out := rec.Body.String()
		if !strings.Contains(out, "puter-hi") {
			t.Fatalf("expected text delta in SSE")
		}
	}
}

func TestHandleMessages_Puter_DirectSSE_NonStreamJSON(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &mockUpstream{events: []upstream.SSEMessage{
		{Type: "message_start", Event: map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":    "msg_test",
				"type":  "message",
				"role":  "assistant",
				"model": "claude-sonnet-4-6",
			},
		}},
		{Type: "content_block_start", Event: map[string]any{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		}},
		{Type: "content_block_delta", Event: map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{
				"type": "text_delta",
				"text": "puter-direct",
			},
		}},
		{Type: "content_block_stop", Event: map[string]any{
			"type":  "content_block_stop",
			"index": 0,
		}},
		{Type: "message_delta", Event: map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason": "end_turn",
			},
			"usage": map[string]any{
				"output_tokens": 3,
			},
		}},
		{Type: "message_stop", Event: map[string]any{
			"type": "message_stop",
		}},
	}}

	body, _ := json.Marshal(map[string]any{
		"model":    "claude-sonnet-4-6",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"system":   []any{},
		"stream":   false,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/puter/v1/messages", bytes.NewReader(body))
	h.HandleMessages(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "puter-direct") {
		t.Fatalf("expected direct SSE text in response, got: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"content":null`) {
		t.Fatalf("did not expect null content, got: %s", rec.Body.String())
	}
}

func TestHandleMessages_Puter_DirectSSE_NonStreamToolUseJSON(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &mockUpstream{events: []upstream.SSEMessage{
		{Type: "message_start", Event: map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":    "msg_tool_use",
				"type":  "message",
				"role":  "assistant",
				"model": "claude-sonnet-4-6",
			},
		}},
		{Type: "content_block_start", Event: map[string]any{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    "tool_write_1",
				"name":  "Write",
				"input": map[string]any{},
			},
		}},
		{Type: "content_block_delta", Event: map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": `{"file_path":"note.txt","content":"alpha beta"}`,
			},
		}},
		{Type: "content_block_stop", Event: map[string]any{
			"type":  "content_block_stop",
			"index": 0,
		}},
		{Type: "message_delta", Event: map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason": "tool_use",
			},
			"usage": map[string]any{
				"output_tokens": 7,
			},
		}},
		{Type: "message_stop", Event: map[string]any{
			"type": "message_stop",
		}},
	}}

	body, _ := json.Marshal(map[string]any{
		"model":    "claude-sonnet-4-6",
		"messages": []map[string]any{{"role": "user", "content": "use the Write tool"}},
		"system":   []any{},
		"stream":   false,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/puter/v1/messages", bytes.NewReader(body))
	h.HandleMessages(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if strings.Contains(out, `"content":null`) {
		t.Fatalf("expected tool_use content blocks, got: %s", out)
	}
	if !strings.Contains(out, `"type":"tool_use"`) {
		t.Fatalf("expected tool_use block in JSON body, got: %s", out)
	}
	if !strings.Contains(out, `"name":"Write"`) {
		t.Fatalf("expected Write tool call in JSON body, got: %s", out)
	}
	if !strings.Contains(out, `"stop_reason":"tool_use"`) {
		t.Fatalf("expected stop_reason tool_use, got: %s", out)
	}
}

func TestHandleMessages_Puter_DirectSSE_StreamToolUseJSON(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &mockUpstream{events: []upstream.SSEMessage{
		{Type: "message_start", Event: map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":    "msg_tool_use_stream",
				"type":  "message",
				"role":  "assistant",
				"model": "claude-sonnet-4-6",
			},
		}},
		{Type: "content_block_start", Event: map[string]any{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    "tool_write_stream_1",
				"name":  "Write",
				"input": map[string]any{},
			},
		}},
		{Type: "content_block_delta", Event: map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": `{"file_path":"note.txt","content":"alpha beta"}`,
			},
		}},
		{Type: "content_block_stop", Event: map[string]any{
			"type":  "content_block_stop",
			"index": 0,
		}},
		{Type: "message_delta", Event: map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason": "tool_use",
			},
			"usage": map[string]any{
				"output_tokens": 7,
			},
		}},
		{Type: "message_stop", Event: map[string]any{
			"type": "message_stop",
		}},
	}}

	body, _ := json.Marshal(map[string]any{
		"model":    "claude-sonnet-4-6",
		"messages": []map[string]any{{"role": "user", "content": "use the Write tool"}},
		"system":   []any{},
		"stream":   true,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/puter/v1/messages", bytes.NewReader(body))
	h.HandleMessages(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if !strings.Contains(out, `event: content_block_start`) {
		t.Fatalf("expected content_block_start SSE, got: %s", out)
	}
	if !strings.Contains(out, `"type":"tool_use"`) {
		t.Fatalf("expected tool_use block in SSE, got: %s", out)
	}
	if !strings.Contains(out, `"name":"Write"`) {
		t.Fatalf("expected Write tool name in SSE, got: %s", out)
	}
	if !strings.Contains(out, `alpha beta`) {
		t.Fatalf("expected write payload in SSE, got: %s", out)
	}
	if !strings.Contains(out, `"stop_reason":"tool_use"`) {
		t.Fatalf("expected stop_reason tool_use in SSE, got: %s", out)
	}
}

func TestHandleMessages_Puter_BoltStyleToolCall_StreamAndJSON(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &mockUpstream{events: []upstream.SSEMessage{
		{Type: "model.tool-call", Event: map[string]any{
			"toolCallId": "tool_write_bolt_style_1",
			"toolName":   "Write",
			"input":      `{"file_path":"note.txt","content":"alpha beta"}`,
		}},
		{Type: "model.finish", Event: map[string]any{
			"finishReason": "tool_use",
			"usage": map[string]any{
				"inputTokens":  12,
				"outputTokens": 7,
			},
		}},
	}}

	mkBody := func(stream bool) []byte {
		body, _ := json.Marshal(map[string]any{
			"model":    "claude-opus-4-5",
			"messages": []map[string]any{{"role": "user", "content": "use the Write tool"}},
			"system":   []any{},
			"stream":   stream,
		})
		return body
	}

	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://x/puter/v1/messages", bytes.NewReader(mkBody(false)))
		h.HandleMessages(rec, req)
		if rec.Code != 200 {
			t.Fatalf("non-stream expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		out := rec.Body.String()
		if !strings.Contains(out, `"type":"tool_use"`) || !strings.Contains(out, `"name":"Write"`) {
			t.Fatalf("expected Write tool_use in JSON, got: %s", out)
		}
		if !strings.Contains(out, `"stop_reason":"tool_use"`) {
			t.Fatalf("expected stop_reason tool_use in JSON, got: %s", out)
		}
	}

	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://x/puter/v1/messages", bytes.NewReader(mkBody(true)))
		h.HandleMessages(rec, req)
		if rec.Code != 200 {
			t.Fatalf("stream expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		out := rec.Body.String()
		if !strings.Contains(out, `"type":"tool_use"`) || !strings.Contains(out, `"name":"Write"`) {
			t.Fatalf("expected Write tool_use in SSE, got: %s", out)
		}
		if !strings.Contains(out, `alpha beta`) {
			t.Fatalf("expected write payload in SSE, got: %s", out)
		}
	}
}

func TestHandleMessages_Puter_DirectSSE_NonStreamRepeatWriteFollowupReturnsFallback(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &mockUpstream{events: []upstream.SSEMessage{
		{Type: "message_start", Event: map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":    "msg_repeat_write",
				"type":  "message",
				"role":  "assistant",
				"model": "claude-opus-4-5",
			},
		}},
		{Type: "content_block_start", Event: map[string]any{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    "tool_write_repeat",
				"name":  "Write",
				"input": map[string]any{},
			},
		}},
		{Type: "content_block_delta", Event: map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": `{"file_path":"note.txt","content":"alpha beta"}`,
			},
		}},
		{Type: "content_block_stop", Event: map[string]any{
			"type":  "content_block_stop",
			"index": 0,
		}},
		{Type: "message_delta", Event: map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason": "tool_use",
			},
			"usage": map[string]any{
				"output_tokens": 5,
			},
		}},
		{Type: "message_stop", Event: map[string]any{
			"type": "message_stop",
		}},
	}}

	body, _ := json.Marshal(map[string]any{
		"model": "claude-opus-4-5",
		"messages": []map[string]any{
			{"role": "user", "content": "Use the Write tool to create note.txt with alpha beta"},
			{"role": "assistant", "content": []map[string]any{
				{
					"type":  "tool_use",
					"id":    "tool_write_1",
					"name":  "Write",
					"input": map[string]any{"file_path": "note.txt", "content": "alpha beta"},
				},
			}},
			{"role": "user", "content": []map[string]any{
				{
					"type":        "tool_result",
					"tool_use_id": "tool_write_1",
					"content":     "Done",
				},
			}},
		},
		"system": []any{},
		"stream": false,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/puter/v1/messages", bytes.NewReader(body))
	h.HandleMessages(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if strings.Contains(out, `"content":null`) {
		t.Fatalf("expected duplicate-write fallback, got null content: %s", out)
	}
	if !strings.Contains(out, duplicateToolResultFallbackText) {
		t.Fatalf("expected duplicate-write fallback text, got: %s", out)
	}
	if !strings.Contains(out, `"stop_reason":"end_turn"`) {
		t.Fatalf("expected stop_reason end_turn after duplicate write suppression, got: %s", out)
	}
}

func TestHandleMessages_SuggestionMode_LocalResponse(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &panicUpstream{}

	mkBody := func(stream bool) []byte {
		payload := map[string]any{
			"model": "claude-3-5-sonnet",
			"messages": []map[string]any{
				{"role": "user", "content": "继续处理这个问题"},
				{"role": "assistant", "content": "已经定位完了。如果你要，我下一步可以直接帮你提交修复。"},
				{"role": "user", "content": "[SUGGESTION MODE: Suggest what the user might naturally type next into Claude Code.]"},
			},
			"system": []any{},
			"stream": stream,
		}
		b, _ := json.Marshal(payload)
		return b
	}

	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://x/orchids/v1/messages", bytes.NewReader(mkBody(false)))
		h.HandleMessages(rec, req)
		if rec.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "\"type\":\"message\"") {
			t.Fatalf("expected message json, got: %s", rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "可以") {
			t.Fatalf("expected local suggestion in response, got: %s", rec.Body.String())
		}
	}

	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://x/orchids/v1/messages", bytes.NewReader(mkBody(true)))
		h.HandleMessages(rec, req)
		if rec.Code != 200 {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		out := rec.Body.String()
		if !strings.Contains(out, "event: message_start") || !strings.Contains(out, "event: message_stop") {
			t.Fatalf("expected sse message start/stop, got: %s", out)
		}
		if !strings.Contains(out, "可以") {
			t.Fatalf("expected local suggestion in sse output, got: %s", out)
		}
	}
}

func TestHandleMessages_TitleGeneration_LocalResponse(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, ContextMaxTokens: 1024, ContextSummaryMaxTokens: 256, ContextKeepTurns: 2}
	h := NewWithLoadBalancer(cfg, nil)
	h.client = &panicUpstream{}

	payload := map[string]any{
		"model": "claude-haiku-4-5-20251001",
		"messages": []map[string]any{
			{"role": "user", "content": "添加科学计数法"},
		},
		"system": []map[string]any{
			{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
			{
				"type": "text",
				"text": "Generate a concise, sentence-case title (3-7 words) that captures the main topic or goal of this coding session.\n\nReturn JSON with a single \"title\" field.",
			},
		},
		"tools":  []any{},
		"stream": true,
	}
	body, _ := json.Marshal(payload)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/bolt/v1/messages", bytes.NewReader(body))
	h.HandleMessages(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	out := rec.Body.String()
	if !strings.Contains(out, "event: message_start") || !strings.Contains(out, "event: message_stop") {
		t.Fatalf("expected local SSE message start/stop, got: %s", out)
	}
	if !strings.Contains(out, "\"text\":\"{\\\"title\\\":\\\"添加科学计数法\\\"}\"") {
		t.Fatalf("expected generated title JSON in local response, got: %s", out)
	}
}
