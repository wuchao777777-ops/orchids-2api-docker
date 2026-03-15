package orchids

import (
	"strings"

	"orchids-api/internal/upstream"
)

// SSEWriter keeps Orchids stream emission close to the CodeFreeMax shape while
// still targeting this repo's upstream.SSEMessage boundary.
type SSEWriter struct {
	state     *requestState
	onMessage func(upstream.SSEMessage)
}

func NewSSEWriter(state *requestState, onMessage func(upstream.SSEMessage)) *SSEWriter {
	if onMessage == nil {
		return nil
	}
	return &SSEWriter{state: state, onMessage: onMessage}
}

func (w *SSEWriter) WriteMessageStart() {
	if w == nil || w.state == nil || !w.state.stream || w.state.messageStarted {
		return
	}
	w.state.messageStarted = true
	w.onMessage(orchidsMessageStartEvent(w.state.modelName))
}

func (w *SSEWriter) WriteUsage(usage orchidsFastUsage) {
	if w == nil {
		return
	}

	usageMap := make(map[string]interface{}, 4)
	if usage.InputTokensSnake != nil {
		usageMap["input_tokens"] = usage.InputTokensSnake
	} else if usage.InputTokens != nil {
		usageMap["inputTokens"] = usage.InputTokens
	}
	if usage.OutputTokensSnake != nil {
		usageMap["output_tokens"] = usage.OutputTokensSnake
	} else if usage.OutputTokens != nil {
		usageMap["outputTokens"] = usage.OutputTokens
	}
	w.WriteUsageMap(usageMap)
}

func (w *SSEWriter) WriteUsageMap(usage map[string]interface{}) {
	if w == nil {
		return
	}

	normalized := normalizeOrchidsUsage(usage)
	if len(normalized) == 0 {
		return
	}
	if w.state != nil {
		recordOrchidsUsage(w.state, normalized)
	}

	event := map[string]interface{}{"type": "tokens-used"}
	if value, ok := normalized["inputTokens"]; ok {
		event["inputTokens"] = value
	}
	if value, ok := normalized["outputTokens"]; ok {
		event["outputTokens"] = value
	}
	if value, ok := normalized["cacheReadInputTokens"]; ok {
		event["cacheReadInputTokens"] = value
	}
	if len(event) > 1 {
		w.onMessage(upstream.SSEMessage{Type: "model", Event: event})
	}
}

func (w *SSEWriter) WriteMessageEnd() {
	if w == nil || w.state == nil {
		return
	}

	snapshot := snapshotOrchidsCompletion(w.state)
	if snapshot.emitTextEnd && snapshot.textBlockIndex >= 0 {
		w.onMessage(orchidsContentBlockStopEvent(snapshot.textBlockIndex))
	}
	if snapshot.emitReasoningEnd && snapshot.reasoningBlockIndex >= 0 {
		w.onMessage(orchidsContentBlockStopEvent(snapshot.reasoningBlockIndex))
	}
	if snapshot.emitFinish {
		if w.state.stream {
			w.onMessage(orchidsMessageDeltaEvent(snapshot.finishReason, w.state.outputTokens))
			w.onMessage(orchidsMessageStopEvent())
			return
		}
		w.onMessage(upstream.SSEMessage{Type: "model", Event: map[string]interface{}{"type": "finish", "finishReason": snapshot.finishReason}})
	}
}

func (w *SSEWriter) WriteThinkingDelta(text string) bool {
	if w == nil || w.state == nil || text == "" {
		return false
	}
	if beginOrchidsReasoning(w.state) {
		w.onMessage(orchidsContentBlockStartThinkingEvent(orchidsActiveReasoningBlockIndex(w.state)))
	}
	w.onMessage(orchidsContentBlockDeltaThinkingEvent(orchidsActiveReasoningBlockIndex(w.state), text))
	return true
}

func (w *SSEWriter) WriteTextDelta(eventType, text string) bool {
	if w == nil || w.state == nil {
		return false
	}
	if !acceptOrchidsTextDelta(w.state, eventType, text) {
		return false
	}
	if beginOrchidsText(w.state) {
		w.onMessage(orchidsContentBlockStartTextEvent(orchidsActiveTextBlockIndex(w.state)))
	}
	w.onMessage(orchidsContentBlockDeltaTextEvent(orchidsActiveTextBlockIndex(w.state), text))
	return true
}

func (w *SSEWriter) WriteToolCalls(toolCalls []orchidsToolCall) bool {
	if w == nil || w.state == nil || len(toolCalls) == 0 {
		return false
	}
	if !recordOrchidsToolCalls(w.state, len(toolCalls)) {
		return false
	}

	wroteAny := false
	for _, call := range toolCalls {
		if w.WriteToolUseBlock(call) {
			wroteAny = true
		}
	}
	if !wroteAny {
		return false
	}
	return true
}

