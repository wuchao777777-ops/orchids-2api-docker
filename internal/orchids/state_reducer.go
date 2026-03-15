package orchids

import "strings"

type orchidsCompletionState struct {
	emitTextEnd         bool
	emitReasoningEnd    bool
	emitFinish          bool
	textBlockIndex      int
	reasoningBlockIndex int
	finishReason        string
}

func markOrchidsResponseStarted(state *requestState) bool {
	if state.responseStarted {
		return false
	}
	state.responseStarted = true
	return true
}

func beginOrchidsReasoning(state *requestState) bool {
	if state.reasoningStarted {
		return false
	}
	state.reasoningStarted = true
	state.reasoningBlockIndex = nextOrchidsBlockIndex(state)
	return true
}

func endOrchidsReasoning(state *requestState) bool {
	if !state.reasoningStarted {
		return false
	}
	state.reasoningStarted = false
	state.reasoningBlockIndex = -1
	return true
}

func beginOrchidsText(state *requestState) bool {
	if state.textStarted {
		return false
	}
	state.textStarted = true
	state.textBlockIndex = nextOrchidsBlockIndex(state)
	return true
}

func endOrchidsText(state *requestState) bool {
	if !state.textStarted {
		return false
	}
	state.textStarted = false
	state.textBlockIndex = -1
	return true
}

func nextOrchidsBlockIndex(state *requestState) int {
	idx := state.nextBlockIndex
	state.nextBlockIndex++
	return idx
}

func recordOrchidsToolCalls(state *requestState, count int) bool {
	if count <= 0 {
		return false
	}
	state.sawToolCall = true
	return true
}

func snapshotOrchidsCompletion(state *requestState) orchidsCompletionState {
	finishReason := strings.TrimSpace(state.finishReason)
	if finishReason == "" {
		finishReason = "end_turn"
		if state.sawToolCall {
			finishReason = "tool_use"
		}
	} else {
		finishReason = orchidsNormalizeFinishReason(finishReason)
	}

	snapshot := orchidsCompletionState{
		emitTextEnd:         state.textStarted,
		emitReasoningEnd:    state.reasoningStarted,
		emitFinish:          !state.finishSent,
		textBlockIndex:      state.textBlockIndex,
		reasoningBlockIndex: state.reasoningBlockIndex,
		finishReason:        finishReason,
	}

	state.textStarted = false
	state.reasoningStarted = false
	state.textBlockIndex = -1
	state.reasoningBlockIndex = -1
	state.finishReason = finishReason
	if snapshot.emitFinish {
		state.finishSent = true
	}

	return snapshot
}

func orchidsNormalizeFinishReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case "tool-calls", "tool_use":
		return "tool_use"
	case "stop", "end_turn":
		return "end_turn"
	default:
		return "end_turn"
	}
}

func applyOrchidsModelState(state *requestState, eventType string) {
	switch eventType {
	case "text-start", "text-delta":
		state.textStarted = true
	case "text-end":
		state.textStarted = false
	case "reasoning-start", "reasoning-delta":
		state.reasoningStarted = true
	case "reasoning-end":
		state.reasoningStarted = false
	case "tool-call":
		state.sawToolCall = true
	case "finish":
		state.textStarted = false
		state.reasoningStarted = false
		state.textBlockIndex = -1
		state.reasoningBlockIndex = -1
	}
}
