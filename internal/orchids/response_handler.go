package orchids

import (
	"strconv"
	"strings"

	"github.com/goccy/go-json"

	"orchids-api/internal/upstream"
)

type orchidsToolCall struct {
	id    string
	name  string
	input string
}

type orchidsResponseOutput struct {
	Type      string
	CallID    string
	ID        string
	Name      string
	Arguments string
	Input     interface{}
}

func fallbackOrchidsToolCallID(toolName, toolInput string) string {
	name := strings.ToLower(strings.TrimSpace(toolName))
	if name == "" {
		return ""
	}
	input := strings.TrimSpace(toolInput)
	if input == "" {
		input = "{}"
	}
	sum := fnv1a64Pair(name, input)
	out := make([]byte, 0, len("orchids_anon_")+16)
	out = append(out, "orchids_anon_"...)
	out = strconv.AppendUint(out, sum, 16)
	return string(out)
}

func fnv1a64Pair(a, b string) uint64 {
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	h := offset
	for i := 0; i < len(a); i++ {
		h ^= uint64(a[i])
		h *= prime
	}
	h ^= 0
	h *= prime
	for i := 0; i < len(b); i++ {
		h ^= uint64(b[i])
		h *= prime
	}
	return h
}

func extractToolCallsFromFastResponse(msg orchidsFastResponseDone, clientTools []interface{}) []orchidsToolCall {
	if len(msg.Response.Output) == 0 {
		return nil
	}

	outputs := make([]orchidsResponseOutput, 0, len(msg.Response.Output))
	for _, item := range msg.Response.Output {
		outputs = append(outputs, orchidsResponseOutput{
			Type:      item.Type,
			CallID:    item.CallID,
			ID:        item.ID,
			Name:      item.Name,
			Arguments: item.Arguments,
			Input:     item.Input,
		})
	}
	return extractToolCallsFromOutputs(outputs, clientTools)
}

func extractToolCallsFromResponse(msg map[string]interface{}, clientTools []interface{}) []orchidsToolCall {
	resp, ok := msg["response"].(map[string]interface{})
	if !ok {
		return nil
	}
	output, ok := resp["output"].([]interface{})
	if !ok {
		return nil
	}

	outputs := make([]orchidsResponseOutput, 0, len(output))
	for _, item := range output {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		callID, _ := m["callId"].(string)
		id, _ := m["id"].(string)
		name, _ := m["name"].(string)
		arguments, _ := m["arguments"].(string)
		outputs = append(outputs, orchidsResponseOutput{
			Type:      typ,
			CallID:    callID,
			ID:        id,
			Name:      name,
			Arguments: arguments,
			Input:     m["input"],
		})
	}
	return extractToolCallsFromOutputs(outputs, clientTools)
}

func extractToolCallsFromOutputs(outputs []orchidsResponseOutput, clientTools []interface{}) []orchidsToolCall {
	if len(outputs) == 0 {
		return nil
	}

	var calls []orchidsToolCall
	for _, item := range outputs {
		switch item.Type {
		case "function_call":
			clientName := MapToolNameToClient(item.Name, clientTools)
			args := transformToolInputJSON(item.Name, clientName, item.Arguments)
			id := item.CallID
			if id == "" {
				id = fallbackOrchidsToolCallID(clientName, args)
			}
			if id == "" || clientName == "" {
				continue
			}
			calls = append(calls, orchidsToolCall{id: id, name: clientName, input: args})

		case "tool_use":
			clientName := MapToolNameToClient(item.Name, clientTools)
			if clientName == "" {
				continue
			}
			inputStr := marshalOrchidsToolInput(item.Input)
			if inputStr == "" && strings.TrimSpace(item.Arguments) != "" {
				inputStr = strings.TrimSpace(item.Arguments)
			}
			inputStr = transformToolInputJSON(item.Name, clientName, inputStr)
			id := item.ID
			if id == "" {
				id = fallbackOrchidsToolCallID(clientName, inputStr)
			}
			if id == "" {
				continue
			}
			calls = append(calls, orchidsToolCall{id: id, name: clientName, input: inputStr})
		}
	}
	if len(calls) == 0 {
		return nil
	}
	return calls
}

func marshalOrchidsToolInput(input interface{}) string {
	if input == nil {
		return ""
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	return string(raw)
}

func handleOrchidsFastCompletion(
	msg orchidsFastResponseDone,
	state *requestState,
	onMessage func(upstream.SSEMessage),
	clientTools []interface{},
) bool {
	emitOrchidsUsageEvent(msg.Response.Usage, onMessage)
	toolCalls := extractToolCallsFromFastResponse(msg, clientTools)
	emitOrchidsToolCalls(toolCalls, state, onMessage)
	emitOrchidsCompletionTail(state, onMessage)
	return true
}

func handleOrchidsCompletionMessage(
	msgType string,
	msg map[string]interface{},
	state *requestState,
	onMessage func(upstream.SSEMessage),
	clientTools []interface{},
) bool {
	if msgType == EventResponseDone {
		if usage, ok := msg["response"].(map[string]interface{}); ok {
			if u, ok := usage["usage"].(map[string]interface{}); ok {
				emitOrchidsUsageMapEvent(u, onMessage)
			}
		}
		toolCalls := extractToolCallsFromResponse(msg, clientTools)
		emitOrchidsToolCalls(toolCalls, state, onMessage)
	}
	emitOrchidsCompletionTail(state, onMessage)
	return true
}
