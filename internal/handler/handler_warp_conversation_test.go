package handler

import (
	"bytes"
	"context"
	"github.com/goccy/go-json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"orchids-api/internal/audit"
	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/prompt"
	"orchids-api/internal/upstream"
)

type fakePayloadClient struct {
	mu                  sync.Mutex
	calls               []upstream.UpstreamRequest
	conversationIDsByOp []string
}

func (f *fakePayloadClient) SendRequest(ctx context.Context, prompt string, chatHistory []interface{}, model string, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	return nil
}

func (f *fakePayloadClient) SendRequestWithPayload(ctx context.Context, req upstream.UpstreamRequest, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	idx := len(f.calls) - 1
	var convID string
	if idx >= 0 && idx < len(f.conversationIDsByOp) {
		convID = f.conversationIDsByOp[idx]
	}
	f.mu.Unlock()

	if convID != "" {
		onMessage(upstream.SSEMessage{
			Type:  "model.conversation_id",
			Event: map[string]interface{}{"id": convID},
		})
	}
	onMessage(upstream.SSEMessage{
		Type:  "model.finish",
		Event: map[string]interface{}{"finishReason": "end_turn"},
	})
	return nil
}

func (f *fakePayloadClient) snapshotCalls() []upstream.UpstreamRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]upstream.UpstreamRequest, len(f.calls))
	copy(out, f.calls)
	return out
}

func makeWarpRequestBody(t *testing.T, text, conversationID string) []byte {
	t.Helper()
	req := ClaudeRequest{
		Model:          "claude-opus-4-6",
		ConversationID: conversationID,
		Messages: []prompt.Message{
			{
				Role:    "user",
				Content: prompt.MessageContent{Text: text},
			},
		},
		Stream: false,
		Tools:  []interface{}{},
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return body
}

func newTestHandler(client UpstreamClient) *Handler {
	return &Handler{
		config:       &config.Config{DebugEnabled: false},
		client:       client,
		sessionStore: NewMemorySessionStore(30*time.Minute, 1024),
		dedupStore:   NewMemoryDedupStore(duplicateWindow, duplicateCleanupWindow),
		auditLogger:  audit.NewNopLogger(),
	}
}

func TestWarpConversationID_NotPersistedWithoutConversationKey(t *testing.T) {
	t.Parallel()

	client := &fakePayloadClient{
		conversationIDsByOp: []string{"warp_upstream_conv_1", "warp_upstream_conv_2"},
	}
	h := newTestHandler(client)

	req1 := httptest.NewRequest(http.MethodPost, "/warp/v1/messages", bytes.NewReader(makeWarpRequestBody(t, "first", "")))
	rec1 := httptest.NewRecorder()
	h.HandleMessages(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", rec1.Code, http.StatusOK)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/warp/v1/messages", bytes.NewReader(makeWarpRequestBody(t, "second", "")))
	rec2 := httptest.NewRecorder()
	h.HandleMessages(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second request status = %d, want %d", rec2.Code, http.StatusOK)
	}

	calls := client.snapshotCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 upstream calls, got %d", len(calls))
	}

	if !strings.HasPrefix(calls[0].ChatSessionID, "chat_") {
		t.Fatalf("first ChatSessionID = %q, expected chat_*", calls[0].ChatSessionID)
	}
	if !strings.HasPrefix(calls[1].ChatSessionID, "chat_") {
		t.Fatalf("second ChatSessionID = %q, expected chat_*", calls[1].ChatSessionID)
	}
	if calls[1].ChatSessionID == "warp_upstream_conv_1" {
		t.Fatalf("second request unexpectedly reused upstream conversation id: %q", calls[1].ChatSessionID)
	}
	// Verify empty conversation key does not store convID
	if _, ok := h.sessionStore.GetConvID(context.Background(), ""); ok {
		t.Fatalf("unexpected cached conversation id for empty conversation key")
	}
}

func TestWarpConversationID_PersistedWithConversationKey(t *testing.T) {
	t.Parallel()

	client := &fakePayloadClient{
		conversationIDsByOp: []string{"warp_upstream_conv_persist"},
	}
	h := newTestHandler(client)

	const conversationID = "local_conversation_key_1"

	req1 := httptest.NewRequest(http.MethodPost, "/warp/v1/messages", bytes.NewReader(makeWarpRequestBody(t, "first", conversationID)))
	rec1 := httptest.NewRecorder()
	h.HandleMessages(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", rec1.Code, http.StatusOK)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/warp/v1/messages", bytes.NewReader(makeWarpRequestBody(t, "second", conversationID)))
	rec2 := httptest.NewRecorder()
	h.HandleMessages(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second request status = %d, want %d", rec2.Code, http.StatusOK)
	}

	calls := client.snapshotCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 upstream calls, got %d", len(calls))
	}

	if !strings.HasPrefix(calls[0].ChatSessionID, "chat_") {
		t.Fatalf("first ChatSessionID = %q, expected chat_*", calls[0].ChatSessionID)
	}
	if calls[1].ChatSessionID != "warp_upstream_conv_persist" {
		t.Fatalf("second ChatSessionID = %q, want %q", calls[1].ChatSessionID, "warp_upstream_conv_persist")
	}
}

func TestWarpPassthrough_DoesNotTrimMessagesOrSanitizeSystem(t *testing.T) {
	t.Parallel()

	client := &fakePayloadClient{}
	h := &Handler{
		config: &config.Config{
			DebugEnabled:            false,
			WarpMaxHistoryMessages:  1,
			WarpMaxToolResults:      1,
			OrchidsCCEntrypointMode: "strip",
		},
		client:       client,
		sessionStore: NewMemorySessionStore(30*time.Minute, 1024),
		dedupStore:   NewMemoryDedupStore(duplicateWindow, duplicateCleanupWindow),
		auditLogger:  audit.NewNopLogger(),
	}

	reqPayload := ClaudeRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "m1"}},
			{Role: "assistant", Content: prompt.MessageContent{Text: "m2"}},
			{Role: "user", Content: prompt.MessageContent{Text: "m3"}},
		},
		System: []prompt.SystemItem{
			{Type: "text", Text: "You are Claude Code, Anthropic's official CLI for Claude."},
			{Type: "text", Text: "cc_entrypoint=claude-code; keep=this"},
		},
		Stream: false,
		Tools:  []interface{}{},
	}

	body, err := json.Marshal(reqPayload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/warp/v1/messages", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.HandleMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("request status = %d, want %d", rec.Code, http.StatusOK)
	}

	calls := client.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(calls))
	}

	if len(calls[0].Messages) != len(reqPayload.Messages) {
		t.Fatalf("messages len = %d, want %d", len(calls[0].Messages), len(reqPayload.Messages))
	}
	if len(calls[0].System) != len(reqPayload.System) {
		t.Fatalf("system len = %d, want %d", len(calls[0].System), len(reqPayload.System))
	}
	if !strings.Contains(calls[0].System[0].Text, "Claude Code") {
		t.Fatalf("expected warp system prompt to be unchanged, got %q", calls[0].System[0].Text)
	}
	if !strings.Contains(calls[0].System[1].Text, "cc_entrypoint=claude-code") {
		t.Fatalf("expected cc_entrypoint to be preserved for warp, got %q", calls[0].System[1].Text)
	}
}
