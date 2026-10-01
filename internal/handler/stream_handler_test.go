package handler

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"encoding/json"

	"orchids-api/internal/adapter"
	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/testutil"
	"orchids-api/internal/tiktoken"
	"orchids-api/internal/upstream"
)

type flushRecorder struct {
	header  http.Header
	buf     bytes.Buffer
	code    int
	flushes int
}

type failingResponseWriter struct {
	header http.Header
	err    error
}

func (w *failingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *failingResponseWriter) Write([]byte) (int, error) { return 0, w.err }
func (w *failingResponseWriter) WriteHeader(int)           {}
func (w *failingResponseWriter) Flush()                    {}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{header: make(http.Header), code: 200}
}

func (r *flushRecorder) Header() http.Header         { return r.header }
func (r *flushRecorder) Write(b []byte) (int, error) { return r.buf.Write(b) }
func (r *flushRecorder) WriteHeader(statusCode int)  { r.code = statusCode }
func (r *flushRecorder) Flush()                      { r.flushes++ }

func TestMarshalSSEPayloads_ManualJSONEscapes(t *testing.T) {
	newline := string(byte('\n'))
	expectedText := "he" + "\"" + "llo" + newline + "next"
	raw, err := marshalSSEContentBlockDeltaTextBytes(7, expectedText)
	if err != nil {
		t.Fatalf("marshal text delta: %v", err)
	}
	var delta map[string]any
	testutil.NoError(t, json.Unmarshal(raw, &delta), "unmarshal text delta: %v")
	testutil.Equal(t, int(delta["index"].(float64)), 7)
	deltaObj := delta["delta"].(map[string]any)
	if deltaObj["type"] != "text_delta" || deltaObj["text"] != expectedText {
		t.Fatalf("unexpected delta payload: %#v", deltaObj)
	}

	expectedToolID := `tool_"1`
	expectedToolName := "Wr" + newline + "ite"
	raw, err = appendSSEContentBlockStartToolUse(nil, 3, expectedToolID, expectedToolName)
	if err != nil {
		t.Fatalf("marshal tool start: %v", err)
	}
	var startPayload map[string]any
	testutil.NoError(t, json.Unmarshal(raw, &startPayload), "unmarshal tool start: %v")
	contentBlock := startPayload["content_block"].(map[string]any)
	if contentBlock["id"] != expectedToolID || contentBlock["name"] != expectedToolName {
		t.Fatalf("unexpected tool payload: %#v", contentBlock)
	}

	expectedSignature := "sig\"" + newline + "next"
	rawBytes, err := appendSSEContentBlockStartThinking(nil, 4, expectedSignature)
	if err != nil {
		t.Fatalf("marshal thinking start: %v", err)
	}
	var thinkingStartPayload map[string]any
	testutil.NoError(t, json.Unmarshal(rawBytes, &thinkingStartPayload), "unmarshal thinking start: %v")
	thinkingBlock := thinkingStartPayload["content_block"].(map[string]any)
	if thinkingBlock["type"] != "thinking" || thinkingBlock["signature"] != expectedSignature {
		t.Fatalf("unexpected thinking payload: %#v", thinkingBlock)
	}

	expectedPartialJSON := "{\"path\":\"a.txt\",\"content\":\"he\\\"llo" + newline + "next\"}"
	rawBytes, err = appendSSEContentBlockDeltaInputJSON(nil, 5, expectedPartialJSON)
	if err != nil {
		t.Fatalf("marshal input_json delta: %v", err)
	}
	var inputJSONPayload map[string]any
	testutil.NoError(t, json.Unmarshal(rawBytes, &inputJSONPayload), "unmarshal input_json delta: %v")
	inputDelta := inputJSONPayload["delta"].(map[string]any)
	if inputDelta["type"] != "input_json_delta" || inputDelta["partial_json"] != expectedPartialJSON {
		t.Fatalf("unexpected input_json payload: %#v", inputDelta)
	}

	expectedStopReason := "tool_\"use" + newline + "next"
	rawBytes, err = marshalSSEMessageDeltaBytes(expectedStopReason, 42)
	if err != nil {
		t.Fatalf("marshal message delta: %v", err)
	}
	var msg map[string]any
	testutil.NoError(t, json.Unmarshal(rawBytes, &msg), "unmarshal message delta: %v")
	testutil.Equal(t, msg["type"], "message_delta")
	testutil.EqualAny(t, msg["delta"].(map[string]any)["stop_reason"], expectedStopReason)
	testutil.Equal(t, int(msg["usage"].(map[string]any)["output_tokens"].(float64)), 42)

	msgStartRaw, err := marshalSSEMessageStartBytes("msg_123", "claude-test", 12, 0)
	if err != nil {
		t.Fatalf("marshal message start: %v", err)
	}
	var msgStart map[string]any
	testutil.NoError(t, json.Unmarshal(msgStartRaw, &msgStart), "unmarshal message start: %v")
	testutil.Equal(t, msgStart["type"], "message_start")
	messageObj := msgStart["message"].(map[string]any)
	if messageObj["id"] != "msg_123" || messageObj["model"] != "claude-test" {
		t.Fatalf("unexpected message object: %#v", messageObj)
	}
	usageObj := messageObj["usage"].(map[string]any)
	if int(usageObj["input_tokens"].(float64)) != 12 || int(usageObj["output_tokens"].(float64)) != 0 {
		t.Fatalf("unexpected usage object: %#v", usageObj)
	}

	plainText := "hello ??"
	rawBytes, err = marshalSSEContentBlockDeltaTextBytes(9, plainText)
	if err != nil {
		t.Fatalf("marshal plain text delta: %v", err)
	}
	var plainDelta map[string]any
	testutil.NoError(t, json.Unmarshal(rawBytes, &plainDelta), "unmarshal plain text delta: %v")
	testutil.EqualAny(t, plainDelta["delta"].(map[string]any)["text"], plainText)

	htmlEscaped := "<tag>&\u2028\u2029"
	rawBytes, err = marshalSSEContentBlockDeltaTextBytes(10, htmlEscaped)
	if err != nil {
		t.Fatalf("marshal html escaped delta: %v", err)
	}
	if !bytes.Contains(rawBytes, []byte("\\u003c")) || !bytes.Contains(rawBytes, []byte("\\u003e")) || !bytes.Contains(rawBytes, []byte("\\u0026")) {
		t.Fatalf("expected html-sensitive bytes to be escaped, got: %s", rawBytes)
	}
	if !bytes.Contains(rawBytes, []byte("\\u2028")) || !bytes.Contains(rawBytes, []byte("\\u2029")) {
		t.Fatalf("expected line separator bytes to be escaped, got: %s", rawBytes)
	}
	var escapedDelta map[string]any
	testutil.NoError(t, json.Unmarshal(rawBytes, &escapedDelta), "unmarshal html escaped delta: %v")
	testutil.EqualAny(t, escapedDelta["delta"].(map[string]any)["text"], htmlEscaped)
}

