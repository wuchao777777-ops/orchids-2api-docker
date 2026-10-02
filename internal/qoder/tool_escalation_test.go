package qoder

import (
	"encoding/json"
	"orchids-api/internal/testutil"
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
			testutil.NoError(t, json.Unmarshal([]byte(got), &after))
			_, present := after["justification"]
			testutil.NotEqual(t, present, tc.removed)
			if tc.removed {
				delete(before, "justification")
			}
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(after)
			testutil.Equal(t, string(a), string(b))
		})
	}
	malformed := `{"command":`
	testutil.Equal(t, normalizeCommandEscalation("run_command", malformed), malformed)
}
func TestStreamRepairsCommandEscalationAfterSplitArguments(t *testing.T) {
	body := envelope(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"run_command","arguments":"{\"command\":\"python3 -m speedtest.speedtest --simple\","}}]}}]}`) +
		envelope(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"justification\":\"network test\"}"}}]},"finish_reason":"tool_calls"}]}`) + "event:finish\ndata: {}\n\n"
	events, result, err := collectStream(t, body)
	testutil.NoError(t, err)
	calls := 0
	for _, event := range events {
		if event.Type != "model.tool-call" {
			continue
		}
		calls++
		testutil.Equal(t, event.Event["toolCallId"], "call_1")
		input := event.Event["input"].(string)
		testutil.Falsef(t, strings.Contains(input, "justification") || !strings.Contains(input, "speedtest.speedtest --simple"), "input=%s", input)
	}
	testutil.Equal(t, calls, 1)
	testutil.Equal(t, result.ToolCallCount, 1)
	testutil.Equal(t, result.FinishReason(), "tool_use")
}

func TestTextToolFallbackRepairsOrphanEscalation(t *testing.T) {
	content := `Tool calls: [{"id":"fallback-1","function":{"name":"run_command","arguments":{"command":"ls","justification":"reason"}}}]`
	chunk, _ := json.Marshal(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"delta": map[string]interface{}{"content": content}, "finish_reason": "stop"}}})
	var events []upstream.SSEMessage
	result, err := consumeStreamObserved(strings.NewReader(envelope(string(chunk))+"event:finish\ndata: {}\n\n"), true, func(event upstream.SSEMessage) { events = append(events, event) }, nil)
	testutil.NoError(t, err)
	for _, event := range events {
		if event.Type == "model.tool-call" {
			testutil.MustNotContain(t, event.Event["input"].(string), "justification")
			testutil.Equal(t, result.ToolCallCount, 1)
			return
		}
	}
	t.Fatal("missing fallback tool call")
}
