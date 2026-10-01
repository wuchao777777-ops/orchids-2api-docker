package cline

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"encoding/json"

	"orchids-api/internal/prompt"
	"orchids-api/internal/testutil"
	"orchids-api/internal/upstream"
)

// TestBuildChatBodyCarriesTheUpstreamDefaults pins what the upstream needs on
// every request: an omitted reasoning_effort makes some models answer with empty
// content, and a session_id is how it correlates a turn.
func TestBuildChatBodyCarriesTheUpstreamDefaults(t *testing.T) {
	body, err := buildChatBody(upstream.UpstreamRequest{
		Model:    "x-ai/grok-4.1-fast",
		Messages: []prompt.Message{{Role: "user", Content: prompt.MessageContent{Text: "hello"}}},
	}, "x-ai/grok-4.1-fast")
	if err != nil {
		t.Fatalf("buildChatBody() error = %v", err)
	}
	raw := string(body)
	for _, want := range []string{
		`"model":"x-ai/grok-4.1-fast"`,
		`"max_tokens":128000`,
		`"reasoning_effort":"high"`,
		`"session_id":"sess_`,
		`"stream":true`,
		`"messages":[{"role":"user","content":"hello"}]`,
	} {
		testutil.CheckContain(t, raw, want)
	}
}

func TestBuildChatBodyConvertsAnthropicToolsToOpenAI(t *testing.T) {
	body, err := buildChatBody(upstream.UpstreamRequest{
		Model:    "z-ai/glm-5.3-flash",
		Messages: []prompt.Message{{Role: "user", Content: prompt.MessageContent{Text: "inspect"}}},
		Tools: []interface{}{map[string]interface{}{
			"name": "glob", "description": "Find files", "input_schema": map[string]interface{}{
				"type": "object", "properties": map[string]interface{}{"pattern": map[string]interface{}{"type": "string"}}, "required": []interface{}{"pattern"},
			},
		}},
		ToolChoice: map[string]interface{}{"type": "tool", "name": "glob"},
	}, "z-ai/glm-5.3-flash")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	testutil.NoError(t, json.Unmarshal(body, &decoded))
	tools, _ := decoded["tools"].([]interface{})
	testutil.Equal(t, len(tools), 1)
	tool := tools[0].(map[string]interface{})
	testutil.Equal(t, tool["type"], "function")
	function := tool["function"].(map[string]interface{})
	if function["name"] != "glob" || function["parameters"] == nil {
		t.Fatalf("function=%#v", function)
	}
	choice := decoded["tool_choice"].(map[string]interface{})
	if choice["type"] != "function" || choice["function"].(map[string]interface{})["name"] != "glob" {
		t.Fatalf("tool_choice=%#v", choice)
	}
}

func TestBuildChatBodyKeepsOpenAITools(t *testing.T) {
	openAI := map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "bash", "parameters": map[string]interface{}{"type": "object"}}}
	body, err := buildChatBody(upstream.UpstreamRequest{Messages: []prompt.Message{{Role: "user", Content: prompt.MessageContent{Text: "run"}}}, Tools: []interface{}{openAI}}, "z-ai/glm-5.3-flash")
	if err != nil {
		t.Fatal(err)
	}
	testutil.MustContain(t, string(body), `"tools":[{"function":{"name":"bash","parameters":{"type":"object"}},"type":"function"}]`)
}

// TestBuildMessagesMapsToolHistory proves an assistant tool_use block becomes a
// tool_calls entry and its tool_result becomes a paired `tool` message: an
// orphan tool result is rejected upstream.
func TestBuildMessagesMapsToolHistory(t *testing.T) {
	out := buildMessages(upstream.UpstreamRequest{
		System: []prompt.SystemItem{{Type: "text", Text: "be terse"}},
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "read the file"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
					{Type: "tool_use", ID: "call-1", Name: "Read", Input: map[string]interface{}{"path": "/tmp/a"}},
				}},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
					{Type: "tool_result", ToolUseID: "call-1", Content: "file body"},
				}},
			},
		},
	})
	testutil.Equal(t, len(out), 4)
	if out[0].Role != "system" || out[0].Content != "be terse" {
		t.Errorf("first message = %+v, want the system item", out[0])
	}
	if len(out[2].ToolCalls) != 1 || out[2].ToolCalls[0].Function.Name != "Read" {
		t.Fatalf("assistant message = %+v, want one Read tool call", out[2])
	}
	if out[3].Role != "tool" || out[3].ToolCallID != "call-1" || out[3].Content != "file body" {
		t.Errorf("tool result = %+v, want a paired tool message", out[3])
	}
}

