package qoder

import (
	"encoding/json"
	"orchids-api/internal/prompt"
	"orchids-api/internal/upstream"
	"strings"
	"testing"
)

// Reconstructed from the observed dump fields; no credentials or private logs.
func TestCaptureUsageAndTimings(t *testing.T) {
	body := envelope(`{"choices":[{"index":0,"delta":{"content":"测试123123"},"finish_reason":"stop"}],"usage":{"prompt_tokens":27260,"completion_tokens":48,"total_tokens":27308,"billable":false,"credits":0.4885985714285714,"prompt_tokens_details":{"cacheable_tokens":27254,"cached_tokens":0}}}`) + "event:finish\ndata:{\"firstTokenDuration\":1556,\"totalDuration\":2577,\"serverDuration\":89}\n\n"
	var text strings.Builder
	r, e := consumeStreamWithTools(strings.NewReader(body), true, func(m upstream.SSEMessage) {
		if m.Type == "model.text-delta" {
			if v, ok := m.Event["delta"].(string); ok {
				text.WriteString(v)
			}
		}
	})
	if e != nil {
		t.Fatal(e)
	}
	if text.String() != "测试123123" || r.ToolCallCount != 0 {
		t.Fatalf("text=%q calls=%d", text.String(), r.ToolCallCount)
	}
	if r.Usage["billable"] != false || r.Usage["firstTokenDuration"] != int64(1556) || r.Usage["cacheable_tokens"] != 27254 {
		t.Fatalf("metadata lost: %v", r.Usage)
	}
	if _, ok := r.Usage["cacheWriteTokens"]; ok {
		t.Fatal("cacheable tokens counted as writes")
	}
}
func TestReasoningItemSurvivesToolHistory(t *testing.T) {
	var msg prompt.Message
	if e := json.Unmarshal([]byte(`{"role":"assistant","content":"","reasoning_item":{"opaque":"signature"},"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{}"}}]}`), &msg); e != nil {
		t.Fatal(e)
	}
	b, e := buildChatBody(upstream.UpstreamRequest{Messages: []prompt.Message{msg}}, modelEntry{Key: "qfmodel"}, "s", "r", "set")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := decodeBody(b)
	if e != nil {
		t.Fatal(e)
	}
	var wire chatBody
	if e = json.Unmarshal(raw, &wire); e != nil {
		t.Fatal(e)
	}
	if len(wire.Messages) != 1 || string(wire.Messages[0].ReasoningItem) != `{"opaque":"signature"}` || len(wire.Messages[0].ToolCalls) != 1 {
		t.Fatalf("history lost: %s", raw)
	}
}

func TestReasoningOnlyHistoryIsNotDropped(t *testing.T) {
	var msg prompt.Message
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":"","reasoning_item":{"opaque":"signature"}}`), &msg); err != nil {
		t.Fatal(err)
	}
	messages, _, err := buildMessages(upstream.UpstreamRequest{Messages: []prompt.Message{msg}})
	if err != nil || len(messages) != 1 || len(messages[0].ReasoningItem) == 0 {
		t.Fatalf("reasoning-only history lost: %#v %v", messages, err)
	}
}
