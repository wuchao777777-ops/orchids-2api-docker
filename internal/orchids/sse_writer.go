package orchids

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/goccy/go-json"

	"orchids-api/internal/upstream"
)

// SSEWriter emits CodeFreeMax-style final SSE frames for Orchids streams.
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

func (w *SSEWriter) directEmitter() upstream.DirectSSEEmitter {
	if w == nil || w.state == nil {
		return nil
	}
	return w.state.directSSE
}

func recordOrchidsEmittedToolCall(state *requestState, id string) bool {
	if state == nil {
		return true
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return true
	}
	if state.emittedToolCallIDs == nil {
		state.emittedToolCallIDs = make(map[string]struct{})
	}
	if _, exists := state.emittedToolCallIDs[id]; exists {
		return false
	}
	state.emittedToolCallIDs[id] = struct{}{}
	return true
}

func (w *SSEWriter) emitEvent(event string, payload map[string]interface{}, final bool) {
	if w == nil {
		return
	}
	if direct := w.directEmitter(); direct != nil {
		raw, err := json.Marshal(payload)
		if err == nil {
			direct.WriteDirectSSE(event, raw, final)
			return
		}
	}
	if w.onMessage != nil {
		w.onMessage(upstream.SSEMessage{Type: event, Event: payload})
	}
}

func (w *SSEWriter) WriteMessageStart() {
	if w == nil || w.state == nil || !w.state.stream || w.state.messageStarted {
		return
	}
	w.state.messageStarted = true
	w.emitEvent("message_start", orchidsMessageStartEvent(w.state.modelName), false)
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
	if direct := w.directEmitter(); direct != nil {
		inputTokens, _ := orchidsUsageInt(normalized["inputTokens"])
		outputTokens, _ := orchidsUsageInt(normalized["outputTokens"])
		direct.ObserveUsage(inputTokens, outputTokens)
		return
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
		w.emitEvent("content_block_stop", orchidsContentBlockStopEvent(snapshot.textBlockIndex), false)
	}
	if snapshot.emitReasoningEnd && snapshot.reasoningBlockIndex >= 0 {
		w.emitEvent("content_block_stop", orchidsContentBlockStopEvent(snapshot.reasoningBlockIndex), false)
	}
	if snapshot.emitFinish {
		if w.state.stream {
			if direct := w.directEmitter(); direct != nil {
				direct.ObserveStopReason(orchidsFinalStopReason(snapshot.finishReason))
			}
			w.emitEvent("message_delta", orchidsMessageDeltaEvent(snapshot.finishReason, w.state.outputTokens), false)
			w.emitEvent("message_stop", orchidsMessageStopEvent(), true)
			if direct := w.directEmitter(); direct != nil {
				direct.FinishDirectSSE(orchidsFinalStopReason(snapshot.finishReason))
			}
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
		w.emitEvent("content_block_start", orchidsContentBlockStartThinkingEvent(orchidsActiveReasoningBlockIndex(w.state)), false)
	}
	if direct := w.directEmitter(); direct != nil {
		direct.ObserveThinkingDelta(text)
	}
	w.emitEvent("content_block_delta", orchidsContentBlockDeltaThinkingEvent(orchidsActiveReasoningBlockIndex(w.state), text), false)
	return true
}

func (w *SSEWriter) WriteReasoningStart() bool {
	if w == nil || w.state == nil {
		return false
	}
	if !beginOrchidsReasoning(w.state) {
		return false
	}
	w.emitEvent("content_block_start", orchidsContentBlockStartThinkingEvent(orchidsActiveReasoningBlockIndex(w.state)), false)
	return true
}

func (w *SSEWriter) WriteReasoningEnd() bool {
	if w == nil || w.state == nil {
		return false
	}
	index := orchidsActiveReasoningBlockIndex(w.state)
	if index < 0 || !endOrchidsReasoning(w.state) {
		return false
	}
	w.emitEvent("content_block_stop", orchidsContentBlockStopEvent(index), false)
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
		w.emitEvent("content_block_start", orchidsContentBlockStartTextEvent(orchidsActiveTextBlockIndex(w.state)), false)
	}
	if direct := w.directEmitter(); direct != nil {
		direct.ObserveTextDelta(text)
	}
	w.emitEvent("content_block_delta", orchidsContentBlockDeltaTextEvent(orchidsActiveTextBlockIndex(w.state), text), false)
	return true
}