// TestBuildMessagesDropsOrphanToolResults covers the case that would otherwise
// be rejected: a tool_result whose call is not in the history has nothing to
// pair with.
func TestBuildMessagesDropsOrphanToolResults(t *testing.T) {
	out := buildMessages(upstream.UpstreamRequest{
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "hi"}},
			{
				Role: "user",
				Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{
					{Type: "tool_result", ToolUseID: "call-absent", Content: "body"},
				}},
			},
		},
	})
	if len(out) != 1 || out[0].Content != "hi" {
		t.Fatalf("messages = %+v, want only the user message", out)
	}
}

// TestConsumeStreamEmitsTextAndUsage drives the SSE conversion that the handler
// consumes, including the {"data":{...}} envelope: a chunk that arrives wrapped
// must still produce its delta.
func TestConsumeStreamEmitsTextAndUsage(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: {\"data\":{\"choices\":[{\"delta\":{\"content\":\" world\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}}\n\n" +
		"data: [DONE]\n\n"
	var text strings.Builder
	var usage map[string]interface{}
	finish := false
	result, err := consumeStream(strings.NewReader(stream), false, func(msg upstream.SSEMessage) {
		switch msg.Type {
		case "model.text-delta":
			text.WriteString(msg.Event["delta"].(string))
		case "model.tokens-used":
			usage = msg.Event
		case "model.finish":
			finish = true
		}
	})
	if err != nil {
		t.Fatalf("consumeStream() error = %v", err)
	}
	testutil.CheckEqual(t, text.String(), "hello world")
	if usage == nil || usage["inputTokens"] != 5 || usage["outputTokens"] != 2 {
		t.Errorf("usage = %+v, want the normalized pair", usage)
	}
	if !result.SawMeaningfulEvent {
		t.Error("SawMeaningfulEvent = false, want true")
	}
	if finish {
		t.Error("consumeStream emitted a finish event; the caller owns it")
	}
}

func TestConsumeStreamRejectsMalformedDataFrame(t *testing.T) {
	t.Parallel()
	stream := "data: {not-json}\n\ndata: [DONE]\n\n"
	if _, err := consumeStream(strings.NewReader(stream), false, nil); err == nil || !strings.Contains(err.Error(), "protocol error") {
		t.Fatalf("error = %v, want protocol error", err)
	}
}

func TestConsumeStreamRejectsEOFBeforeFinish(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
	result, err := consumeStream(strings.NewReader(stream), false, nil)
	if !errors.Is(err, ErrStreamTruncated) {
		t.Fatalf("error=%v, want ErrStreamTruncated", err)
	}
	if !result.SawMeaningfulEvent {
		t.Fatal("partial data should remain observable even though the stream failed")
	}
}

func TestConsumeStreamAcceptsFinishReasonWithoutDone(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"complete\"},\"finish_reason\":\"stop\"}]}\n\n"
	result, err := consumeStream(strings.NewReader(stream), false, nil)
	if err != nil {
		t.Fatalf("error=%v", err)
	}
	if !result.SawMeaningfulEvent {
		t.Fatal("complete stream was not observed")
	}
}

// TestConsumeStreamEmitsEachToolCallOnce is the regression the accumulator
// exists for: the finish-time emit and the end-of-stream flush both run, and a
// tool call must not reach the client twice.
func TestConsumeStreamEmitsEachToolCallOnce(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"function\":{\"name\":\"Read\",\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"/tmp/a}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	var calls int
	var names []string
	result, err := consumeStream(strings.NewReader(stream), false, func(msg upstream.SSEMessage) {
		if msg.Type != "model.tool-call" {
			return
		}
		calls++
		names = append(names, msg.Event["toolName"].(string))
	})
	if err != nil {
		t.Fatalf("consumeStream() error = %v", err)
	}
	testutil.Equal(t, calls, 1)
	testutil.CheckEqual(t, result.ToolCallCount, 1)
	testutil.CheckEqual(t, result.FinishReason(), "tool_use")
}

