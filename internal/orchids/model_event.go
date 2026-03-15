package orchids

import (
	"strings"

	"github.com/goccy/go-json"

	"orchids-api/internal/upstream"
)

func orchidsModelPayload(event map[string]interface{}) map[string]interface{} {
	if len(event) == 0 {
		return nil
	}

	payload := make(map[string]interface{}, len(event))
	if nested, ok := event["data"].(map[string]interface{}); ok {
		for key, value := range nested {
			payload[key] = value
		}
	}
	for key, value := range event {
		if key == "data" {
			continue
		}
		if _, exists := payload[key]; !exists {
			payload[key] = value
		}
	}
	return payload
}

func orchidsModelEventType(event map[string]interface{}) string {
	payload := orchidsModelPayload(event)
	if payload == nil {
		return ""
	}
	if eventType, ok := payload["type"].(string); ok {
		return strings.TrimSpace(eventType)
	}
	return ""
}

func orchidsShouldSuppressCodingAgentDuplicate(eventType string) bool {
	switch eventType {
	case "text-start", "text-delta", "text-end", "reasoning-start", "reasoning-delta", "reasoning-end":
		return true
	default:
		return false
	}
}

func normalizeOrchidsUsage(usage map[string]interface{}) map[string]interface{} {
	if len(usage) == 0 {
		return nil
	}

	normalized := make(map[string]interface{}, len(usage)+3)
	for key, value := range usage {
		normalized[key] = value
	}
	if _, ok := normalized["inputTokens"]; !ok {
		if value, ok := normalized["input_tokens"]; ok {
			normalized["inputTokens"] = value
		}
	}
	if _, ok := normalized["outputTokens"]; !ok {
		if value, ok := normalized["output_tokens"]; ok {
			normalized["outputTokens"] = value
		}
	}
	if _, ok := normalized["cacheReadInputTokens"]; !ok {
		if value, ok := normalized["cache_read_input_tokens"]; ok {
			normalized["cacheReadInputTokens"] = value
		}
	}
	return normalized
}

