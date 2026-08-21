package puter

import (
	"fmt"
	"strings"

	"github.com/goccy/go-json"

	"orchids-api/internal/prompt"
)

// convertMessages 将 OpenAI 风格消息转换为 Puter 消息。系统条目逐字透传为独立
// system 消息，其余消息按角色/schema 映射。
func convertMessages(messages []prompt.Message, system []prompt.SystemItem) []Message {
	out := make([]Message, 0, len(messages)+len(system))
	for _, item := range system {
		if text := strings.TrimSpace(item.Text); text != "" {
			out = append(out, Message{Role: "system", Content: item.Text})
		}
	}
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "" {
			role = "user"
		}
		if msg.Content.IsString() {
			if text := msg.Content.GetText(); strings.TrimSpace(text) != "" {
				out = append(out, Message{Role: role, Content: text})
			}
			continue
		}
		switch role {
		case "assistant":
			if converted, ok := convertAssistantMessage(msg.Content.GetBlocks()); ok {
				out = append(out, converted)
			}
		case "user", "tool":
			out = append(out, convertUserBlocks(msg.Content.GetBlocks())...)
		default:
			if text := joinTextBlocks(msg.Content.GetBlocks()); text != "" {
				out = append(out, Message{Role: role, Content: text})
			}
		}
	}
	return out
}

func convertAssistantMessage(blocks []prompt.ContentBlock) (Message, bool) {
	message := Message{Role: "assistant"}
	var text []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				text = append(text, block.Text)
			}
		case "tool_use":
			name := strings.TrimSpace(block.Name)
			if name == "" {
				continue
			}
			id := strings.TrimSpace(block.ID)
			if id == "" {
				id = newToolCallID()
			}
			message.ToolCalls = append(message.ToolCalls, ToolCall{
				ID:   id,
				Type: "function",
				Function: ToolCallFunction{
					Name:      name,
					Arguments: compactToolInput(block.Input),
				},
			})
		}
	}
	message.Content = strings.Join(text, "\n")
	return message, message.Content != "" || len(message.ToolCalls) > 0
}

func convertUserBlocks(blocks []prompt.ContentBlock) []Message {
	out := make([]Message, 0, len(blocks))
	var text []string
	flushText := func() {
		if len(text) == 0 {
			return
		}
		out = append(out, Message{Role: "user", Content: strings.Join(text, "\n")})
		text = text[:0]
	}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				text = append(text, block.Text)
			}
		case "tool_result":
			flushText()
			toolID := strings.TrimSpace(block.ToolUseID)
			if toolID == "" {
				continue
			}
			out = append(out, Message{
				Role:       "tool",
				ToolCallID: toolID,
				Content:    stringifyToolResult(block.Content),
			})
		}
	}
	flushText()
	return out
}

func joinTextBlocks(blocks []prompt.ContentBlock) string {
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func stringifyToolResult(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []prompt.ContentBlock:
		return joinTextBlocks(typed)
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(raw)
	}
}

func compactToolInput(value interface{}) string {
	if value == nil {
		return "{}"
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return "{}"
	}
	return string(raw)
}

func normalizeToolDefinitions(tools []interface{}) []interface{} {
	out := make([]interface{}, 0, len(tools))
	for _, tool := range tools {
		raw, err := json.Marshal(tool)
		if err != nil {
			continue
		}
		var decoded map[string]interface{}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			continue
		}
		if fn, ok := decoded["function"].(map[string]interface{}); ok {
			if strings.TrimSpace(stringValue(fn["name"])) == "" {
				continue
			}
			decoded["type"] = "function"
			out = append(out, decoded)
			continue
		}
		name := strings.TrimSpace(stringValue(decoded["name"]))
		if name == "" {
			if strings.TrimSpace(stringValue(decoded["type"])) != "" {
				out = append(out, decoded)
			}
			continue
		}
		parameters := decoded["input_schema"]
		if parameters == nil {
			parameters = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		out = append(out, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        name,
				"description": stringValue(decoded["description"]),
				"parameters":  parameters,
			},
		})
	}
	return out
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
// splitMultiToolCalls 把单个 assistant 消息里的多个 tool_calls 拆成多段
// "assistant(单个 tool_call) → tool(对应回应)"。puter 的 DeepSeekProvider
// 会在每个 tool 消息后注入一条 system 消息;若一个 assistant 消息带多个
// tool_calls,注入的 system 会插在 tool 回应之间,而 DeepSeek 的严格校验
// 要求 assistant(tool_calls) 后只能跟 tool 消息直到全部 tool_call_id 被回应,
// 于是报 "insufficient tool messages following tool_calls message"。
// 拆成单 tool_call 序列后,每个 assistant 的回应在注入前就已完整。
func splitMultiToolCalls(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		m := messages[i]
		if m.Role != "assistant" || len(m.ToolCalls) < 2 {
			out = append(out, m)
			continue
		}
		j := i + 1
		results := make([]Message, 0, len(m.ToolCalls))
		for j < len(messages) && messages[j].Role == "tool" {
			results = append(results, messages[j])
			j++
		}
		byID := make(map[string]Message, len(m.ToolCalls))
		for _, r := range results {
			byID[r.ToolCallID] = r
		}
		if len(byID) != len(m.ToolCalls) {
			// 回应不齐时保持原样,不擅自改写。
			out = append(out, m)
			continue
		}
		for k, tc := range m.ToolCalls {
			part := Message{Role: "assistant", ToolCalls: []ToolCall{tc}}
			if k == 0 {
				part.Content = m.Content
			}
			out = append(out, part)
			if res, ok := byID[tc.ID]; ok {
				out = append(out, res)
			}
		}
		i = j - 1
	}
	return out
}