func TestInjectNoAvailableAccountError_RateLimitAnswers429(t *testing.T) {
	rec := httptest.NewRecorder()
	sh := newStreamHandler(&config.Config{}, rec, debug.New(false, false), true, false, adapter.FormatAnthropic)

	sh.InjectNoAvailableAccountError(
		`upstream API error: status=429, body={"code":"rate-limited","message":"You have hit the rate limit."}`,
		errors.New("no enabled accounts available for channel: workbuddy (all matching accounts are rate-limited or cooling down)"),
	)

	// A non-streaming request has committed nothing yet, so the failure is a real
	// error response. It used to be a 200 whose assistant content was the error
	// text, which a client cannot tell from an answer.
	testutil.Equal(t, rec.Code, http.StatusTooManyRequests)
	body := rec.Body.String()
	testutil.MustContainAll(t, body, `"type":"error"`, "rate-limited")
	testutil.MustNotContain(t, body, "Please check account statuses in Admin UI")
	// The selector error is a diagnostic: it belongs in the log, not in a body a
	// client may show to a user.
	testutil.MustNotContain(t, body, "no enabled accounts available for channel")
}

// TestInjectNoAvailableAccountError_CreditExhaustionIsChannelNeutral pins that the
// spent-allowance message names no channel: whichever provider ran out says the
// same thing, and the upstream's own text never reaches the client.
func TestInjectNoAvailableAccountError_CreditExhaustionIsChannelNeutral(t *testing.T) {
	rec := httptest.NewRecorder()
	sh := newStreamHandler(&config.Config{}, rec, debug.New(false, false), true, false, adapter.FormatAnthropic)

	sh.InjectNoAvailableAccountError(
		`workbuddy API error: status=429, message={"error":{"data":{"code":14018,"msg":"Credits exhausted. Please visit the link below to purchase add-on packs"}}}`,
		nil,
	)

	testutil.Equal(t, rec.Code, http.StatusTooManyRequests)
	body := rec.Body.String()
	testutil.MustNotContain(t, body, "workbuddy API error")
	testutil.MustContainAll(t, body, `"type":"error"`, "exhausted its allowance")
}

