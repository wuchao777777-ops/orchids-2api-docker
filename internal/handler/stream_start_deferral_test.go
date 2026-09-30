package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/adapter"
	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/upstream"
)

// sharedRefusalUpstream always fails the way a queue-throttled upstream does, and
// counts the attempts so a test can tell a single probe from a spent budget.
type sharedRefusalUpstream struct {
	calls int
}

func (m *sharedRefusalUpstream) SendRequestWithPayload(_ context.Context, req upstream.UpstreamRequest, _ func(upstream.SSEMessage), _ *debug.Logger) error {
	m.calls++
	return errors.New(`qoder upstream rejected the credential: {"code":"10605","message":"{\"isQueued\":true,\"serviceAvailable\":false,\"retryAfterSeconds\":30}"}`)
}

// recoveringRefusalUpstream succeeds after its queue probe so the retry path
// must return an answer without requiring another client-side request.
type recoveringRefusalUpstream struct {
	calls int
}

func (m *recoveringRefusalUpstream) SendRequestWithPayload(_ context.Context, _ upstream.UpstreamRequest, onMessage func(upstream.SSEMessage), _ *debug.Logger) error {
	m.calls++
	if m.calls <= 2 {
		return errors.New(`qoder upstream rejected the credential: {"code":"10605","message":"{\"isQueued\":true,\"serviceAvailable\":false,\"retryAfterSeconds\":30}"}`)
	}
	onMessage(upstream.SSEMessage{Type: "model.text-delta", Event: map[string]any{"delta": "ready"}})
	onMessage(upstream.SSEMessage{Type: "model.finish", Event: map[string]any{"finishReason": "end_turn"}})
	return nil
}

func TestSharedRefusalRecoversInsideSingleRequest(t *testing.T) {
	cfg := &config.Config{RequestTimeout: 10, MaxRetries: 3, RetryDelay: 1}
	h := NewWithLoadBalancer(cfg, nil)
	stub := &recoveringRefusalUpstream{}
	h.client = stub
	payload := map[string]any{"model": "qwen3.8-flash", "messages": []map[string]any{{"role": "user", "content": "hi"}}, "stream": true}
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/qoder/v1/chat/completions", bytes.NewReader(body))
	h.HandleMessages(rec, req)
	if stub.calls != 3 || rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ready") {
		t.Fatalf("calls=%d status=%d body=%s", stub.calls, rec.Code, rec.Body.String())
	}
}

// TestStreamOpensOnlyWhenThereIsSomethingToSend pins the deferral: a streaming
// response must not commit its status before the upstream has produced anything,
// or a failure can no longer be answered with a status.
func TestStreamOpensOnlyWhenThereIsSomethingToSend(t *testing.T) {
	rec := newFlushRecorder()
	sh := newStreamHandler(&config.Config{}, rec, debug.New(false, false), true, true, adapter.FormatAnthropic)
	defer sh.release()
	sh.pendingModel = "qwen3.8-flash"

	if got := rec.buf.String(); got != "" {
		t.Fatalf("nothing had been produced, yet the client already received: %q", got)
	}
	if sh.hasCommitted() {
		t.Fatal("hasCommitted() = true before any output")
	}

	sh.handleMessage(upstream.SSEMessage{
		Type:  "model.text-delta",
		Event: map[string]any{"delta": "hello"},
	})

	out := rec.buf.String()
	startAt := strings.Index(out, "event: message_start")
	textAt := strings.Index(out, `"text":"hello"`)
	if startAt < 0 || textAt < 0 {
		t.Fatalf("expected an opening frame followed by the text, got: %s", out)
	}
	if startAt > textAt {
		t.Fatalf("content arrived before the opening frame: %s", out)
	}
	if strings.Count(out, "event: message_start") != 1 {
		t.Fatalf("expected exactly one opening frame, got: %s", out)
	}
	if !sh.hasCommitted() {
		t.Fatal("hasCommitted() = false after the opening frame was written")
	}
}

// TestKeepAliveDoesNotCommitSilentStream keeps the HTTP status available for
// a queue refusal even after the watchdog's first 15-second tick.
func TestKeepAliveDoesNotCommitSilentStream(t *testing.T) {
	rec := newFlushRecorder()
	sh := newStreamHandler(&config.Config{}, rec, debug.New(false, false), true, true, adapter.FormatAnthropic)
	defer sh.release()
	sh.pendingModel = "qwen3.8-flash"

	sh.writeKeepAlive()
	if rec.buf.Len() != 0 || sh.hasCommitted() {
		t.Fatalf("keep-alive prematurely opened response: %q", rec.buf.String())
	}
	sh.reportRequestFailure("queue refused", "rate_limit", "Qoder model queue unavailable", 0)
	if !strings.Contains(rec.buf.String(), "Qoder model queue unavailable") || strings.Contains(rec.buf.String(), "event: message_start") {
		t.Fatalf("queue failure was not returned as HTTP error: %q", rec.buf.String())
	}
}

