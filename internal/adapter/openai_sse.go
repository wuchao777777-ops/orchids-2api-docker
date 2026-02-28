package adapter

import "github.com/goccy/go-json"

// BuildOpenAIChunk 将 Anthropic SSE 事件转换为 OpenAI chunk。
func BuildOpenAIChunk(msgID string, created int64, event string, data []byte) ([]byte, bool) {
	chunk := map[string]interface{}{
		"id":      msgID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   "",
		"choices": []map[string]interface{}{},
	}

	var parsedData map[string]interface{}
	if err := json.Unmarshal(data, &parsedData); err != nil {
		return nil, false
	}

	choice := map[string]interface{}{
		"index": 0,
		"delta": map[string]interface{}{},
	}

	switch event {
	case "message_start":
		if msg, ok := parsedData["message"].(map[string]interface{}); ok {
			choice["delta"] = map[string]interface{}{"role": "assistant"}
			if model, ok := msg["model"].(string); ok {
				chunk["model"] = model
			}
		}
	case "content_block_start":
		if cb, ok := parsedData["content_block"].(map[string]interface{}); ok {
			if cb["type"] == "text" {
				if text, ok := cb["text"].(string); ok && text != "" {
					choice["delta"] = map[string]interface{}{"content": text}
				}
			} else if cb["type"] == "tool_use" {
				choice["delta"] = map[string]interface{}{
					"tool_calls": []map[string]interface{}{
						{
							"index": 0,
							"id":    cb["id"],
							"type":  "function",
							"function": map[string]interface{}{
								"name":      cb["name"],
								"arguments": "",
							},
						},
					},
				}
			}
		}
	case "content_block_delta":
		if delta, ok := parsedData["delta"].(map[string]interface{}); ok {
			if delta["type"] == "text_delta" {
				choice["delta"] = map[string]interface{}{"content": delta["text"]}
			} else if delta["type"] == "input_json_delta" {
				choice["delta"] = map[string]interface{}{
					"tool_calls": []map[string]interface{}{
						{
							"index": 0,
							"function": map[string]interface{}{
								"arguments": delta["partial_json"],
							},
						},
					},
				}
			} else if delta["type"] == "thinking_delta" {
				choice["delta"] = map[string]interface{}{"reasoning_content": delta["thinking"]}
			}
		}
	case "message_delta":
		if delta, ok := parsedData["delta"].(map[string]interface{}); ok {
			if stopReason, ok := delta["stop_reason"].(string); ok {
				choice["finish_reason"] = stopReason
			}
		}
		choice["delta"] = map[string]interface{}{}
	case "message_stop":
		choice["finish_reason"] = "stop"
		choice["delta"] = map[string]interface{}{}
	case "content_block_stop":
		return nil, false
	default:
		return nil, false
	}

	delta, _ := choice["delta"].(map[string]interface{})
	if len(delta) == 0 && choice["finish_reason"] == nil {
		return nil, false
	}

	chunk["choices"] = []interface{}{choice}
	bytes, err := json.Marshal(chunk)
	if err != nil {
		return nil, false
	}
	return bytes, true
}