// TestInjectNoAvailableAccountError_StreamingReportsInBandError is the other half
// of the contract: once the stream has actually started, its status is already
// 200 and can never be revisited.
//
// It must not pretend to be an answer either. The report is the protocol's error
// event, and the stream ends there rather than with a normal stop. A stream that
// has sent nothing yet is deliberately NOT in this case -- see
// TestStreamError_OpenAIFormatRespectsWhetherTheStreamStarted, which pins that an
// uncommitted response still answers with a real status.
func TestInjectNoAvailableAccountError_StreamingReportsInBandError(t *testing.T) {
	rec := httptest.NewRecorder()
	sh := newStreamHandler(&config.Config{}, rec, debug.New(false, false), true, true, adapter.FormatAnthropic)
	// Commit the response the way a started stream does: the opening frame is
	// written lazily now, so a stream only becomes unrevistable once it has begun.
	// Opening without content keeps this test about the in-band report rather than
	// about the answer text.
	sh.mu.Lock()
	sh.writeMessageStartLocked("workbuddy-model", 12, 0)
	sh.mu.Unlock()

	sh.InjectNoAvailableAccountError(
		`upstream API error: status=429, body={"code":"rate-limited"}`,
		errors.New("no enabled accounts available for channel: workbuddy (all matching accounts are rate-limited or cooling down)"),
	)

	testutil.Equal(t, rec.Code, http.StatusOK)
	body := rec.Body.String()
	testutil.MustContainAll(t, body, "event: error", `"type":"error"`)
	// The failure must not be dressed as assistant text.
	testutil.MustNotContain(t, body, "content_block_delta")
	testutil.MustNotContain(t, body, "no enabled accounts available for channel")
}

// TestStreamError_OpenAIFormatRespectsWhetherTheStreamStarted pins the two
// answers the streaming path owes a caller, and keeps them consistent with the
// initial-selection entrance that reports the same condition.
//
// Before any content the response is still uncommitted, so the failure is an
// HTTP status: the client sees a retryable 429 rather than a truncated stream.
// Once content has been sent the status is fixed, so the same failure has to
// terminate the stream in band.
func TestStreamError_OpenAIFormatRespectsWhetherTheStreamStarted(t *testing.T) {
	t.Run("nothing sent yet answers with a status", func(t *testing.T) {
		rec := httptest.NewRecorder()
		sh := newStreamHandler(&config.Config{}, rec, debug.New(false, false), true, true, adapter.FormatOpenAI)

		sh.InjectNoAvailableAccountError(`upstream API error: status=429`, errors.New("no enabled accounts available"))

		testutil.Equal(t, rec.Code, http.StatusTooManyRequests)
		body := rec.Body.String()
		testutil.MustContainAll(t, body, `"error":{`, "rate-limited")
		testutil.MustNotContainAny(t, body, "data:", "[DONE]")
	})

	t.Run("after content it terminates the stream in band", func(t *testing.T) {
		rec := httptest.NewRecorder()
		sh := newStreamHandler(&config.Config{}, rec, debug.New(false, false), true, true, adapter.FormatOpenAI)
		sh.handleMessage(upstream.SSEMessage{
			Type:  "model.text-delta",
			Event: map[string]any{"delta": "partial"},
		})

		sh.InjectNoAvailableAccountError(`upstream API error: status=429`, errors.New("no enabled accounts available"))

		body := rec.Body.String()
		testutil.MustContainAll(t, body, `"error":{`, "rate-limited")
		testutil.MustContain(t, body, "[DONE]")
	})
}

