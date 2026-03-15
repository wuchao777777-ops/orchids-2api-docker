package orchids

import (
	"strings"
	"testing"

	"orchids-api/internal/upstream"
)

type directSSERecorder struct {
	events       []string
	payloads     [][]byte
	finals       []bool
	texts        []string
	thinking     []string
	toolNames    []string
	toolInputs   []string
	inputTokens  []int
	outputTokens []int
	stopReasons  []string
	finished     []string
}

func (r *directSSERecorder) WriteDirectSSE(event string, payload []byte, final bool) {
	r.events = append(r.events, event)
	r.payloads = append(r.payloads, append([]byte(nil), payload...))
	r.finals = append(r.finals, final)
}

func (r *directSSERecorder) ObserveTextDelta(text string) {
	r.texts = append(r.texts, text)
}

func (r *directSSERecorder) ObserveThinkingDelta(text string) {
	r.thinking = append(r.thinking, text)
}

func (r *directSSERecorder) ObserveToolCall(name, input string) {
	r.toolNames = append(r.toolNames, name)
	r.toolInputs = append(r.toolInputs, input)
}

func (r *directSSERecorder) ObserveUsage(inputTokens, outputTokens int) {
	r.inputTokens = append(r.inputTokens, inputTokens)
	r.outputTokens = append(r.outputTokens, outputTokens)
}

func (r *directSSERecorder) ObserveStopReason(stopReason string) {
	r.stopReasons = append(r.stopReasons, stopReason)
}

func (r *directSSERecorder) FinishDirectSSE(stopReason string) {
	r.finished = append(r.finished, stopReason)
}

func TestSSEWriter_UsesDirectEmitterForStreamingFrames(t *testing.T) {
	rec := &directSSERecorder{}
	callbackCount := 0
	state := &requestState{
		stream:    true,
		modelName: "claude-sonnet-4-6",
		directSSE: rec,
	}

	writer := NewSSEWriter(state, func(upstreamMsg upstream.SSEMessage) {
		_ = upstreamMsg
		callbackCount++
	})

	writer.WriteMessageStart()
	writer.WriteUsageMap(map[string]interface{}{
		"inputTokens":  12,
		"outputTokens": 34,
	})
	if !writer.WriteTextDelta(EventOutputTextDelta, "hello") {
		t.Fatal("expected text delta to be written")
	}
	if !writer.WriteToolCalls([]orchidsToolCall{{id: "tool_1", name: "Read", input: `{"file_path":"/tmp/a.txt"}`}}) {
		t.Fatal("expected tool calls to be written")
	}
	writer.WriteMessageEnd()

	if callbackCount != 0 {
		t.Fatalf("expected direct emitter to bypass callback, got %d callback events", callbackCount)
	}
	if len(rec.events) == 0 {
		t.Fatal("expected direct SSE events to be recorded")
	}
	if rec.events[0] != "message_start" {
		t.Fatalf("first event = %q want message_start", rec.events[0])
	}
	last := len(rec.events) - 1
	if rec.events[last] != "message_stop" {
		t.Fatalf("last event = %q want message_stop", rec.events[last])
	}
	if !rec.finals[last] {
		t.Fatal("expected message_stop to be marked final")
	}
	if len(rec.texts) != 1 || rec.texts[0] != "hello" {
		t.Fatalf("text observations = %#v", rec.texts)
	}
	if len(rec.toolNames) != 1 || rec.toolNames[0] != "Read" {
		t.Fatalf("tool observations = %#v", rec.toolNames)
	}
	if len(rec.inputTokens) != 1 || rec.inputTokens[0] != 12 {
		t.Fatalf("input token observations = %#v", rec.inputTokens)
	}
	if len(rec.outputTokens) != 1 || rec.outputTokens[0] != 34 {
		t.Fatalf("output token observations = %#v", rec.outputTokens)
	}
	if len(rec.stopReasons) != 1 || rec.stopReasons[0] != "tool_use" {
		t.Fatalf("stop reasons = %#v", rec.stopReasons)
	}
	if len(rec.finished) != 1 || rec.finished[0] != "tool_use" {
		t.Fatalf("finish callbacks = %#v", rec.finished)
	}
}

func TestSSEWriter_GeneratesToolInputIDWhenMissing(t *testing.T) {
	rec := &directSSERecorder{}
	state := &requestState{
		stream:    true,
		modelName: "claude-sonnet-4-6",
		directSSE: rec,
	}

	writer := NewSSEWriter(state, func(upstream.SSEMessage) {})
	if !writer.WriteToolInputStart("", "Read") {
		t.Fatal("expected tool-input-start without id to be accepted")
	}
	if !strings.HasPrefix(state.lastPendingToolID, "toolu_") {
		t.Fatalf("generated id=%q want toolu_ prefix", state.lastPendingToolID)
	}
	if !writer.WriteToolInputDelta("", `{"file_path":"/tmp/demo.txt"}`) {
		t.Fatal("expected tool-input-delta without id to reuse generated id")
	}
	if !writer.WriteToolInputEnd("") {
		t.Fatal("expected tool-input-end without id to reuse generated id")
	}
	if len(rec.toolNames) != 1 || rec.toolNames[0] != "Read" {
		t.Fatalf("tool observations = %#v", rec.toolNames)
	}
	if len(rec.toolInputs) != 1 || rec.toolInputs[0] != `{"file_path":"/tmp/demo.txt"}` {
		t.Fatalf("tool inputs = %#v", rec.toolInputs)
	}
}

func TestSSEWriter_EmitsEmptyBlocksForExplicitStartEnd(t *testing.T) {
	rec := &directSSERecorder{}
	state := &requestState{
		stream:    true,
		modelName: "claude-sonnet-4-6",
		directSSE: rec,
	}

	writer := NewSSEWriter(state, func(upstream.SSEMessage) {})
	if !writer.WriteTextStart() {
		t.Fatal("expected text-start to open a block")
	}
	if !writer.WriteTextEnd() {
		t.Fatal("expected text-end to close the block")
	}
	if !writer.WriteReasoningStart() {
		t.Fatal("expected reasoning-start to open a block")
	}
	if !writer.WriteReasoningEnd() {
		t.Fatal("expected reasoning-end to close the block")
	}

	if len(rec.events) != 4 {
		t.Fatalf("events=%#v want 4 explicit start/end frames", rec.events)
	}
	if rec.events[0] != "content_block_start" || rec.events[1] != "content_block_stop" {
		t.Fatalf("text block events=%#v", rec.events[:2])
	}
	if rec.events[2] != "content_block_start" || rec.events[3] != "content_block_stop" {
		t.Fatalf("reasoning block events=%#v", rec.events[2:])
	}
}
