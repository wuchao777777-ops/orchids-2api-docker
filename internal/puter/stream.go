package puter

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/upstream"
)

type streamResult struct {
	SawMeaningfulEvent bool
	ToolCallCount      int
	Usage              map[string]int
}

func (r streamResult) FinishReason() string {
	if r.ToolCallCount > 0 {
		return "tool_use"
	}
	return "end_turn"
}

var puterToolCallSequence atomic.Uint64

func newToolCallID() string {
	return fmt.Sprintf("toolu_%d_%d", time.Now().UnixNano(), puterToolCallSequence.Add(1))
}

func consumePuterStream(body io.Reader, onMessage func(upstream.SSEMessage)) (streamResult, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	result := streamResult{}

	for scanner.Scan() {
		line := normalizePuterStreamLine(scanner.Text())
		if line == "" {
			continue
		}

		var apiErr ErrorResponse
		if err := json.Unmarshal([]byte(line), &apiErr); err == nil && apiErr.Error.Present() {
			return result, formatPuterAPIError(apiErr.Error.AsPayload(), line)
		}

		var chunk StreamChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(chunk.Type)) {
		case "text":
			result.SawMeaningfulEvent = true
			if onMessage != nil && chunk.Text != "" {
				onMessage(upstream.SSEMessage{Type: "model.text-delta", Event: map[string]interface{}{"delta": chunk.Text}})
			}
		case "reasoning":
			result.SawMeaningfulEvent = true
			if onMessage != nil && chunk.Reasoning != "" {
				onMessage(upstream.SSEMessage{Type: "model.reasoning-delta", Event: map[string]interface{}{"delta": chunk.Reasoning}})
			}
		case "tool_use":
			name := strings.TrimSpace(chunk.Name)
			if name == "" {
				return result, fmt.Errorf("puter stream returned tool_use without a name")
			}
			id := strings.TrimSpace(chunk.ID)
			if id == "" {
				id = newToolCallID()
			}
			result.SawMeaningfulEvent = true
			result.ToolCallCount++
			if onMessage != nil {
				onMessage(upstream.SSEMessage{Type: "model.tool-call", Event: map[string]interface{}{
					"toolCallId": id,
					"toolName":   name,
					"input":      normalizeStreamToolInput(chunk.Input),
				}})
			}
		case "usage":
			result.SawMeaningfulEvent = true
			result.Usage = normalizePuterUsage(chunk.Usage)
			if onMessage != nil && len(result.Usage) > 0 {
				event := make(map[string]interface{}, len(result.Usage))
				for key, value := range result.Usage {
					event[key] = value
				}
				onMessage(upstream.SSEMessage{Type: "model.tokens-used", Event: event})
			}
		case "error":
			message := strings.TrimSpace(chunk.Message)
			if message == "" {
				message = line
			}
			return result, fmt.Errorf("puter stream error: %s", message)
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("failed to read puter stream: %w", err)
	}
	return result, nil
}

func normalizePuterStreamLine(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(strings.ToLower(line), "data:") {
		line = strings.TrimSpace(line[5:])
	}
	if line == "[DONE]" {
		return ""
	}
	return line
}

func normalizeStreamToolInput(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "{}"
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return "{}"
		}
		return text
	}
	return trimmed
}

func normalizePuterUsage(raw map[string]interface{}) map[string]int {
	if len(raw) == 0 {
		return nil
	}
	input, hasInput := firstUsageInt(raw, "inputTokens", "input_tokens", "promptTokens", "prompt_tokens")
	output, hasOutput := firstUsageInt(raw, "outputTokens", "output_tokens", "completionTokens", "completion_tokens")
	if !hasInput && !hasOutput {
		return nil
	}
	out := make(map[string]int, 4)
	if hasInput {
		out["inputTokens"] = input
		out["input_tokens"] = input
	}
	if hasOutput {
		out["outputTokens"] = output
		out["output_tokens"] = output
	}
	return out
}

func firstUsageInt(values map[string]interface{}, keys ...string) (int, bool) {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int(typed), true
		case float32:
			return int(typed), true
		case int:
			return typed, true
		case int64:
			return int(typed), true
		case json.Number:
			parsed, err := typed.Int64()
			if err == nil {
				return int(parsed), true
			}
		}
	}
	return 0, false
}
