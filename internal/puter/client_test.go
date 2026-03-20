package puter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orchids-api/internal/config"
	"orchids-api/internal/prompt"
	"orchids-api/internal/store"
	"orchids-api/internal/upstream"
)

func TestSendRequestWithPayload_EmitsAnthropicEvents(t *testing.T) {
	prevURL := puterAPIURL
	t.Cleanup(func() { puterAPIURL = prevURL })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"auth_token":"puter-token"`) {
			t.Fatalf("request body missing auth token: %s", string(body))
		}
		if !strings.Contains(string(body), `"driver":"claude"`) {
			t.Fatalf("request body missing claude driver: %s", string(body))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{\"type\":\"text\",\"text\":\"hello from puter\"}\n")
	}))
	defer srv.Close()
	puterAPIURL = srv.URL

	client := NewFromAccount(&store.Account{AccountType: "puter", ClientCookie: "puter-token"}, nil)
	var events []string
	err := client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-5",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "hello"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg.Type)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events=%v want %v", events, want)
	}
}

func TestSendRequestWithPayload_PropagatesAPIError(t *testing.T) {
	prevURL := puterAPIURL
	t.Cleanup(func() { puterAPIURL = prevURL })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{\"success\":false,\"error\":{\"iface\":\"puter-chat-completion\",\"code\":\"no_implementation_available\",\"message\":\"No implementation available for interface `puter-chat-completion`.\",\"status\":502}}\n")
	}))
	defer srv.Close()
	puterAPIURL = srv.URL

	client := NewFromAccount(&store.Account{AccountType: "puter", ClientCookie: "puter-token"}, nil)
	err := client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "gpt-5.4",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "hello"}},
		},
	}, nil, nil)
	if err == nil {
		t.Fatal("expected SendRequestWithPayload() to fail")
	}
	if !strings.Contains(err.Error(), "no_implementation_available") {
		t.Fatalf("expected no_implementation_available error, got %v", err)
	}
}

func TestSendRequestWithPayload_PropagatesStringAPIError(t *testing.T) {
	prevURL := puterAPIURL
	t.Cleanup(func() { puterAPIURL = prevURL })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{\"success\":false,\"error\":\"Model not found, please try one of the following models listed here: https://developer.puter.com/ai/models/\"}\n")
	}))
	defer srv.Close()
	puterAPIURL = srv.URL

	client := NewFromAccount(&store.Account{AccountType: "puter", ClientCookie: "puter-token"}, nil)
	err := client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-3-5-sonnet",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "hello"}},
		},
	}, nil, nil)
	if err == nil {
		t.Fatal("expected SendRequestWithPayload() to fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "model not found") {
		t.Fatalf("expected model not found error, got %v", err)
	}
}

func TestReadStreamText_StripsDataPrefixAndUsesDelta(t *testing.T) {
	text, err := readStreamText(strings.NewReader("data: {\"type\":\"text\",\"delta\":\"hello\"}\n\ndata: [DONE]\n"))
	if err != nil {
		t.Fatalf("readStreamText() error = %v", err)
	}
	if text != "hello" {
		t.Fatalf("text = %q, want hello", text)
	}
}

func TestParseToolCalls_StripsToolCallMarkup(t *testing.T) {
	toolCalls, text := parseToolCalls("before <tool_call>{\"name\":\"Read\",\"input\":{\"path\":\"/tmp/a\"}}</tool_call> after")
	if len(toolCalls) != 1 {
		t.Fatalf("toolCalls len = %d, want 1", len(toolCalls))
	}
	if toolCalls[0].Name != "Read" {
		t.Fatalf("toolCalls[0].Name = %q, want Read", toolCalls[0].Name)
	}
	if text != "before  after" && text != "before after" {
		t.Fatalf("text = %q", text)
	}
}

func TestDriverForModel(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{model: "claude-opus-4-5", want: "claude"},
		{model: "gpt-5.1", want: "openai"},
		{model: "gemini-2.5-pro", want: "google"},
		{model: "grok-3", want: "x-ai"},
		{model: "deepseek-chat", want: "deepseek"},
		{model: "mistral-large-latest", want: "mistral"},
		{model: "devstral-small-2507", want: "mistral"},
		{model: "openrouter:openai/gpt-5.1", want: "openrouter"},
		{model: "togetherai:meta-llama/Llama-3.3-70B-Instruct-Turbo", want: "togetherai"},
	}

	for _, tt := range tests {
		if got := driverForModel(tt.model); got != tt.want {
			t.Fatalf("driverForModel(%q)=%q want %q", tt.model, got, tt.want)
		}
	}
}

func TestBuildRequest_IncludesWorkdirToolPrompt(t *testing.T) {
	client := NewFromAccount(&store.Account{AccountType: "puter", ClientCookie: "puter-token"}, nil)
	req := upstream.UpstreamRequest{
		Model:   "claude-sonnet-4-6",
		Workdir: `d:\Code\Orchids-2api`,
		Tools: []interface{}{
			map[string]interface{}{
				"name":        "Read",
				"description": "Read a file",
				"input_schema": map[string]interface{}{
					"type": "object",
				},
			},
		},
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "这个项目是干什么的"}},
		},
	}

	built := client.buildRequest(req)
	if len(built.Args.Messages) == 0 || built.Args.Messages[0].Role != "system" {
		t.Fatalf("expected leading system prompt, got %#v", built.Args.Messages)
	}
	systemPrompt := built.Args.Messages[0].Content
	for _, want := range []string{
		"The real local project working directory is `d:\\Code\\Orchids-2api`.",
		"Treat the project root as `.`",
		"# Tools",
		"<tool_call>",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, systemPrompt)
		}
	}
}

func TestBuildRequest_NoToolsPromptDisablesToolCalls(t *testing.T) {
	client := NewFromAccount(&store.Account{AccountType: "puter", ClientCookie: "puter-token"}, nil)
	req := upstream.UpstreamRequest{
		Model:   "claude-sonnet-4-6",
		Workdir: `d:\Code\Orchids-2api`,
		NoTools: true,
		Tools: []interface{}{
			map[string]interface{}{
				"name": "Read",
			},
		},
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "总结刚才的工具结果"}},
		},
	}

	built := client.buildRequest(req)
	systemPrompt := built.Args.Messages[0].Content
	if !strings.Contains(systemPrompt, "This turn must not make any tool calls.") {
		t.Fatalf("expected no-tools instruction, got:\n%s", systemPrompt)
	}
	if strings.Contains(systemPrompt, "# Tools") {
		t.Fatalf("did not expect tool catalog when no-tools gate is enabled, got:\n%s", systemPrompt)
	}
}

func TestNewFromAccount_ReusesSharedHTTPClient(t *testing.T) {
	cfg := &config.Config{
		RequestTimeout: 30,
		ProxyHTTP:      "http://proxy.local:3128",
		ProxyUser:      "user",
		ProxyPass:      "pass",
	}

	clientA := NewFromAccount(&store.Account{AccountType: "puter", ClientCookie: "token-a"}, cfg)
	clientB := NewFromAccount(&store.Account{AccountType: "puter", ClientCookie: "token-b"}, cfg)

	if clientA.httpClient != clientB.httpClient {
		t.Fatal("expected puter clients with same transport config to reuse shared http client")
	}
	if !clientA.sharedHTTPClient || !clientB.sharedHTTPClient {
		t.Fatal("expected sharedHTTPClient flag to be set")
	}
}
