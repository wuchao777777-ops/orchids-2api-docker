package handler

import (
	"bytes"
	"github.com/goccy/go-json"
	"net/http"
	"strings"
	"testing"
	"time"

	"orchids-api/internal/adapter"
	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/upstream"
)

type flushRecorder struct {
	header http.Header
	buf    bytes.Buffer
	code   int
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{header: make(http.Header), code: 200}
}

func (r *flushRecorder) Header() http.Header         { return r.header }
func (r *flushRecorder) Write(b []byte) (int, error) { return r.buf.Write(b) }
func (r *flushRecorder) WriteHeader(statusCode int)  { r.code = statusCode }
func (r *flushRecorder) Flush()                      {}

func TestSanitizeToolInput_FieldMapping(t *testing.T) {
	in := `{"path":"a.txt","content":"hi","overwrite":true}`
	out := sanitizeToolInput("write", in)
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("expected json out: %v", err)
	}
	if _, ok := m["overwrite"]; ok {
		t.Fatalf("expected overwrite removed")
	}
	if m["file_path"] != "a.txt" {
		t.Fatalf("expected file_path mapped, got %v", m["file_path"])
	}
	if _, ok := m["path"]; ok {
		t.Fatalf("expected path removed")
	}
}

func TestHasRequiredToolInput_Validations(t *testing.T) {
	if hasRequiredToolInput("write", `{}`) {
		t.Fatalf("write should require path+content")
	}
	if !hasRequiredToolInput("write", `{"file_path":"a","content":"x"}`) {
		t.Fatalf("write with file_path+content should be valid")
	}
	if !hasRequiredToolInput("write", `{"path":"a","content":"x"}`) {
		t.Fatalf("write with legacy path should be valid")
	}
	if hasRequiredToolInput("bash", `{"cmd":""}`) {
		t.Fatalf("bash should require non-empty cmd/command")
	}
}

func TestSideEffectToolDedupKey(t *testing.T) {
	if got := sideEffectToolDedupKey("bash", `{"command":"echo 1"}`); got != "bash:echo 1" {
		t.Fatalf("unexpected key: %q", got)
	}
	if got := sideEffectToolDedupKey("write", `{"file_path":"a","content":"x"}`); !strings.HasPrefix(got, "write:a\x00") {
		t.Fatalf("unexpected key: %q", got)
	}
	if got := sideEffectToolDedupKey("read", `{"file_path":"a"}`); got != "" {
		t.Fatalf("read should not be treated as side effect")
	}
}

func TestNormalizeIntroKey(t *testing.T) {
	if got := normalizeIntroKey("  Hello! How can I help you today? "); got != "intro:en:greet" {
		t.Fatalf("unexpected: %q", got)
	}
	if got := normalizeIntroKey("你好，我能帮你什么"); got != "intro:zh:greet" {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestStreamHandler_TextFlow_AnthropicSSE(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false}
	rec := newFlushRecorder()
	logger := debug.New(false, false)
	defer logger.Close()
	sh := newStreamHandler(cfg, rec, logger, false, true, adapter.FormatAnthropic, "")
	defer sh.release()

	// seed a message_start so the stream resembles real output
	sh.writeSSE("message_start", `{"type":"message_start"}`)

	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "text-start"}})
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "text-delta", "delta": "hi"}})
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "text-end"}})
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "finish", "finishReason": "stop"}})

	out := rec.buf.String()
	if !strings.Contains(out, "event: content_block_start") {
		t.Fatalf("expected content_block_start in output, got: %s", out)
	}
	if !strings.Contains(out, "\"text\":\"hi\"") {
		t.Fatalf("expected text delta, got: %s", out)
	}
	if !strings.Contains(out, "event: message_stop") {
		t.Fatalf("expected message_stop, got: %s", out)
	}
}

func TestStreamHandler_ToolInput_EndEmitsToolUse(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false}
	rec := newFlushRecorder()
	logger := debug.New(false, false)
	defer logger.Close()
	sh := newStreamHandler(cfg, rec, logger, false, true, adapter.FormatAnthropic, "")
	defer sh.release()

	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "tool-input-start", "id": "t1", "toolName": "bash"}})
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "tool-input-delta", "id": "t1", "delta": `{"command":"echo 1"}`}})
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "tool-input-end", "id": "t1"}})

	out := rec.buf.String()
	if !strings.Contains(out, "\"type\":\"tool_use\"") {
		t.Fatalf("expected tool_use emitted, got: %s", out)
	}
	if !strings.Contains(out, "echo 1") {
		t.Fatalf("expected command in tool input, got: %s", out)
	}
}

