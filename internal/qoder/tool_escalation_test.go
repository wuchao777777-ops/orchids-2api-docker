package qoder

import (
	"encoding/json"
	"orchids-api/internal/upstream"
	"strings"
	"testing"
)

func TestCommandEscalationCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		removed     bool
	}{
		{"run_command", `{"command":"python3 -m speedtest.speedtest --simple","description":"speedtest","justification":"network test"}`, true},
		{"functions.exec_command", `{"cmd":"ls","justification":"reason","sandbox_permissions":null}`, true},
		{"mcp__terminal__run_command", `{"command":"ls","justification":"reason","sandbox_permissions":""}`, true},
		{"Bash", `{"command":"ls","justification":"reason","sandbox_permissions":"require_escalated"}`, false},
		{"run_command", `{"command":"ls","justification":"reason","sandbox_permissions":"use_default"}`, false},
		{"run_command", `{"command":"ls","justification":"reason","sandbox_permissions":42}`, false},
		{"submit_review", `{"command":"ls","justification":"review reason"}`, false},
		{"run_command", `{"path":"file","justification":"reason"}`, false},
	} {
		t.Run(tc.name+tc.input, func(t *testing.T) {
			got := normalizeCommandEscalation(tc.name, tc.input)
			var before, after map[string]json.RawMessage
			json.Unmarshal([]byte(tc.input), &before)
			if err := json.Unmarshal([]byte(got), &after); err != nil {
				t.Fatal(err)
			}
			_, present := after["justification"]
			if present == tc.removed {
				t.Fatalf("unexpected justification: %s", got)
			}
			if tc.removed {
				delete(before, "justification")
			}
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(after)
			if string(a) != string(b) {
				t.Fatalf("other arguments changed: %s vs %s", a, b)
			}
		})
	}
	malformed := `{"command":`
	if got := normalizeCommandEscalation("run_command", malformed); got != malformed {
		t.Fatalf("malformed arguments changed: %s", got)
	}
}
func TestStreamRepairsCommandEscalationAfterSplitArguments(t *testing.T) {
	body := envelope(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"run_command","arguments":"{\"command\":\"python3 -m speedtest.speedtest --simple\","}}]}}]}`) +
		envelope(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"justification\":\"network test\"}"}}]},"finish_reason":"tool_calls"}]}`) + "event:finish\ndata: {}\n\n"
	events, result, err := collectStream(t, body)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, event := range events {
		if event.Type != "model.tool-call" {
			continue
		}
		calls++
		if event.Event["toolCallId"] != "call_1" {
			t.Fatalf("identity lost: %+v", event)
		}
		input := event.Event["input"].(string)
		if strings.Contains(input, "justification") || !strings.Contains(input, "speedtest.speedtest --simple") {
			t.Fatalf("input=%s", input)
		}
	}
	if calls != 1 || result.ToolCallCount != 1 || result.FinishReason() != "tool_use" {
		t.Fatalf("calls=%d result=%+v", calls, result)
	}
}

func TestTextToolFallbackRepairsOrphanEscalation(t *testing.T) {
	content := `Tool calls: [{"id":"fallback-1","function":{"name":"run_command","arguments":{"command":"ls","justification":"reason"}}}]`
	chunk, _ := json.Marshal(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"delta": map[string]interface{}{"content": content}, "finish_reason": "stop"}}})
	var events []upstream.SSEMessage
	result, err := consumeStreamWithTools(strings.NewReader(envelope(string(chunk))+"event:finish\ndata: {}\n\n"), true, func(event upstream.SSEMessage) { events = append(events, event) })
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "model.tool-call" {
			if strings.Contains(event.Event["input"].(string), "justification") {
				t.Fatalf("fallback not normalized: %+v", event)
			}
			if result.ToolCallCount != 1 {
				t.Fatal(result.ToolCallCount)
			}
			return
		}
	}
	t.Fatal("missing fallback tool call")
}