// TestKeepAliveContinuesAfterStreamOpens covers the usual keep-alive behavior
// after an actual content block has committed the response.
func TestKeepAliveContinuesAfterStreamOpens(t *testing.T) {
	rec := newFlushRecorder()
	sh := newStreamHandler(&config.Config{}, rec, debug.New(false, false), true, true, adapter.FormatAnthropic)
	defer sh.release()
	sh.pendingModel = "qwen3.8-flash"
	sh.handleMessage(upstream.SSEMessage{Type: "model.text-delta", Event: map[string]any{"delta": "hello"}})
	sh.writeKeepAlive()
	if !strings.Contains(rec.buf.String(), sseKeepAlive) {
		t.Fatalf("committed response has no keep-alive: %q", rec.buf.String())
	}
}

// TestTerminalOnlyResponseStillOpensTheStream covers an answer that produced no
// content: the opening frame must still precede the terminal events, or the
// client receives a stop for a message that never started.
func TestTerminalOnlyResponseStillOpensTheStream(t *testing.T) {
	rec := newFlushRecorder()
	sh := newStreamHandler(&config.Config{}, rec, debug.New(false, false), true, true, adapter.FormatAnthropic)
	defer sh.release()
	sh.pendingModel = "qwen3.8-flash"

	sh.finishResponse("end_turn")

	out := rec.buf.String()
	startAt := strings.Index(out, "event: message_start")
	deltaAt := strings.Index(out, "event: message_delta")
	stopAt := strings.Index(out, "event: message_stop")
	if startAt < 0 || deltaAt < 0 || stopAt < 0 {
		t.Fatalf("expected opening and terminal frames, got: %s", out)
	}
	if !(startAt < deltaAt && deltaAt < stopAt) {
		t.Fatalf("frames out of order: %s", out)
	}
}

// TestSharedRefusalBeforeOutputUsesRetryWindow confirms that the server keeps
// the same request open through its bounded retry window. The upstream queued
// the request and named a window, so the answer is 429 — the retryable capacity
// status an OpenAI-compatible client already backs off from — with the caller's
// retry hint, and never a 503 claiming the upstream service is down while the
// same upstream answers most requests. Nothing is opened as an SSE stream.
func TestSharedRefusalBeforeOutputUsesRetryWindow(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, RequestTimeout: 10, MaxRetries: 3, RetryDelay: 1}
	h := NewWithLoadBalancer(cfg, nil)
	stub := &sharedRefusalUpstream{}
	h.client = stub

	payload := map[string]any{
		"model":    "qwen3.8-flash",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	}
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/qoder/v1/chat/completions", bytes.NewReader(body))

	h.HandleMessages(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusTooManyRequests, rec.Body.String())
	}
	out := rec.Body.String()
	if strings.Contains(out, "event: error") || strings.Contains(out, "data:") {
		t.Fatalf("an uncommitted stream must not answer with SSE frames: %s", out)
	}
	if !strings.Contains(out, "upstream_queue") {
		t.Fatalf("body = %s, want the upstream_queue answer", out)
	}
	// Initial request plus the three configured retry probes.
	if stub.calls != 4 {
		t.Fatalf("upstream attempts = %d, want 4 (initial request plus retry budget)", stub.calls)
	}
}

// TestSharedRefusalWaitBudgetIsBounded pins the ceiling on the shared-refusal
// retry wait. Production clients gave up at ~125s, so the bound has to stop
// short of that while still covering the windows an upstream can hand out.
//
// It also pins the accounting rule that used to make the documented coverage
// unreachable: the budget is charged the upstream's own hint, not hint plus
// jitter. Charging the jitter made a 30s hint cost up to 35s, so the second
// 30s window never fit inside 60s and a request that met the gate twice was
// answered 503 after ~37s — exactly the latency production showed.
func TestSharedRefusalWaitBudgetIsBounded(t *testing.T) {
	if sharedRefusalTotalWaitBudget != 90*time.Second {
		t.Fatalf("budget = %v, want 90s", sharedRefusalTotalWaitBudget)
	}
	for _, tc := range []struct {
		name    string
		already time.Duration
		next    time.Duration
		want    bool
	}{
		{"first window fits", 0, 30 * time.Second, true},
		{"second window fits exactly", 30 * time.Second, 30 * time.Second, true},
		{"third window fits exactly", 60 * time.Second, 30 * time.Second, true},
		{"fourth window does not", 90 * time.Second, 30 * time.Second, false},
		{"a window that would overrun is refused", 70 * time.Second, 30 * time.Second, false},
		{"nothing to wait for is not a wait", 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sharedRefusalWaitAllowed(tc.already, tc.next); got != tc.want {
				t.Fatalf("sharedRefusalWaitAllowed(%v, %v) = %v, want %v", tc.already, tc.next, got, tc.want)
			}
		})
	}

	// The configured form wins, and an operator raising it must actually get the
	// longer window rather than being clamped back to the constant.
	if got := SharedRefusalWaitBudget(0); got != sharedRefusalTotalWaitBudget {
		t.Fatalf("unset budget = %v, want the built-in default", got)
	}
	if got := SharedRefusalWaitBudget(150000); got != 150*time.Second {
		t.Fatalf("configured budget = %v, want 150s", got)
	}
	if !sharedRefusalWaitAllowedWithin(90*time.Second, 30*time.Second, SharedRefusalWaitBudget(150000)) {
		t.Fatal("a raised budget must admit the window the default refuses")
	}
}