func TestStreamHandler_OpenAI_SendsDONEOnStop(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false}
	rec := newFlushRecorder()
	logger := debug.New(false, false)
	defer logger.Close()
	sh := newStreamHandler(cfg, rec, logger, false, true, adapter.FormatOpenAI, "")
	defer sh.release()

	sh.finishResponse("end_turn")
	out := rec.buf.String()
	if !strings.Contains(out, "[DONE]") {
		t.Fatalf("expected [DONE] for openai SSE, got: %s", out)
	}
}

func TestMaskDedupKey_Stable(t *testing.T) {
	cfg := &config.Config{}
	rec := newFlushRecorder()
	logger := debug.New(false, false)
	defer logger.Close()
	sh := newStreamHandler(cfg, rec, logger, false, false, adapter.FormatAnthropic, "")
	defer sh.release()

	a := sh.maskDedupKey("bash:echo 1")
	b := sh.maskDedupKey("bash:echo 1")
	if a != b {
		t.Fatalf("expected stable mask")
	}
	if !strings.HasPrefix(a, "bash#") {
		t.Fatalf("expected prefix bash#, got %q", a)
	}
}

func (h *streamHandler) maskDedupKey(key string) string { return maskDedupKey(key) }

func TestExtractThinkingSignature(t *testing.T) {
	e := map[string]any{"signature": "sig"}
	if got := extractThinkingSignature(e); got != "sig" {
		t.Fatalf("unexpected: %q", got)
	}
	e2 := map[string]any{"data": map[string]any{"signature": "sig2"}}
	if got := extractThinkingSignature(e2); got != "sig2" {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestStreamHandler_TokensUsed_OverridesEstimation(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false, OutputTokenMode: "final"}
	rec := newFlushRecorder()
	logger := debug.New(false, false)
	defer logger.Close()
	sh := newStreamHandler(cfg, rec, logger, false, false, adapter.FormatAnthropic, "")
	defer sh.release()

	sh.setUsageTokens(10, -1)
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "tokens-used", "inputTokens": float64(12), "outputTokens": float64(34)}})

	// finishing should keep upstream usage (useUpstreamUsage=true)
	sh.finishResponse("end_turn")
	if sh.inputTokens != 12 || sh.outputTokens != 34 {
		t.Fatalf("unexpected usage: in=%d out=%d", sh.inputTokens, sh.outputTokens)
	}
}

func TestStreamHandler_KeepAlive_NoPanic(t *testing.T) {
	cfg := &config.Config{DebugEnabled: false}
	rec := newFlushRecorder()
	logger := debug.New(false, false)
	defer logger.Close()
	sh := newStreamHandler(cfg, rec, logger, false, true, adapter.FormatAnthropic, "")
	defer sh.release()

	// should not write once hasReturn set
	sh.hasReturn = true
	sh.writeKeepAlive()
	if rec.buf.Len() != 0 {
		t.Fatalf("expected no output when hasReturn")
	}

	// reset and ensure it writes
	sh.hasReturn = false
	sh.writeKeepAlive()
	if !strings.Contains(rec.buf.String(), ": keep-alive") {
		t.Fatalf("expected keep-alive comment")
	}
}

func TestStreamHandler_EventThrottle_fs_operation(t *testing.T) {
	cfg := &config.Config{DebugEnabled: true}
	rec := newFlushRecorder()
	logger := debug.New(false, false)
	defer logger.Close()
	sh := newStreamHandler(cfg, rec, logger, false, true, adapter.FormatAnthropic, "")
	defer sh.release()

	sh.handleMessage(upstream.SSEMessage{Type: "fs_operation", Event: map[string]any{"operation": "scan"}})
	first := rec.buf.Len()
	sh.handleMessage(upstream.SSEMessage{Type: "fs_operation", Event: map[string]any{"operation": "scan"}})
	second := rec.buf.Len()
	if second != first {
		t.Fatalf("expected throttling to suppress second fs_operation within 1s")
	}
	// allow after 1s
	sh.lastScanTime = time.Now().Add(-2 * time.Second)
	sh.handleMessage(upstream.SSEMessage{Type: "fs_operation", Event: map[string]any{"operation": "scan"}})
	if rec.buf.Len() == second {
		t.Fatalf("expected third fs_operation to be written after throttle window")
	}
}
