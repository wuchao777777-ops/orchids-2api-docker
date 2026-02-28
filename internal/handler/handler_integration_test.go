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
	events []upstream.SSEMessage
}

func (m *mockUpstream) SendRequest(ctx context.Context, prompt string, chatHistory []interface{}, model string, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	for _, e := range m.events {
		onMessage(e)
	}
	return nil
}

func (m *mockUpstream) SendRequestWithPayload(ctx context.Context, req upstream.UpstreamRequest, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	for _, e := range m.events {
		onMessage(e)
	}
	return nil
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