func TestAppendSSEPayloadBuildersMatchMarshal(t *testing.T) {
	tests := []struct {
		name     string
		marshal  func() ([]byte, error)
		appendTo func([]byte) ([]byte, error)
	}{
		{
			name:    "tool start",
			marshal: func() ([]byte, error) { return appendSSEContentBlockStartToolUse(nil, 3, `tool_"1`, "Wr\nite") },
			appendTo: func(dst []byte) ([]byte, error) {
				return appendSSEContentBlockStartToolUse(dst, 3, `tool_"1`, "Wr\nite")
			},
		},
		{
			name:    "text start",
			marshal: func() ([]byte, error) { return marshalSSEContentBlockStartTextBytes(4) },
			appendTo: func(dst []byte) ([]byte, error) {
				return appendSSEContentBlockStartText(dst, 4)
			},
		},
		{
			name:    "thinking start",
			marshal: func() ([]byte, error) { return appendSSEContentBlockStartThinking(nil, 5, "sig\n123") },
			appendTo: func(dst []byte) ([]byte, error) {
				return appendSSEContentBlockStartThinking(dst, 5, "sig\n123")
			},
		},
		{
			name: "input json delta",
			marshal: func() ([]byte, error) {
				return appendSSEContentBlockDeltaInputJSON(nil, 6, `{"path":"a.txt","content":"he\"llo"}`)
			},
			appendTo: func(dst []byte) ([]byte, error) {
				return appendSSEContentBlockDeltaInputJSON(dst, 6, `{"path":"a.txt","content":"he\"llo"}`)
			},
		},
		{
			name:    "text delta",
			marshal: func() ([]byte, error) { return marshalSSEContentBlockDeltaTextBytes(7, "hello\nworld") },
			appendTo: func(dst []byte) ([]byte, error) {
				return appendSSEContentBlockDeltaText(dst, 7, "hello\nworld")
			},
		},
		{
			name:    "thinking delta",
			marshal: func() ([]byte, error) { return appendSSEContentBlockDeltaThinking(nil, 8, "step <1>") },
			appendTo: func(dst []byte) ([]byte, error) {
				return appendSSEContentBlockDeltaThinking(dst, 8, "step <1>")
			},
		},
		{
			name:    "block stop",
			marshal: func() ([]byte, error) { return marshalSSEContentBlockStopBytes(9) },
			appendTo: func(dst []byte) ([]byte, error) {
				return appendSSEContentBlockStop(dst, 9)
			},
		},
		{
			name:    "message delta",
			marshal: func() ([]byte, error) { return marshalSSEMessageDeltaBytes("tool_use\nnext", 42) },
			appendTo: func(dst []byte) ([]byte, error) {
				return appendSSEMessageDelta(dst, "tool_use\nnext", 42)
			},
		},
	}

	buf := make([]byte, 0, 256)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, err := tt.marshal()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got, err := tt.appendTo(buf[:0])
			if err != nil {
				t.Fatalf("append: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("got=%s want=%s", got, want)
			}
			buf = got[:0]
		})
	}
}

func TestSanitizeToolInput_FieldMapping(t *testing.T) {
	in := `{"path":"a.txt","content":"hi","overwrite":true}`
	out := sanitizeToolInput("write", in)
	var m map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(out), &m), "expected json out: %v")
	if _, ok := m["overwrite"]; ok {
		t.Fatalf("expected overwrite removed")
	}
	testutil.Equal(t, m["file_path"], "a.txt")
	if _, ok := m["path"]; ok {
		t.Fatalf("expected path removed")
	}
}

func TestNormalizeUpstreamToolCall_ListDirUsesTopLevelBash(t *testing.T) {
	name, input := normalizeUpstreamToolCall("LS", `{"path":"/tmp/project"}`)
	testutil.Equal(t, name, "Bash")
	var payload map[string]string
	testutil.NoError(t, json.Unmarshal([]byte(input), &payload), "expected json input, got %v")
	testutil.Equal(t, payload["command"], `ls -1A -- "/tmp/project"`)
	testutil.Equal(t, payload["description"], "List top-level directory entries")
}

func TestNormalizeUpstreamToolCall_GlobPreservesGlob(t *testing.T) {
	name, input := normalizeUpstreamToolCall("Glob", `{"path":"/tmp/project"}`)
	testutil.Equal(t, name, "Glob")
	testutil.MustContain(t, input, `"pattern":"*"`)
}

