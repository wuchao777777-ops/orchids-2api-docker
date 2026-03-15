package orchids

import (
	"testing"

	"orchids-api/internal/upstream"
)

func TestSnapshotOrchidsCompletionResetsActiveState(t *testing.T) {
	t.Parallel()

	state := &requestState{
		textStarted:      true,
		reasoningStarted: true,
		sawToolCall:      true,
	}

	snapshot := snapshotOrchidsCompletion(state)
	if !snapshot.emitTextEnd || !snapshot.emitReasoningEnd || !snapshot.emitFinish {
		t.Fatalf("snapshot=%+v want text/reasoning/finish emissions", snapshot)
	}
	if snapshot.finishReason != "tool_use" {
		t.Fatalf("finishReason=%q want tool_use", snapshot.finishReason)
	}
	if state.textStarted || state.reasoningStarted {
		t.Fatalf("state should clear active blocks, got %+v", state)
	}
	if state.textBlockIndex != -1 || state.reasoningBlockIndex != -1 {
		t.Fatalf("state block indexes should reset, got %+v", state)
	}
	if !state.finishSent {
		t.Fatalf("finishSent=%v want true", state.finishSent)
	}
}

func TestAcceptOrchidsTextDeltaSuppressesCrossChannelDuplicate(t *testing.T) {
	t.Parallel()

	state := &requestState{
		lastTextDelta: "hello",
		lastTextEvent: EventOutputTextDelta,
	}

	if acceptOrchidsTextDelta(state, EventResponseChunk, "hello") {
		t.Fatal("expected cross-channel duplicate to be suppressed")
	}
	if !acceptOrchidsTextDelta(state, EventOutputTextDelta, "hello") {
		t.Fatal("expected same-channel repeat to be preserved")
	}
}

func TestApplyOrchidsModelStateFinishClearsOpenBlocks(t *testing.T) {
	t.Parallel()

	state := &requestState{
		textStarted:      true,
		reasoningStarted: true,
	}

	applyOrchidsModelState(state, "finish")
	if state.textStarted || state.reasoningStarted {
		t.Fatalf("state=%+v want closed blocks", state)
	}
	if state.textBlockIndex != -1 || state.reasoningBlockIndex != -1 {
		t.Fatalf("state block indexes should reset, got %+v", state)
	}
}

func TestNewOrchidsRequestStateNormalizesModelName(t *testing.T) {
	t.Parallel()

	state := newOrchidsRequestState(upstream.UpstreamRequest{
		Model: "claude-opus-4.5",
	})

	if state.modelName != "claude-opus-4-6" {
		t.Fatalf("modelName=%q want claude-opus-4-6", state.modelName)
	}
}