func (w *SSEWriter) WriteToolUseBlock(call orchidsToolCall) bool {
	if w == nil {
		return false
	}

	toolID := strings.TrimSpace(call.id)
	toolName := strings.TrimSpace(call.name)
	if toolID == "" || toolName == "" {
		return false
	}

	index := 0
	if w.state != nil {
		if textIndex := orchidsActiveTextBlockIndex(w.state); textIndex >= 0 && endOrchidsText(w.state) {
			w.onMessage(orchidsContentBlockStopEvent(textIndex))
		}
		if reasoningIndex := orchidsActiveReasoningBlockIndex(w.state); reasoningIndex >= 0 && endOrchidsReasoning(w.state) {
			w.onMessage(orchidsContentBlockStopEvent(reasoningIndex))
		}
		index = nextOrchidsBlockIndex(w.state)
	}

	w.onMessage(orchidsContentBlockStartToolUseEvent(index, toolID, toolName))

	if input := strings.TrimSpace(call.input); input != "" {
		w.onMessage(orchidsContentBlockDeltaInputJSONEvent(index, input))
	}

	w.onMessage(orchidsContentBlockStopEvent(index))

	return true
}

func (w *SSEWriter) WriteError(code, message string) {
	if w == nil {
		return
	}
	w.onMessage(upstream.SSEMessage{
		Type: "error",
		Event: map[string]interface{}{
			"type":    "error",
			"code":    code,
			"message": message,
		},
	})
}

func orchidsActiveTextBlockIndex(state *requestState) int {
	if state == nil || !state.textStarted {
		return -1
	}
	if state.textBlockIndex < 0 {
		return 0
	}
	return state.textBlockIndex
}

func orchidsActiveReasoningBlockIndex(state *requestState) int {
	if state == nil || !state.reasoningStarted {
		return -1
	}
	if state.reasoningBlockIndex < 0 {
		return 0
	}
	return state.reasoningBlockIndex
}

func orchidsContentBlockStartTextEvent(index int) upstream.SSEMessage {
	return upstream.SSEMessage{
		Type: "content_block_start",
		Event: map[string]interface{}{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]interface{}{
				"type": "text",
				"text": "",
			},
		},
	}
}

func orchidsMessageStartEvent(model string) upstream.SSEMessage {
	return upstream.SSEMessage{
		Type: "message_start",
		Event: map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":      "",
				"type":    "message",
				"role":    "assistant",
				"model":   model,
				"content": []interface{}{},
				"usage": map[string]interface{}{
					"input_tokens":  0,
					"output_tokens": 0,
				},
			},
		},
	}
}

func orchidsMessageDeltaEvent(finishReason string, outputTokens int) upstream.SSEMessage {
	return upstream.SSEMessage{
		Type: "message_delta",
		Event: map[string]interface{}{
			"type": "message_delta",
			"delta": map[string]interface{}{
				"stop_reason": orchidsFinalStopReason(finishReason),
			},
			"usage": map[string]interface{}{
				"output_tokens": outputTokens,
			},
		},
	}
}

func orchidsMessageStopEvent() upstream.SSEMessage {
	return upstream.SSEMessage{
		Type: "message_stop",
		Event: map[string]interface{}{
			"type": "message_stop",
		},
	}
}

func orchidsContentBlockStartToolUseEvent(index int, id, name string) upstream.SSEMessage {
	return upstream.SSEMessage{
		Type: "content_block_start",
		Event: map[string]interface{}{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]interface{}{
				"type":  "tool_use",
				"id":    id,
				"name":  name,
				"input": map[string]interface{}{},
			},
		},
	}
}

func orchidsContentBlockStartThinkingEvent(index int) upstream.SSEMessage {
	return upstream.SSEMessage{
		Type: "content_block_start",
		Event: map[string]interface{}{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]interface{}{
				"type":      "thinking",
				"thinking":  "",
				"signature": "",
			},
		},
	}
}

func orchidsContentBlockDeltaInputJSONEvent(index int, partialJSON string) upstream.SSEMessage {
	return upstream.SSEMessage{
		Type: "content_block_delta",
		Event: map[string]interface{}{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]interface{}{
				"type":         "input_json_delta",
				"partial_json": partialJSON,
			},
		},
	}
}

func orchidsContentBlockDeltaTextEvent(index int, text string) upstream.SSEMessage {
	return upstream.SSEMessage{
		Type: "content_block_delta",
		Event: map[string]interface{}{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]interface{}{
				"type": "text_delta",
				"text": text,
			},
		},
	}
}

func orchidsContentBlockDeltaThinkingEvent(index int, text string) upstream.SSEMessage {
	return upstream.SSEMessage{
		Type: "content_block_delta",
		Event: map[string]interface{}{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]interface{}{
				"type":     "thinking_delta",
				"thinking": text,
			},
		},
	}
}

func orchidsContentBlockStopEvent(index int) upstream.SSEMessage {
	return upstream.SSEMessage{
		Type: "content_block_stop",
		Event: map[string]interface{}{
			"type":  "content_block_stop",
			"index": index,
		},
	}
}

func orchidsFinalStopReason(finishReason string) string {
	switch strings.TrimSpace(finishReason) {
	case "tool-calls", "tool_use":
		return "tool_use"
	default:
		return "end_turn"
	}
}

func recordOrchidsUsage(state *requestState, usage map[string]interface{}) {
	if state == nil || len(usage) == 0 {
		return
	}
	if value, ok := orchidsUsageInt(usage["inputTokens"]); ok {
		state.inputTokens = value
	}
	if value, ok := orchidsUsageInt(usage["outputTokens"]); ok {
		state.outputTokens = value
	}
}

func orchidsUsageInt(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	}
	return 0, false
}
