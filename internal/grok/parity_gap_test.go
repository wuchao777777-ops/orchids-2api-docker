package grok

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"orchids-api/internal/store"
)

func TestResolveModel_WebAndConsoleConversationCatalogs(t *testing.T) {
	cases := []struct {
		id       string
		upstream UpstreamKind
		model    string
	}{
		{"grok-chat-fast", UpstreamAppChat, "grok-chat-fast"},
		{"Web/grok-chat-heavy", UpstreamAppChat, "grok-chat-heavy"},
		{"Console/grok-4.20-0309-reasoning", UpstreamConsole, "grok-4.20-0309-reasoning"},
		{"console/grok-build-0.1", UpstreamConsole, "grok-build-0.1"},
	}
	for _, tc := range cases {
		spec, ok := ResolveModel(tc.id)
		if !ok || spec.Upstream != tc.upstream {
			t.Fatalf("ResolveModel(%q) = %#v,%v", tc.id, spec, ok)
		}
		actual := firstNonEmpty(spec.ConsoleModel, spec.UpstreamModel)
		if actual != tc.model {
			t.Fatalf("ResolveModel(%q) upstream=%q want %q", tc.id, actual, tc.model)
		}
	}
}

func TestResolveConversationModelUsesAccountBuildCatalog(t *testing.T) {
	h, s, mini := setupValidationHandler(t)
	defer func() {
		_ = s.Close()
		mini.Close()
	}()
	if err := s.CreateAccount(context.Background(), &store.Account{
		Name: "build", AccountType: "grok", GrokProvider: ProviderBuild, CredentialType: "oauth", Enabled: true,
		OAuthAccessToken: "token", GrokModels: []string{"grok-future-account-model"}, GrokModelsSyncedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	spec, ok := h.resolveConversationModel(context.Background(), "grok-future-account-model")
	if !ok || spec.Upstream != UpstreamCLI || spec.UpstreamModel != "grok-future-account-model" {
		t.Fatalf("dynamic spec=%#v,%v", spec, ok)
	}
	if _, ok := h.resolveConversationModel(context.Background(), "arbitrary-unadvertised-model"); ok {
		t.Fatal("unadvertised model must not become an upstream probe")
	}
	if err := s.CreateModel(context.Background(), &store.Model{
		Channel: "grok", ModelID: "grok-future-account-model", Name: "future",
		Status: store.ModelStatusOffline, Verified: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.ensureResolvedModelEnabled(context.Background(), spec.ID, spec); err == nil {
		t.Fatal("disabled dynamic model must remain disabled")
	}
}

func TestResponsesStreamTranslationIsIncremental(t *testing.T) {
	reader, writer := io.Pipe()
	recorder := newObservedStreamWriter("response.output_text.delta")
	done := make(chan struct{})
	go func() {
		writeResponsesStreamFromChatReader(recorder, "grok-chat-fast", reader)
		close(done)
	}()

	_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
	select {
	case <-recorder.observed:
	case <-time.After(time.Second):
		t.Fatal("first Responses delta was buffered until stream completion")
	}
	_ = writer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("translator did not finish")
	}
}

func TestResponsesStreamAggregatesFragmentedToolArguments(t *testing.T) {
	raw := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"x\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	recorder := httptest.NewRecorder()
	writeResponsesStreamFromChatReader(recorder, "grok-chat-fast", strings.NewReader(raw))
	body := recorder.Body.String()
	if count := strings.Count(body, "event: response.output_item.added"); count != 1 {
		t.Fatalf("function item added count=%d body=%s", count, body)
	}
	if !strings.Contains(body, `"arguments":"{\"x\":1}"`) {
		t.Fatalf("fragmented arguments were not aggregated: %s", body)
	}
}

type observedStreamWriter struct {
	header   http.Header
	mu       sync.Mutex
	body     bytes.Buffer
	needle   string
	observed chan struct{}
	once     sync.Once
}

func newObservedStreamWriter(needle string) *observedStreamWriter {
	return &observedStreamWriter{header: make(http.Header), needle: needle, observed: make(chan struct{})}
}

func (w *observedStreamWriter) Header() http.Header { return w.header }
func (w *observedStreamWriter) WriteHeader(int)     {}
func (w *observedStreamWriter) Flush()              {}
func (w *observedStreamWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.body.Write(data)
	if strings.Contains(w.body.String(), w.needle) {
		w.once.Do(func() { close(w.observed) })
	}
	return n, err
}

func TestAnthropicAdvancedFieldsAndReasoningReplayArePreserved(t *testing.T) {
	req := anthropicMessagesRequest{
		Model: "grok-4.6", MaxTokens: 4096,
		Thinking:      map[string]interface{}{"type": "enabled", "budget_tokens": float64(2048)},
		StopSequences: []string{"END"},
		OutputConfig:  map[string]interface{}{"effort": "high", "format": map[string]interface{}{"type": "json_schema"}},
		MCPServers:    []map[string]interface{}{{"type": "url", "url": "https://example.test/mcp"}},
		Metadata:      map[string]interface{}{"session_id": "claude-session"},
		Messages: []anthropicMessage{
			{Role: "assistant", Content: []interface{}{map[string]interface{}{
				"type": "thinking", "thinking": "private plan", "signature": "opaque-cipher",
			}, map[string]interface{}{"type": "text", "text": "previous answer"}}},
			{Role: "user", Content: "continue"},
		},
	}
	chat, err := anthropicRequestToChat(req)
	if err != nil {
		t.Fatalf("anthropicRequestToChat() error = %v", err)
	}
	if chat.ReasoningEffort == nil || *chat.ReasoningEffort != "high" || chat.PromptCacheKey != "claude-session" {
		t.Fatalf("reasoning/session not preserved: %#v", chat)
	}
	if len(chat.Stop) != 1 || chat.Stop[0] != "END" || len(chat.MCPServers) != 1 || len(chat.ThinkingConfig) == 0 {
		t.Fatalf("advanced fields not preserved: %#v", chat)
	}
	if chat.Messages[0].ReasoningContent != "private plan" || chat.Messages[0].ReasoningEncryptedContent != "opaque-cipher" {
		t.Fatalf("thinking history not preserved: %#v", chat.Messages[0])
	}
}

func TestReasoningReplayIsModelAndSessionIsolated(t *testing.T) {
	h := &Handler{affinity: map[string]sessionAffinityEntry{}, replay: map[string]reasoningReplayEntry{}}
	h.storeReasoningReplay("grok-4.6", "session-a", "cipher-a")
	if got := h.loadReasoningReplay("grok-4.6", "session-a"); got != "cipher-a" {
		t.Fatalf("replay=%q want cipher-a", got)
	}
	if got := h.loadReasoningReplay("grok-4.5", "session-a"); got != "" {
		t.Fatalf("cross-model replay leak: %q", got)
	}
	if got := h.loadReasoningReplay("grok-4.6", "session-b"); got != "" {
		t.Fatalf("cross-session replay leak: %q", got)
	}

	payload := map[string]interface{}{"input": []interface{}{map[string]interface{}{"role": "user", "content": "continue"}}}
	h.applyNativeReasoningReplay("grok-4.6", "session-a", payload)
	input := payload["input"].([]interface{})
	if len(input) != 2 || input[0].(map[string]interface{})["encrypted_content"] != "cipher-a" {
		t.Fatalf("native replay injection=%#v", input)
	}
}

func TestNativeReasoningReplayConvertsStringInput(t *testing.T) {
	h := &Handler{affinity: map[string]sessionAffinityEntry{}, replay: map[string]reasoningReplayEntry{}}
	h.storeReasoningReplay("grok-4.6", "session", "cipher")
	payload := map[string]interface{}{"input": "continue"}
	h.applyNativeReasoningReplay("grok-4.6", "session", payload)
	input, ok := payload["input"].([]interface{})
	if !ok || len(input) != 2 {
		t.Fatalf("input=%#v", payload["input"])
	}
	if input[0].(map[string]interface{})["encrypted_content"] != "cipher" {
		t.Fatalf("replay=%#v", input[0])
	}
}

func TestPrepareGrokSessionSeparatesTenantsAndSoftReplay(t *testing.T) {
	reqA := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	reqA.Header.Set("x-session-id", "same-client-session")
	a := prepareGrokSession(reqA, "grok-4.6", "", []ChatMessage{{Role: "user", Content: "hello"}})
	if a.Key == "" || !a.Replay {
		t.Fatalf("explicit session=%#v", a)
	}
	soft := prepareGrokSession(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), "grok-4.6", "", []ChatMessage{{Role: "user", Content: "hello"}})
	if soft.Key == "" || soft.Replay {
		t.Fatalf("soft session=%#v", soft)
	}
	otherModel := prepareGrokSession(reqA, "grok-4.5", "", []ChatMessage{{Role: "user", Content: "hello"}})
	if otherModel.Key == a.Key {
		t.Fatal("session identity must be model-isolated")
	}
}

func TestQualityGateRetriesLongMissingThinkingButAllowsShortReply(t *testing.T) {
	long := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"this is a sufficiently long visible answer without reasoning evidence\"}\n\n" +
		"data: {\"type\":\"response.completed\"}\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(long))}
	missing, err := gateResponseForThinking(resp)
	if err != nil || !missing {
		t.Fatalf("long missing-thinking gate = %v,%v", missing, err)
	}

	short := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"
	resp = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(short))}
	missing, err = gateResponseForThinking(resp)
	if err != nil || missing {
		t.Fatalf("short reply gate = %v,%v", missing, err)
	}

	withThinking := "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"plan\"}\n\n"
	resp = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(bytes.NewBufferString(withThinking))}
	missing, err = gateResponseForThinking(resp)
	if err != nil || missing {
		t.Fatalf("thinking gate = %v,%v", missing, err)
	}
}