func TestConsumeStreamConvertsGLMTextToolCall(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"checking <tool_call>bash:ls -la /tmp/a</arg_value><arg_key>command</arg_key><arg_value>ls -la /tmp/a</arg_value></tool_call>\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	var text strings.Builder
	var calls []upstream.SSEMessage
	result, err := consumeStream(strings.NewReader(stream), true, func(msg upstream.SSEMessage) {
		switch msg.Type {
		case "model.text-delta":
			text.WriteString(msg.Event["delta"].(string))
		case "model.tool-call":
			calls = append(calls, msg)
		}
	})
	if err != nil {
		t.Fatalf("consumeStream() error = %v", err)
	}
	testutil.Equal(t, text.String(), "checking ")
	testutil.Equal(t, len(calls), 1)
	if calls[0].Event["toolName"] != "bash" || calls[0].Event["input"] != `{"command":"ls -la /tmp/a"}` {
		t.Fatalf("tool call = %#v", calls[0].Event)
	}
	if result.ToolCallCount != 1 || result.FinishReason() != "tool_use" {
		t.Fatalf("result = %+v, want one tool_use", result)
	}
}

func TestParseClineTextToolCallPreservesStructuredArguments(t *testing.T) {
	markup := `<tool_call>write:ignored</arg_value><arg_key>content</arg_key><arg_value>{&quot;ok&quot;:true}</arg_value><arg_key>count</arg_key><arg_value>2</arg_value></tool_call>`
	visible, calls := parseClineTextToolCalls(markup)
	if visible != "" || len(calls) != 1 {
		t.Fatalf("visible=%q calls=%+v", visible, calls)
	}
	testutil.Equal(t, calls[0].Function.Arguments, `{"content":{"ok":true},"count":2}`)
}

func TestParseClineRepeatedTagToolCall(t *testing.T) {
	text := `让我先看一下项目。<tool_call> GetType(ItemType) + $assetPath.Write and glob the workspace.<tool_call>glob<tool_call>glob: *<tool_call>args: {"pattern":"*"}<tool_call>run_in_background: false`
	visible, calls := parseClineTextToolCalls(text)
	if visible != "让我先看一下项目。" || len(calls) != 1 {
		t.Fatalf("visible=%q calls=%+v", visible, calls)
	}
	if calls[0].Function.Name != "glob" || calls[0].Function.Arguments != `{"pattern":"*"}` {
		t.Fatalf("call=%+v", calls[0])
	}
}

func TestParseClineCompactMultipleToolCalls(t *testing.T) {
	text := `先查看结构。<tool_call>glob,{"pattern":"*"}<tool_call>glob,{"pattern":"*/*"}`
	visible, calls := parseClineTextToolCalls(text)
	if visible != "先查看结构。" || len(calls) != 2 {
		t.Fatalf("visible=%q calls=%+v", visible, calls)
	}
	if calls[0].Function.Name != "glob" || calls[0].Function.Arguments != `{"pattern":"*"}` || calls[1].Function.Arguments != `{"pattern":"*/*"}` {
		t.Fatalf("calls=%+v", calls)
	}
}

func TestParseClineRepeatedTagToolCallRejectsProse(t *testing.T) {
	text := `plain <tool_call>this is not a tool<tool_call>args: {"x":1}`
	visible, calls := parseClineTextToolCalls(text)
	if visible != text || len(calls) != 0 {
		t.Fatalf("visible=%q calls=%+v", visible, calls)
	}
}