func (w *SSEWriter) WriteTextStart() bool {
	if w == nil || w.state == nil {
		return false
	}
	if !beginOrchidsText(w.state) {
		return false
	}
	w.emitEvent("content_block_start", orchidsContentBlockStartTextEvent(orchidsActiveTextBlockIndex(w.state)), false)
	return true
}

func (w *SSEWriter) WriteTextEnd() bool {
	if w == nil || w.state == nil {
		return false
	}
	index := orchidsActiveTextBlockIndex(w.state)
	if index < 0 || !endOrchidsText(w.state) {
		return false
	}
	w.emitEvent("content_block_stop", orchidsContentBlockStopEvent(index), false)
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
	if !recordOrchidsEmittedToolCall(w.state, toolID) {
		return false
	}

	index := 0
	if w.state != nil {
		if textIndex := orchidsActiveTextBlockIndex(w.state); textIndex >= 0 && endOrchidsText(w.state) {
			w.emitEvent("content_block_stop", orchidsContentBlockStopEvent(textIndex), false)
		}
		if reasoningIndex := orchidsActiveReasoningBlockIndex(w.state); reasoningIndex >= 0 && endOrchidsReasoning(w.state) {
			w.emitEvent("content_block_stop", orchidsContentBlockStopEvent(reasoningIndex), false)
		}
		index = nextOrchidsBlockIndex(w.state)
	}

	w.emitEvent("content_block_start", orchidsContentBlockStartToolUseEvent(index, toolID, toolName), false)

	if input := strings.TrimSpace(call.input); input != "" {
		w.emitEvent("content_block_delta", orchidsContentBlockDeltaInputJSONEvent(index, input), false)
	}

	if direct := w.directEmitter(); direct != nil {
		direct.ObserveToolCall(toolName, call.input)
	}
	w.emitEvent("content_block_stop", orchidsContentBlockStopEvent(index), false)

	return true
}

func (w *SSEWriter) WriteToolInputStart(id, name string) bool {
	if w == nil || w.state == nil {
		return false
	}
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if id == "" {
		id = generateOrchidsToolUseID()
	}
	if id == "" {
		return false
	}
	if !recordOrchidsEmittedToolCall(w.state, id) {
		return false
	}

	index := nextOrchidsBlockIndex(w.state)
	if textIndex := orchidsActiveTextBlockIndex(w.state); textIndex >= 0 && endOrchidsText(w.state) {
		w.emitEvent("content_block_stop", orchidsContentBlockStopEvent(textIndex), false)
	}
	if reasoningIndex := orchidsActiveReasoningBlockIndex(w.state); reasoningIndex >= 0 && endOrchidsReasoning(w.state) {
		w.emitEvent("content_block_stop", orchidsContentBlockStopEvent(reasoningIndex), false)
	}
	if w.state.pendingToolInputs == nil {
		w.state.pendingToolInputs = make(map[string]*orchidsPendingToolInput)
	}
	w.state.pendingToolInputs[id] = &orchidsPendingToolInput{
		name:       name,
		blockIndex: index,
	}
	w.state.lastPendingToolID = id
	w.emitEvent("content_block_start", orchidsContentBlockStartToolUseEvent(index, id, name), false)
	return true
}