func TestRewriteToolCallToClient_PrunesNestedUnknownTodoFields(t *testing.T) {
	h := newStreamHandler(&config.Config{}, httptest.NewRecorder(), debug.New(false, false), false, false, adapter.FormatAnthropic)
	defer h.release()
	h.setClientTools([]interface{}{map[string]interface{}{
		"name": "TodoWrite",
		"input_schema": map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"todos": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]interface{}{
							"content": map[string]interface{}{"type": "string"},
							"status":  map[string]interface{}{"type": "string"},
						},
					},
				},
			},
		},
	}})
	name, input := h.rewriteToolCallToClient("TodoWrite", `{"todos":[{"content":"one","status":"pending","id":"1"}]}`)
	if name != "TodoWrite" || strings.Contains(input, `"id"`) {
		t.Fatalf("unexpected sanitized todo call: name=%s input=%s", name, input)
	}
	var payload map[string]interface{}
	testutil.NoError(t, json.Unmarshal([]byte(input), &payload))
	todos := payload["todos"].([]interface{})
	if _, ok := todos[0].(map[string]interface{})["content"]; !ok {
		t.Fatalf("content was lost: %s", input)
	}
}

// newStreamTestHandler builds a stream handler over a flushing recorder for a
// test, and tears the logger and handler down with it.
func newStreamTestHandler(t *testing.T, streaming, toolMode bool, format adapter.ResponseFormat) (*streamHandler, *flushRecorder) {
	t.Helper()
	rec := newFlushRecorder()
	logger := debug.New(false, false)
	sh := newStreamHandler(&config.Config{DebugEnabled: false}, rec, logger, streaming, toolMode, format)
	t.Cleanup(func() {
		sh.release()
		logger.Close()
	})
	return sh, rec
}

func TestStreamHandler_TextFlow_AnthropicSSE(t *testing.T) {
	sh, rec := newStreamTestHandler(t, false, true, adapter.FormatAnthropic)

	// seed a message_start so the stream resembles real output. For
	// "message_start" the live writer deliberately skips ensureMessageStartLocked,
	// so this writes exactly the one frame the retired writer wrote.
	sh.mu.Lock()
	sh.writeSSEBytesLockedWithHint("message_start", []byte(`{"type":"message_start"}`), true)
	sh.mu.Unlock()

	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "text-start"}})
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "text-delta", "delta": "hi"}})
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "text-end"}})
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "finish", "finishReason": "stop"}})

	out := rec.buf.String()
	testutil.MustContain(t, out, "event: content_block_start")
	testutil.MustContain(t, out, "\"text\":\"hi\"")
	testutil.MustContain(t, out, "event: message_stop")
}

func TestStreamHandler_OpenAI_SendsDONEOnStop(t *testing.T) {
	sh, rec := newStreamTestHandler(t, false, true, adapter.FormatOpenAI)

	sh.finishResponse("end_turn")
	out := rec.buf.String()
	testutil.MustContain(t, out, "[DONE]")
}

func TestWriteSSEFrameBytes_Output(t *testing.T) {
	var buf bytes.Buffer
	testutil.NoError(t, writeSSEFrameBytes(&buf, "content_block_delta", []byte("{\"type\":\"content_block_delta\"}")), "writeSSEFrameBytes: %v")
	got := buf.String()
	want := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\"}\n\n"
	testutil.Equal(t, got, want)
}

func TestWriteOpenAIFrame_Output(t *testing.T) {
	var buf bytes.Buffer
	testutil.NoError(t, writeOpenAIFrame(&buf, []byte("{\"id\":\"msg_1\"}")), "writeOpenAIFrame: %v")
	got := buf.String()
	want := "data: {\"id\":\"msg_1\"}\n\n"
	testutil.Equal(t, got, want)
}

func TestExtractThinkingSignature(t *testing.T) {
	e := map[string]any{"signature": "sig"}
	testutil.Equal(t, extractThinkingSignature(e), "sig")
	e2 := map[string]any{"data": map[string]any{"signature": "sig2"}}
	testutil.Equal(t, extractThinkingSignature(e2), "sig2")
}

func TestStreamHandler_TokensUsed_OverridesEstimation(t *testing.T) {
	sh, _ := newStreamTestHandler(t, false, false, adapter.FormatAnthropic)

	sh.setUsageTokens(10, -1)
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "tokens-used", "inputTokens": float64(12), "outputTokens": float64(34)}})

	// finishing should keep upstream usage (useUpstreamUsage=true)
	sh.finishResponse("end_turn")
	if sh.inputTokens != 12 || sh.outputTokens != 34 {
		t.Fatalf("unexpected usage: in=%d out=%d", sh.inputTokens, sh.outputTokens)
	}
}

