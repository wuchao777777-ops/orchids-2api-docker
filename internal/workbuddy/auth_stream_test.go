package workbuddy

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"orchids-api/internal/testutil"
	"orchids-api/internal/upstream"
)

func TestConsumeStream_EmitsTextReasoningAndToolCalls(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		`data: {"id":"cmb-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":"think"},"finish_reason":""}]}`,
		`data: {"choices":[{"index":0,"delta":{"content":"hello "},"finish_reason":""}]}`,
		`data: {"choices":[{"index":0,"delta":{"content":"world"},"finish_reason":""}],"usage":null}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"list_files","arguments":"{\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"completion_thinking_tokens":2,"prompt_cache_hit_tokens":3}}`,
		`data: [DONE]`,
	}, "\n")

	var events []upstream.SSEMessage
	result, err := consumeStream(strings.NewReader(body), func(msg upstream.SSEMessage) {
		events = append(events, msg)
	})
	testutil.NoError(t, err, "consumeStream() error = %v")
	testutil.False(t, !result.SawMeaningfulEvent, "SawMeaningfulEvent = false")
	testutil.Equal(t, result.ToolCallCount, 1)
	testutil.Equal(t, result.FinishReason(), "tool_use")
	testutil.Equal(t, result.Usage["inputTokens"], 11)
	testutil.Equal(t, result.Usage["outputTokens"], 7)

	var text, reasoning, toolName, toolInput string
	for _, event := range events {
		switch event.Type {
		case "model.text-delta":
			text += event.Event["delta"].(string)
		case "model.reasoning-delta":
			reasoning += event.Event["delta"].(string)
		case "model.tool-call":
			toolName, _ = event.Event["toolName"].(string)
			toolInput, _ = event.Event["input"].(string)
		}
	}
	testutil.Equal(t, text, "hello world")
	testutil.Equal(t, reasoning, "think")
	testutil.Equal(t, toolName, "list_files")
	testutil.Equal(t, toolInput, `{"path":"."}`)
}

func TestConsumeStream_ReassemblesSplitToolArguments(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_split","type":"function","function":{"name":"write_file","arguments":"{\"path\":"}}]},"finish_reason":""}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"notes.txt\",\"content\":\"ok\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n")

	var calls []upstream.SSEMessage
	result, err := consumeStream(strings.NewReader(body), func(msg upstream.SSEMessage) {
		if msg.Type == "model.tool-call" {
			calls = append(calls, msg)
		}
	})
	testutil.NoError(t, err, "consumeStream() error = %v")
	testutil.Falsef(t, result.ToolCallCount != 1 || len(calls) != 1, "tool calls = %d/%d, want exactly one", result.ToolCallCount, len(calls))
	testutil.Equal(t, calls[0].Event["input"], `{"path":"notes.txt","content":"ok"}`)
}

func TestConsumeStream_DoesNotMergeReusedToolIndex(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"first","arguments":"{}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_b","function":{"name":"second","arguments":"{\"n\":2}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n")
	var calls []upstream.SSEMessage
	result, err := consumeStream(strings.NewReader(body), func(message upstream.SSEMessage) {
		if message.Type == "model.tool-call" {
			calls = append(calls, message)
		}
	})
	testutil.NoError(t, err)
	testutil.Equal(t, result.ToolCallCount, 2)
	testutil.Equal(t, len(calls), 2)
	if calls[0].Event["toolName"] != "first" || calls[0].Event["input"] != "{}" ||
		calls[1].Event["toolName"] != "second" || calls[1].Event["input"] != `{"n":2}` {
		t.Fatalf("reused index calls were corrupted: %#v", calls)
	}
}

func TestConsumeStream_PreservesBusinessEnvelopeAndNestedReasoningUsage(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"completion_tokens_details":{"reasoning_tokens":4}}}`,
		`data: [DONE]`,
	}, "\n")
	result, err := consumeStream(strings.NewReader(body), nil)
	testutil.NoError(t, err)
	testutil.Equal(t, result.Usage["reasoningTokens"], 4)

	for _, code := range []int{CodeModelThrottle, CodeSessionDead} {
		_, err := consumeStream(strings.NewReader(fmt.Sprintf("data: {\"code\":%d,\"msg\":\"business failure\"}\n", code)), nil)
		var typed *APIError
		testutil.Falsef(t, !errors.As(err, &typed) || typed.Code != code || typed.HTTPStatus != http.StatusOK, "code %d error=%#v want typed HTTP-200 business error", code, err)
	}
}