func TestStreamLoopGuardSeparatesAndDetectsRepeatedBlocks(t *testing.T) {
	var guard streamLoopGuard
	block := strings.Repeat("abcdefgh", 16)
	for index := 0; index < 3; index++ {
		if guard.Add(block) {
			t.Fatalf("loop detected too early at %d", index)
		}
	}
	if !guard.Add(block) {
		t.Fatal("four repeated 128-byte blocks should be detected")
	}
	var normal streamLoopGuard
	normalText := strings.Repeat("a", 128) + strings.Repeat("b", 128) + strings.Repeat("c", 128) + strings.Repeat("d", 128)
	if normal.Add(normalText) {
		t.Fatal("normal prose should not trigger conservative loop guard")
	}
}

func TestAffinityMapUsesProviderBoundary(t *testing.T) {
	h := &Handler{affinity: map[string]sessionAffinityEntry{}, replay: map[string]reasoningReplayEntry{}}
	session := grokSessionContext{Key: "key", Model: "grok", Replay: true}
	ctx := withGrokSession(context.Background(), session)
	h.bindAffinity(ctx, ProviderBuild, 10)
	if got := h.affinityAccount(ctx, ProviderBuild); got != 10 {
		t.Fatalf("build affinity=%d", got)
	}
	if got := h.affinityAccount(ctx, ProviderConsole); got != 0 {
		t.Fatalf("provider affinity leaked: %d", got)
	}
}
