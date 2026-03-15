package orchids

import "orchids-api/internal/upstream"

func emitOrchidsMessageStart(state *requestState, onMessage func(upstream.SSEMessage)) {
	NewSSEWriter(state, onMessage).WriteMessageStart()
}

func emitOrchidsUsageEvent(state *requestState, usage orchidsFastUsage, onMessage func(upstream.SSEMessage)) {
	NewSSEWriter(state, onMessage).WriteUsage(usage)
}

func emitOrchidsUsageMapEvent(state *requestState, usage map[string]interface{}, onMessage func(upstream.SSEMessage)) {
	NewSSEWriter(state, onMessage).WriteUsageMap(usage)
}

func emitOrchidsCompletionTail(state *requestState, onMessage func(upstream.SSEMessage)) {
	NewSSEWriter(state, onMessage).WriteMessageEnd()
}

func emitOrchidsReasoningDelta(state *requestState, onMessage func(upstream.SSEMessage), text string) bool {
	return NewSSEWriter(state, onMessage).WriteThinkingDelta(text)
}

func emitOrchidsTextDelta(state *requestState, onMessage func(upstream.SSEMessage), eventType, text string) bool {
	return NewSSEWriter(state, onMessage).WriteTextDelta(eventType, text)
}

func emitOrchidsToolCalls(toolCalls []orchidsToolCall, state *requestState, onMessage func(upstream.SSEMessage)) bool {
	return NewSSEWriter(state, onMessage).WriteToolCalls(toolCalls)
}

func setOrchidsErrorState(state *requestState, code, message string, quota bool) {
	if quota {
		state.errorMsg = "no remaining quota: " + message
		return
	}
	if code != "" {
		state.errorMsg = code + ": " + message
		return
	}
	state.errorMsg = message
}

func emitOrchidsErrorEvent(state *requestState, onMessage func(upstream.SSEMessage), code, message string) {
	NewSSEWriter(state, onMessage).WriteError(code, message)
}