func orchidsToolInputString(payload map[string]interface{}) string {
	if payload == nil {
		return ""
	}

	for _, key := range []string{"input", "arguments", "partial_json"} {
		if value, ok := payload[key].(string); ok {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}

	if input, ok := payload["input"]; ok && input != nil {
		return marshalOrchidsToolInput(input)
	}
	return ""
}

func normalizeOrchidsModelEvent(event map[string]interface{}, clientTools []interface{}) map[string]interface{} {
	payload := orchidsModelPayload(event)
	if len(payload) == 0 {
		return nil
	}

	eventType, _ := payload["type"].(string)
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return nil
	}

	normalized := map[string]interface{}{"type": eventType}

	switch eventType {
	case "text-start", "text-end", "reasoning-start", "reasoning-end":
		if id, ok := payload["id"].(string); ok && strings.TrimSpace(id) != "" {
			normalized["id"] = strings.TrimSpace(id)
		} else {
			normalized["id"] = "0"
		}

	case "text-delta", "reasoning-delta", "tool-input-delta":
		if id, ok := payload["id"].(string); ok && strings.TrimSpace(id) != "" {
			normalized["id"] = strings.TrimSpace(id)
		} else if eventType != "tool-input-delta" {
			normalized["id"] = "0"
		}
		if delta, ok := payload["delta"].(string); ok && delta != "" {
			normalized["delta"] = delta
		}

	case "tool-input-start":
		if id, ok := payload["id"].(string); ok && strings.TrimSpace(id) != "" {
			normalized["id"] = strings.TrimSpace(id)
		}
		toolName, _ := payload["toolName"].(string)
		if strings.TrimSpace(toolName) == "" {
			toolName, _ = payload["tool_name"].(string)
		}
		if strings.TrimSpace(toolName) == "" {
			toolName, _ = payload["name"].(string)
		}
		if strings.TrimSpace(toolName) != "" {
			normalized["toolName"] = MapToolNameToClient(toolName, clientTools)
		}

	case "tool-input-end":
		if id, ok := payload["id"].(string); ok && strings.TrimSpace(id) != "" {
			normalized["id"] = strings.TrimSpace(id)
		}

	case "tool-call":
		toolID, _ := payload["toolCallId"].(string)
		if strings.TrimSpace(toolID) == "" {
			toolID, _ = payload["callId"].(string)
		}
		if strings.TrimSpace(toolID) == "" {
			toolID, _ = payload["id"].(string)
		}
		toolName, _ := payload["toolName"].(string)
		if strings.TrimSpace(toolName) == "" {
			toolName, _ = payload["tool_name"].(string)
		}
		if strings.TrimSpace(toolName) == "" {
			toolName, _ = payload["name"].(string)
		}
		clientName := MapToolNameToClient(toolName, clientTools)
		input := orchidsToolInputString(payload)
		input = transformToolInputJSON(toolName, clientName, input)
		if strings.TrimSpace(toolID) == "" {
			toolID = fallbackOrchidsToolCallID(clientName, input)
		}
		if strings.TrimSpace(toolID) != "" {
			normalized["toolCallId"] = strings.TrimSpace(toolID)
		}
		if strings.TrimSpace(clientName) != "" {
			normalized["toolName"] = clientName
		}
		if strings.TrimSpace(input) != "" {
			normalized["input"] = input
		}

	case "tokens-used":
		if value, ok := payload["inputTokens"]; ok {
			normalized["inputTokens"] = value
		} else if value, ok := payload["input_tokens"]; ok {
			normalized["inputTokens"] = value
		}
		if value, ok := payload["outputTokens"]; ok {
			normalized["outputTokens"] = value
		} else if value, ok := payload["output_tokens"]; ok {
			normalized["outputTokens"] = value
		}

	case "finish":
		if finishReason, ok := payload["finishReason"].(string); ok && strings.TrimSpace(finishReason) != "" {
			normalized["finishReason"] = strings.TrimSpace(finishReason)
		} else if stopReason, ok := payload["stop_reason"].(string); ok && strings.TrimSpace(stopReason) != "" {
			normalized["finishReason"] = strings.TrimSpace(stopReason)
		}
		if usage, ok := payload["usage"].(map[string]interface{}); ok && len(usage) > 0 {
			normalized["usage"] = normalizeOrchidsUsage(usage)
		}

	default:
		for key, value := range payload {
			if key == "type" || key == "data" {
				continue
			}
			normalized[key] = value
		}
	}

	return normalized
}

func emitOrchidsModelEvent(
	rawEvent map[string]interface{},
	state *requestState,
	onMessage func(upstream.SSEMessage),
	clientTools []interface{},
	raw map[string]interface{},
) bool {
	eventType := orchidsModelEventType(rawEvent)
	if eventType == "" {
		return false
	}
	if shouldSuppressOrchidsModelEvent(state, eventType) {
		return false
	}

	normalized := normalizeOrchidsModelEvent(rawEvent, clientTools)
	if len(normalized) == 0 {
		return false
	}

	if usage, ok := normalized["usage"].(map[string]interface{}); ok {
		recordOrchidsUsage(state, usage)
	}

	if eventType == "finish" {
		if finishReason, ok := normalized["finishReason"].(string); ok {
			state.finishReason = strings.TrimSpace(finishReason)
		}
		if state.stream {
			emitOrchidsCompletionTail(state, onMessage)
			return true
		}
	}

	applyOrchidsModelState(state, eventType)

	onMessage(upstream.SSEMessage{Type: "model", Event: normalized, Raw: raw})
	if eventType == "finish" {
		state.finishSent = true
	}

	return eventType == "finish"
}

func decodeOrchidsModelEvent(raw json.RawMessage) map[string]interface{} {
	if len(raw) == 0 {
		return nil
	}
	var event map[string]interface{}
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil
	}
	return event
}