func TestStreamHandler_UsageMissingFieldsKeepLocalEstimate(t *testing.T) {
	sh := newStreamHandler(&config.Config{}, newFlushRecorder(), debug.New(false, false), false, false, adapter.FormatAnthropic)
	defer sh.release()
	sh.inputTokens, sh.outputTokens = 31, 17
	sh.applyUpstreamUsageTokens(map[string]interface{}{"output_tokens": 9, "credits": 0.5})
	if !sh.useUpstreamUsage || sh.inputTokens != 31 || sh.outputTokens != 9 || sh.usageMetadata["credits"] != 0.5 {
		t.Fatalf("partial usage overwrote estimate or lost metadata: input=%d output=%d metadata=%v", sh.inputTokens, sh.outputTokens, sh.usageMetadata)
	}
	sh.resetRoundState()
	sh.inputTokens, sh.outputTokens = 31, 17
	sh.applyUpstreamUsageTokens(map[string]interface{}{"original_credits": 0.25})
	if !sh.useUpstreamUsage || sh.inputTokens != 31 || sh.outputTokens != 17 || sh.usageMetadata["original_credits"] != 0.25 {
		t.Fatalf("credits-only usage lost evidence or estimate: input=%d output=%d metadata=%v", sh.inputTokens, sh.outputTokens, sh.usageMetadata)
	}
}

func TestStreamHandler_DetailedUsageIsAssignedIdempotently(t *testing.T) {
	sh := newStreamHandler(&config.Config{}, newFlushRecorder(), debug.New(false, false), false, false, adapter.FormatAnthropic)
	defer sh.release()
	usage := map[string]interface{}{
		"inputTokens": 1000, "outputTokens": 20, "cacheReadTokens": 900,
		"cacheWriteTokens": 50, "reasoningTokens": 7,
		"credits": 0.25, "original_credits": 0.5,
	}
	sh.handleMessage(upstream.SSEMessage{Type: "model.tokens-used", Event: usage})
	sh.handleMessage(upstream.SSEMessage{Type: "model.finish", Event: map[string]interface{}{"usage": usage}})
	if sh.inputTokens != 1000 || sh.outputTokens != 20 || sh.cachedInputTokens != 900 || sh.cacheWriteTokens != 50 || sh.reasoningTokens != 7 {
		t.Fatalf("detailed usage lost or doubled: in=%d out=%d cached=%d write=%d reasoning=%d", sh.inputTokens, sh.outputTokens, sh.cachedInputTokens, sh.cacheWriteTokens, sh.reasoningTokens)
	}
	if sh.usageMetadata["credits"] != 0.25 || sh.usageMetadata["original_credits"] != 0.5 {
		t.Fatalf("usage metadata = %#v", sh.usageMetadata)
	}
	sh.resetRoundState()
	if sh.cachedInputTokens != 0 || sh.cacheWriteTokens != 0 || sh.reasoningTokens != 0 || sh.usageMetadata != nil {
		t.Fatalf("detailed usage survived round reset: %+v", sh)
	}
}

func TestStreamHandler_FinalOutputTokens_MatchChunkedText(t *testing.T) {
	sh, _ := newStreamTestHandler(t, false, false, adapter.FormatAnthropic)

	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "text-start"}})
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "text-delta", "delta": "hel"}})
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "text-delta", "delta": "lo world!"}})
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "text-end"}})
	sh.handleMessage(upstream.SSEMessage{Type: "model", Event: map[string]any{"type": "finish", "finishReason": "stop"}})

	want := tiktoken.EstimateTextTokens("hello world!")
	testutil.Equal(t, sh.outputTokens, want)
}

func TestStreamHandler_KeepAlive_NoPanic(t *testing.T) {
	sh, rec := newStreamTestHandler(t, false, true, adapter.FormatAnthropic)

	// should not write once terminal state is set
	sh.mu.Lock()
	sh.hasReturn = true
	sh.returned.Store(true)
	sh.mu.Unlock()
	sh.writeKeepAlive()
	testutil.Equal(t, rec.buf.Len(), 0)

	// reset: a silent stream must stay uncommitted until it has content.
	sh.mu.Lock()
	sh.hasReturn = false
	sh.returned.Store(false)
	sh.mu.Unlock()
	sh.writeKeepAlive()
	testutil.Equal(t, rec.buf.Len(), 0)
	sh.handleMessage(upstream.SSEMessage{Type: "model.text-delta", Event: map[string]interface{}{"delta": "hello"}})
	sh.writeKeepAlive()
	testutil.MustContain(t, rec.buf.String(), ": keep-alive")
}