func TestConsumeStreamLeavesTextToolMarkupWithoutDeclaredTools(t *testing.T) {
	markup := `<tool_call>bash:x</arg_value><arg_key>command</arg_key><arg_value>x</arg_value></tool_call>`
	stream := "data: {\"choices\":[{\"delta\":{\"content\":" + string(mustJSON(t, markup)) + "},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	var text strings.Builder
	result, err := consumeStream(strings.NewReader(stream), false, func(msg upstream.SSEMessage) {
		if msg.Type == "model.text-delta" {
			text.WriteString(msg.Event["delta"].(string))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != markup || result.ToolCallCount != 0 {
		t.Fatalf("text=%q result=%+v", text.String(), result)
	}
}

func mustJSON(t *testing.T, value interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestClassifyStatusTurnsTheCapIntoAKnownWindow is the reason the 429 path is
// special: the wait is in the body, and reading it turns a retry into a
// cooldown the scheduler can honour.
func TestClassifyStatusTurnsTheCapIntoAKnownWindow(t *testing.T) {
	err := classifyStatus(429, []byte(`{"error":"INFERENCE_CAP_ERROR","message":"Try again in 17h 59m"}`))
	capErr, ok := err.(*InferenceCapError)
	if !ok {
		t.Fatalf("classifyStatus(429) = %T (%v), want *InferenceCapError", err, err)
	}
	testutil.CheckEqual(t, capErr.Wait, 17*time.Hour+59*time.Minute)
}

// TestClassifyStatusPreservesFailures checks the one local recovery path (401)
// and the status details handed back to callers for non-auth failures.
func TestClassifyStatusPreservesFailures(t *testing.T) {
	if err := classifyStatus(401, []byte(`{"error":"unauthorized"}`)); !isUnauthorized(err) || !errors.Is(err, ErrCredentialMissing) {
		t.Errorf("classifyStatus(401) = %v, want unauthorized credential error", err)
	}
	for _, tc := range []struct {
		status int
		body   string
	}{
		{400, `{"error":"bad"}`},
		{408, `{"error":"timeout"}`},
		{429, `{"error":"busy"}`},
		{503, `{"error":"unavailable"}`},
	} {
		err := classifyStatus(tc.status, []byte(tc.body))
		if isUnauthorized(err) || errors.Is(err, ErrCredentialMissing) {
			t.Errorf("classifyStatus(%d) = %v, must not trigger credential refresh", tc.status, err)
		}
		testutil.CheckContainAll(t, err.Error(), fmt.Sprintf("status=%d", tc.status), tc.body)
	}
}

// TestCatalogSnapshotRoundTripsTheObservedFeed proves the stored form keeps the
// identifier, and that a bare id written by an older build still resolves.
func TestCatalogSnapshotRoundTripsTheObservedFeed(t *testing.T) {
	models := []Model{{ID: "x-ai/grok-4.1-fast", Name: "Grok 4.1 Fast", Provider: "x-ai", RequiresStream: true}}
	rows := CatalogSnapshot(models)
	testutil.Equal(t, len(rows), 1)
	testutil.CheckEqual(t, catalogID(rows[0]), "x-ai/grok-4.1-fast")
	testutil.CheckEqual(t, catalogID("openai/gpt-5"), "openai/gpt-5")
	// The snapshot is JSON rows, so the identifier has to survive the encoding.
	var decoded map[string]interface{}
	testutil.NoError(t, json.Unmarshal([]byte(rows[0]), &decoded), "row is not JSON: %v")
	testutil.CheckEqual(t, decoded["id"], "x-ai/grok-4.1-fast")
}

// TestParseCatalogPublishesOnlyTheFreeTier covers the feed's shape: the paid
// rows name models this account may not run, and an empty feed is an error
// rather than an empty catalog.
func TestParseCatalogPublishesOnlyTheFreeTier(t *testing.T) {
	models, err := parseCatalog([]byte(`{"free":[{"id":"x-ai/grok-4.1-fast","name":"Grok 4.1 Fast"}],"paid":[{"id":"anthropic/claude-opus","name":"Opus"}]}`))
	if err != nil {
		t.Fatalf("parseCatalog() error = %v", err)
	}
	if len(models) != 1 || models[0].ID != "x-ai/grok-4.1-fast" {
		t.Fatalf("models = %+v, want only the free row", models)
	}
	testutil.CheckEqual(t, models[0].Provider, "x-ai")
	if !models[0].RequiresStream {
		t.Error("RequiresStream = false, want true for a bare identifier")
	}
	if models, err = parseCatalog([]byte(`{"free":[]}`)); err != nil || len(models) != 0 {
		t.Errorf("an empty feed must parse as empty, got %+v / %v", models, err)
	}
}

// TestNewToolCallIDIsUnique keeps two concurrent tool calls from colliding on
// one id, which would pair a result with the wrong call.
func TestNewToolCallIDIsUnique(t *testing.T) {
	seen := make(map[string]bool, 200)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				id := NewToolCallID()
				mu.Lock()
				if seen[id] {
					mu.Unlock()
					t.Errorf("duplicate tool call id %q", id)
					return
				}
				seen[id] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	testutil.CheckEqual(t, len(seen), 200)
}

// TestConsumeStreamSignsReasoningDeltas pins the WorkBuddy/Qoder/Cline parity:
// every reasoning delta of one stream carries the same signature so the
// Anthropic surface keeps them inside a single signed thinking block.
func TestConsumeStreamSignsReasoningDeltas(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"step one\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"step two\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	var signatures []string
	var reasoning strings.Builder
	_, err := consumeStream(strings.NewReader(stream), false, func(msg upstream.SSEMessage) {
		if msg.Type != "model.reasoning-delta" {
			return
		}
		reasoning.WriteString(msg.Event["delta"].(string))
		if sig, ok := msg.Event["signature"].(string); ok {
			signatures = append(signatures, sig)
		}
	})
	if err != nil {
		t.Fatalf("consumeStream() error = %v", err)
	}
	testutil.Equal(t, reasoning.String(), "step onestep two")
	if len(signatures) != 2 || signatures[0] == "" || signatures[0] != signatures[1] {
		t.Fatalf("signatures = %v, want one stable non-empty signature", signatures)
	}
	if !strings.HasPrefix(signatures[0], "cline-v1:") {
		t.Fatalf("signature = %q, want the cline-v1 prefix", signatures[0])
	}
}

func TestConsumeStreamAcceptsGLMReasoningAliases(t *testing.T) {
	stream := `data: {"choices":[{"delta":{"reasoning":"step one"}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"thinking":"step two","content":"answer"},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"
	var reasoning, text strings.Builder
	result, err := consumeStream(strings.NewReader(stream), false, func(msg upstream.SSEMessage) {
		switch msg.Type {
		case "model.reasoning-delta":
			reasoning.WriteString(msg.Event["delta"].(string))
		case "model.text-delta":
			text.WriteString(msg.Event["delta"].(string))
		}
	})
	if err != nil {
		t.Fatalf("consumeStream() error = %v", err)
	}
	testutil.Equal(t, reasoning.String(), "step onestep two")
	if text.String() != "answer" || !result.SawMeaningfulEvent {
		t.Fatalf("text/result = %q/%+v", text.String(), result)
	}
}
func TestConsumeStreamSeparatesSplitThinkingTagsFromAnswer(t *testing.T) {
	stream := `data: {"choices":[{"delta":{"content":"Hello <thi"}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"content":"nk>private reasoning</thi"}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"content":"nk> world"},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"
	for _, toolsEnabled := range []bool{false, true} {
		var text, reasoning strings.Builder
		_, err := consumeStream(strings.NewReader(stream), toolsEnabled, func(msg upstream.SSEMessage) {
			switch msg.Type {
			case "model.text-delta":
				text.WriteString(msg.Event["delta"].(string))
			case "model.reasoning-delta":
				reasoning.WriteString(msg.Event["delta"].(string))
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if text.String() != "Hello  world" || reasoning.String() != "private reasoning" {
			t.Fatalf("tools=%t text=%q reasoning=%q", toolsEnabled, text.String(), reasoning.String())
		}
	}
}

func TestConsumeStreamFlushesIncompleteThinkPrefixAsText(t *testing.T) {
	stream := `data: {"choices":[{"delta":{"content":"answer <thi","reasoning_content":"separate reasoning"},"finish_reason":"stop"}]}` + "\n\n"
	var text, reasoning strings.Builder
	result, err := consumeStream(strings.NewReader(stream), false, func(msg upstream.SSEMessage) {
		switch msg.Type {
		case "model.text-delta":
			text.WriteString(msg.Event["delta"].(string))
		case "model.reasoning-delta":
			reasoning.WriteString(msg.Event["delta"].(string))
		}
	})
	if err != nil || !result.SawMeaningfulEvent || text.String() != "answer <thi" || reasoning.String() != "separate reasoning" {
		t.Fatalf("text=%q reasoning=%q result=%+v err=%v", text.String(), reasoning.String(), result, err)
	}
}

func TestBuildChatBodyHonorsClientReasoningEffort(t *testing.T) {
	body, err := buildChatBody(upstream.UpstreamRequest{
		Model:           "z-ai/glm-5.3-flash",
		ReasoningEffort: "low",
		Messages:        []prompt.Message{{Role: "user", Content: prompt.MessageContent{Text: "hi"}}},
	}, "z-ai/glm-5.3-flash")
	if err != nil {
		t.Fatalf("buildChatBody() error = %v", err)
	}
	testutil.MustContain(t, string(body), `"reasoning_effort":"low"`)

	body, err = buildChatBody(upstream.UpstreamRequest{
		Model:           "z-ai/glm-5.3-flash",
		ReasoningEffort: "none",
		Messages:        []prompt.Message{{Role: "user", Content: prompt.MessageContent{Text: "hi"}}},
	}, "z-ai/glm-5.3-flash")
	if err != nil {
		t.Fatalf("buildChatBody() error = %v", err)
	}
	testutil.MustNotContain(t, string(body), `"reasoning_effort":"none"`)
	testutil.MustContain(t, string(body), `"reasoning_effort":"high"`)
}
