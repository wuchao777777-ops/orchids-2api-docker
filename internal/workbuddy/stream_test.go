package workbuddy

import (
	"strings"
	"testing"

	"orchids-api/internal/testutil"
	"orchids-api/internal/upstream"
)

// collectEvents runs consumeStream and returns every event it reported.
func collectEvents(body string) ([]upstream.SSEMessage, streamResult, error) {
	var events []upstream.SSEMessage
	result, err := consumeStream(strings.NewReader(body), func(msg upstream.SSEMessage) {
		events = append(events, msg)
	})
	return events, result, err
}

// TestConsumeStreamReportsUnifiedUsage proves the merged path still reports
// usage through the stream and the result, and that both spellings survive.
func TestConsumeStreamReportsUnifiedUsage(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"completion_tokens_details":{"reasoning_tokens":4}}}`,
		`data: [DONE]`,
	}, "\n")
	events, result, err := collectEvents(body)
	if err != nil {
		t.Fatal(err)
	}
	var sawUsage bool
	var sawText bool
	for _, event := range events {
		switch event.Type {
		case "model.tokens-used":
			sawUsage = true
			if event.Event["reasoningTokens"] != 4 || event.Event["reasoning_tokens"] != 4 {
				t.Fatalf("usage event = %+v", event.Event)
			}
		case "model.text-delta":
			sawText = true
			testutil.Equal(t, event.Event["delta"], "hi")
		}
	}
	if !sawUsage || !sawText {
		t.Fatalf("usage=%v text=%v events=%+v", sawUsage, sawText, events)
	}
	if result.Usage["reasoningTokens"] != 4 || result.Usage["reasoning_tokens"] != 4 {
		t.Fatalf("result usage = %+v", result.Usage)
	}
	if !result.SawMeaningfulEvent {
		t.Fatal("stream with content and usage was not marked meaningful")
	}
}