func TestStreamHandler_TerminalWriteFailureOverridesClaimedSuccess(t *testing.T) {
	writer := &failingResponseWriter{err: errors.New("client connection closed")}
	logger := debug.New(false, false)
	defer logger.Close()
	sh := newStreamHandler(&config.Config{}, writer, logger, false, true, adapter.FormatAnthropic)
	defer sh.release()

	sh.finishResponse("end_turn")

	returned, failed := sh.terminalState()
	if !returned || !failed {
		t.Fatalf("terminal state = returned:%v failed:%v, want terminal failure", returned, failed)
	}
	testutil.Equal(t, sh.finalStopReason, "write_error")
}

func TestStreamHandler_TerminalStateAndKeepAliveAreRaceSafe(t *testing.T) {
	sh, _ := newStreamTestHandler(t, false, true, adapter.FormatAnthropic)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			sh.writeKeepAlive()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_, _ = sh.terminalState()
		}
		sh.finishResponse("end_turn")
	}()
	wg.Wait()

	if returned, _ := sh.terminalState(); !returned {
		t.Fatal("handler did not reach terminal state")
	}
}

func TestStreamHandler_CoalescesNonTextFlushes(t *testing.T) {
	sh, rec := newStreamTestHandler(t, false, true, adapter.FormatAnthropic)

	// The live writers take an explicit flush hint; writeSSEBytesLocked, the live
	// content_block_stop writer, derives its hint from shouldFlushSSEImmediately,
	// so these frames go through the same writer with the same hint source.
	writeFrame := func(event string, data []byte) {
		sh.mu.Lock()
		defer sh.mu.Unlock()
		sh.writeSSEBytesLockedWithHint(event, data, shouldFlushSSEImmediately(event, data))
	}

	writeFrame("message_start", []byte(`{"type":"message_start"}`))
	testutil.Equal(t, rec.flushes, 1)

	thinkingData, err := appendSSEContentBlockDeltaThinking(nil, 0, "step")
	if err != nil {
		t.Fatalf("marshal thinking delta: %v", err)
	}
	// The seed above wrote the opening frame without marking the stream started
	// (production's writeMessageStartLocked is what sets messageStartWritten), so
	// the first delta also emits the deferred opening frame and flushes it. The
	// delta itself is deferred one slot into the threshold.
	writeFrame("content_block_delta", thinkingData)
	testutil.Equal(t, rec.flushes, 2)

	// The remaining deferred thinking frames below the threshold add no flush.
	for i := 0; i < sseDeferredFlushFrameThreshold-2; i++ {
		writeFrame("content_block_delta", thinkingData)
	}
	testutil.Equal(t, rec.flushes, 2)

	// The frame that reaches sseDeferredFlushFrameThreshold flushes the batch.
	writeFrame("content_block_delta", thinkingData)
	testutil.Equal(t, rec.flushes, 3)

	textData, err := marshalSSEContentBlockDeltaTextBytes(0, "hi")
	if err != nil {
		t.Fatalf("marshal text delta: %v", err)
	}
	writeFrame("content_block_delta", textData)
	testutil.Equal(t, rec.flushes, 4)
}

func TestStreamHandler_FinishResponse_SuppressesGenericEmptyFallbackWhenRequested(t *testing.T) {
	sh, rec := newStreamTestHandler(t, false, true, adapter.FormatAnthropic)

	sh.finishResponse("end_turn")

	out := rec.buf.String()
	testutil.MustNotContain(t, out, "No output was presented to the user")
	testutil.MustContain(t, out, "event: message_stop")
}

func TestStreamHandler_NoToolsGateSuppressesValidToolCall(t *testing.T) {
	sh, rec := newStreamTestHandler(t, false, true, adapter.FormatAnthropic)

	sh.setDisallowToolCalls(true)
	sh.handleMessage(upstream.SSEMessage{
		Type: "model",
		Event: map[string]any{
			"type":       "tool-call",
			"toolCallId": "tool_1",
			"toolName":   "Read",
			"input":      `{"file_path":"README.md"}`,
		},
	})
	sh.finishResponse("tool_use")

	out := rec.buf.String()
	testutil.MustNotContain(t, out, `"type":"tool_use"`)
	testutil.MustContain(t, out, `"stop_reason":"end_turn"`)
}