func (w *SSEWriter) WriteToolInputDelta(id, partialJSON string) bool {
	if w == nil || w.state == nil {
		return false
	}
	id = resolvePendingToolInputID(w.state, id)
	if id == "" || partialJSON == "" || w.state.pendingToolInputs == nil {
		return false
	}
	pending := w.state.pendingToolInputs[id]
	if pending == nil {
		return false
	}
	pending.buf.WriteString(partialJSON)
	w.emitEvent("content_block_delta", orchidsContentBlockDeltaInputJSONEvent(pending.blockIndex, partialJSON), false)
	return true
}

func (w *SSEWriter) WriteToolInputEnd(id string) bool {
	if w == nil || w.state == nil {
		return false
	}
	id = resolvePendingToolInputID(w.state, id)
	if id == "" || w.state.pendingToolInputs == nil {
		return false
	}
	pending := w.state.pendingToolInputs[id]
	if pending == nil {
		return false
	}
	delete(w.state.pendingToolInputs, id)
	if w.state.lastPendingToolID == id {
		w.state.lastPendingToolID = ""
	}
	w.state.sawToolCall = true
	if direct := w.directEmitter(); direct != nil {
		direct.ObserveToolCall(pending.name, strings.TrimSpace(pending.buf.String()))
	}
	w.emitEvent("content_block_stop", orchidsContentBlockStopEvent(pending.blockIndex), false)
	return true
}

func resolvePendingToolInputID(state *requestState, id string) string {
	id = strings.TrimSpace(id)
	if id != "" || state == nil {
		return id
	}
	return strings.TrimSpace(state.lastPendingToolID)
}

func generateOrchidsToolUseID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	encoded := make([]byte, hex.EncodedLen(len(buf)))
	hex.Encode(encoded, buf)
	return "toolu_" + string(encoded)
}

func (w *SSEWriter) WriteError(code, message string) {
	if w == nil {
		return
	}
	w.emitEvent("error", map[string]interface{}{
		"type":    "error",
		"code":    code,
		"message": message,
	}, false)
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

func orchidsContentBlockStartTextEvent(index int) map[string]interface{} {
	return map[string]interface{}{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]interface{}{
			"type": "text",
			"text": "",
		},
	}
}

func orchidsMessageStartEvent(model string) map[string]interface{} {
	return map[string]interface{}{
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
	}
}

func orchidsMessageDeltaEvent(finishReason string, outputTokens int) map[string]interface{} {
	return map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason": orchidsFinalStopReason(finishReason),
		},
		"usage": map[string]interface{}{
			"output_tokens": outputTokens,
		},
	}
}

func orchidsMessageStopEvent() map[string]interface{} {
	return map[string]interface{}{
		"type": "message_stop",
	}
}

func orchidsContentBlockStartToolUseEvent(index int, id, name string) map[string]interface{} {
	return map[string]interface{}{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]interface{}{
			"type":  "tool_use",
			"id":    id,
			"name":  name,
			"input": map[string]interface{}{},
		},
	}
}

func orchidsContentBlockStartThinkingEvent(index int) map[string]interface{} {
	return map[string]interface{}{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]interface{}{
			"type":      "thinking",
			"thinking":  "",
			"signature": "",
		},
	}
}

func orchidsContentBlockDeltaInputJSONEvent(index int, partialJSON string) map[string]interface{} {
	return map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{
			"type":         "input_json_delta",
			"partial_json": partialJSON,
		},
	}
}

func orchidsContentBlockDeltaTextEvent(index int, text string) map[string]interface{} {
	return map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{
			"type": "text_delta",
			"text": text,
		},
	}
}

func orchidsContentBlockDeltaThinkingEvent(index int, text string) map[string]interface{} {
	return map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{
			"type":     "thinking_delta",
			"thinking": text,
		},
	}
}

func orchidsContentBlockStopEvent(index int) map[string]interface{} {
	return map[string]interface{}{
		"type":  "content_block_stop",
		"index": index,
	}
}

func orchidsFinalStopReason(finishReason string) string {
	return orchidsNormalizeFinishReason(finishReason)
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
