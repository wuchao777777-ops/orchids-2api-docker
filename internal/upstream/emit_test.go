package upstream

import (
	"testing"

	"orchids-api/internal/util"
)

// TestSharedEmittersMatchEveryCallersShape pins the three stream readers onto
// one implementation: the event they emit, and the counters they move, are the
// same values each package produced before the merge.
func TestSharedEmittersMatchEveryCallersShape(t *testing.T) {
	t.Parallel()

	var events []SSEMessage
	saw := false
	EmitTextDelta(func(msg SSEMessage) { events = append(events, msg) }, "", &saw)
	if saw || len(events) != 0 {
		t.Fatalf("empty delta reported: saw=%v events=%v", saw, events)
	}
	EmitTextDelta(func(msg SSEMessage) { events = append(events, msg) }, "hi", &saw)
	if !saw || len(events) != 1 || events[0].Type != "model.text-delta" || events[0].Event["delta"] != "hi" {
		t.Fatalf("text delta = %v", events)
	}

	// A nil callback must still count: the counter decides whether the attempt
	// was meaningful, which is independent of whether anyone is listening.
	emitted := false
	toolCount := 0
	accumulator := util.NewToolCallAccumulator()
	accumulator.Add(0, "", "Bash", `{"command":"ls"}`)
	EmitToolCalls(nil, accumulator.CompleteAll(), &emitted, &toolCount)
	if !emitted || toolCount != 1 {
		t.Fatalf("tool emit with no listener: saw=%v count=%d", emitted, toolCount)
	}

	events = nil
	accumulator = util.NewToolCallAccumulator()
	accumulator.Add(0, "call-1", "Read", `{"path":"/tmp/a"}`)
	accumulator.Add(1, "", "Glob", `{"pattern":"*"}`)
	EmitToolCalls(func(msg SSEMessage) { events = append(events, msg) }, accumulator.CompleteAll(), &emitted, &toolCount)
	if len(events) != 2 || toolCount != 3 {
		t.Fatalf("tool emits = %+v count=%d", events, toolCount)
	}
	for _, event := range events {
		if event.Type != "model.tool-call" {
			t.Fatalf("event type = %q", event.Type)
		}
		if _, ok := event.Event["toolCallId"].(string); !ok || event.Event["toolCallId"] == "" {
			t.Fatalf("tool call without an id: %+v", event.Event)
		}
	}
	if events[0].Event["toolName"] != "Read" || events[1].Event["toolName"] != "Glob" {
		t.Fatalf("tool names = %v / %v", events[0].Event["toolName"], events[1].Event["toolName"])
	}

	// Draining is what makes the finish-time flush and the end-of-stream flush
	// both safe on the same accumulator.
	events = nil
	EmitToolCalls(func(msg SSEMessage) { events = append(events, msg) }, accumulator.CompleteAll(), &emitted, &toolCount)
	if len(events) != 0 {
		t.Fatalf("a drained accumulator emitted again: %+v", events)
	}

	usage := map[string]interface{}{"inputTokens": 1}
	stored := map[string]interface{}{}
	sawUsage := false
	ApplyStreamUsage(nil, nil, &sawUsage, &stored)
	if sawUsage || len(stored) != 0 {
		t.Fatalf("empty usage applied: saw=%v stored=%v", sawUsage, stored)
	}
	events = nil
	ApplyStreamUsage(func(msg SSEMessage) { events = append(events, msg) }, usage, &sawUsage, &stored)
	if !sawUsage || stored["inputTokens"] != 1 {
		t.Fatalf("usage not stored: saw=%v stored=%v", sawUsage, stored)
	}
	if len(events) != 1 || events[0].Type != "model.tokens-used" {
		t.Fatalf("usage event = %+v", events)
	}
}

// TestNormalizeUsageMapUnifiesClineAndWorkBuddyAliases pins the merged
// normalizer: the reasoning count is found under every alias either channel
// accepted, and both spellings of each key are emitted because the handler
// reads either one.
func TestNormalizeUsageMapUnifiesClineAndWorkBuddyAliases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  map[string]interface{}
	}{
		{"top level snake", map[string]interface{}{"prompt_tokens": 5.0, "completion_tokens": 2.0, "reasoning_tokens": 4.0}},
		{"top level camel", map[string]interface{}{"promptTokens": 5.0, "completionTokens": 2.0, "reasoningTokens": 4.0}},
		{"glm thinking key", map[string]interface{}{"prompt_tokens": 5.0, "completion_tokens": 2.0, "completion_thinking_tokens": 4.0}},
		{"nested completion details", map[string]interface{}{
			"prompt_tokens": 5.0, "completion_tokens": 2.0,
			"completion_tokens_details": map[string]interface{}{"reasoning_tokens": 4.0},
		}},
		{"nested camel details", map[string]interface{}{
			"prompt_tokens": 5.0, "completion_tokens": 2.0,
			"completionTokensDetails": map[string]interface{}{"reasoningTokens": 4.0},
		}},
		{"nested output details", map[string]interface{}{
			"prompt_tokens": 5.0, "completion_tokens": 2.0,
			"output_tokens_details": map[string]interface{}{"reasoning_tokens": 4.0},
		}},
		{"nested thinking alias", map[string]interface{}{
			"prompt_tokens": 5.0, "completion_tokens": 2.0,
			"completion_tokens_details": map[string]interface{}{"thinking_tokens": 4.0},
		}},
	}
	for _, tc := range cases {
		got := NormalizeUsageMap(tc.raw)
		if got["reasoningTokens"] != 4 || got["reasoning_tokens"] != 4 {
			t.Fatalf("%s: reasoning = %v / %v, want 4", tc.name, got["reasoningTokens"], got["reasoning_tokens"])
		}
		if got["inputTokens"] != 5 || got["input_tokens"] != 5 || got["outputTokens"] != 2 || got["output_tokens"] != 2 {
			t.Fatalf("%s: base usage = %+v", tc.name, got)
		}
	}

	if got := NormalizeUsageMap(nil); got != nil {
		t.Fatalf("nil usage = %+v, want nil", got)
	}
	// A usage object with neither input nor output is not a usage report.
	if got := NormalizeUsageMap(map[string]interface{}{"total_tokens": 9.0}); got != nil {
		t.Fatalf("usage without input/output = %+v, want nil", got)
	}
	if got := NormalizeUsageMap(map[string]interface{}{"prompt_tokens": 3.0, "prompt_cache_hit_tokens": 2.0}); got["cacheReadTokens"] != 2 || got["cache_read_tokens"] != 2 {
		t.Fatalf("cache read = %+v", got)
	}
}