func TestStreamHandler_NoToolsWriteReturnsContentAsText(t *testing.T) {
	sh, rec := newStreamTestHandler(t, false, true, adapter.FormatAnthropic)

	sh.setAllowedToolNames(nil)
	sh.setSurfaceToolRejects(true)
	sh.setDisallowToolCalls(true)
	sh.handleMessage(upstream.SSEMessage{
		Type: "model.tool-call",
		Event: map[string]any{
			"toolCallId": "tool_write_1",
			"toolName":   "Write",
			"input":      `{"file_path":"index.html","content":"<!doctype html><h1>Ready</h1>"}`,
		},
	})
	sh.finishResponse("tool_use")

	out := rec.buf.String()
	testutil.MustNotContain(t, out, `"type":"tool_use"`)
	testutil.MustContain(t, out, "Ready")
	testutil.MustContain(t, out, `"stop_reason":"end_turn"`)
}

func TestStreamHandler_SuccessFallbackOverridesZeroUpstreamUsage(t *testing.T) {
	sh, rec := newStreamTestHandler(t, false, true, adapter.FormatAnthropic)

	sh.setEmptyOutputFallback("File operation completed successfully.")
	sh.handleMessage(upstream.SSEMessage{
		Type: "model.finish",
		Event: map[string]any{
			"finishReason": "end_turn",
			"usage":        map[string]any{"inputTokens": 20, "outputTokens": 0},
		},
	})

	out := rec.buf.String()
	testutil.MustContain(t, out, "File operation completed successfully.")
	// The opening frame reports the usage known when it is opened, which is zero
	// output tokens -- the same figure the eager opening call carried before it was
	// deferred, so a whole-body search for a zero would now always find it. The
	// report that decides the fallback is the terminal one, and that is where the
	// synthesised text has to be counted.
	terminalAt := strings.Index(out, "event: message_delta")
	if terminalAt < 0 {
		t.Fatalf("expected a terminal usage report, got: %s", out)
	}
	terminal := out[terminalAt:]
	testutil.MustNotContain(t, terminal, `"output_tokens":0`)
	testutil.MustContain(t, terminal, `"usage":{"output_tokens":`)
}

func TestResponseMessageID_OpenAIUsesChatCompletionPrefix(t *testing.T) {
	id := responseMessageID(adapter.FormatOpenAI)
	if !strings.HasPrefix(id, "chatcmpl-") {
		t.Fatalf("id=%q want chatcmpl- prefix", id)
	}
	testutil.NotEqual(t, id, responseMessageID(adapter.FormatOpenAI))
}

func TestResponseMessageID_AnthropicKeepsMessagePrefix(t *testing.T) {
	id := responseMessageID(adapter.FormatAnthropic)
	if !strings.HasPrefix(id, "msg_") {
		t.Fatalf("id=%q want msg_ prefix", id)
	}
}

func TestStreamHandler_ReasoningCountsAsUpstreamOutputWhenSuppressed(t *testing.T) {
	sh, _ := newStreamTestHandler(t, true, true, adapter.FormatAnthropic)

	sh.handleMessage(upstream.SSEMessage{
		Type:  "model.reasoning-delta",
		Event: map[string]any{"delta": "already generated and potentially billed"},
	})

	if !sh.hasAnyOutput() {
		t.Fatal("suppressed reasoning must count as upstream output to prevent a billed retry")
	}
	if sh.hasVisibleOutput() {
		t.Fatal("suppressed reasoning must not become visible client output")
	}
}

// TestReportRequestFailure_ClientRejectionAnswers400 pins the branch the
// non-retryable upstream failure takes: a rejection of the request itself is 400,
// and the client still gets the operator-facing text.
//
// This path used to answer 200 with the message as assistant content, which is
// indistinguishable from an answer.
func TestReportRequestFailure_ClientRejectionAnswers400(t *testing.T) {
	rec := httptest.NewRecorder()
	sh := newStreamHandler(&config.Config{}, rec, debug.New(false, false), true, false, adapter.FormatAnthropic)

	sh.reportRequestFailure("probe", "client", "The upstream rejected the request parameters or model. Check the request and model selection.", 0)

	testutil.Equal(t, rec.Code, http.StatusBadRequest)
	body := rec.Body.String()
	testutil.MustContainAll(t, body, `"type":"error"`, "rejected the request parameters")
}
