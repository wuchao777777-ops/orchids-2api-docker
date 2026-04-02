package bolt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/prompt"
	"orchids-api/internal/store"
	"orchids-api/internal/upstream"
)

func TestSendRequestWithPayload_EmitsModelEvents(t *testing.T) {
	prevURL := boltAPIURL
	prevRootURL := boltRootDataURL
	t.Cleanup(func() {
		boltAPIURL = prevURL
		boltRootDataURL = prevRootURL
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "__session=session-token") {
			t.Fatalf("cookie=%q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"projectId":"sb1-demo"`) {
			t.Fatalf("request body missing projectId: %s", string(body))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "0:\"hello\"\n")
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":7}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	var events []string
	err := client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "hello"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg.Type)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	want := []string{"model.text-delta", "model.finish"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events=%v want %v", events, want)
	}
}

func TestSendRequestWithPayload_IgnoresNoReplySentinel(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "0:\"NO_REPLY\"\n")
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":7}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	var events []upstream.SSEMessage
	err := client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "hello"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("events len=%d want 1, events=%#v", len(events), events)
	}
	if events[0].Type != "model.finish" {
		t.Fatalf("first event type=%q want model.finish", events[0].Type)
	}
}

func TestSendRequestWithPayload_ConvertsJSONToolCallTextToModelToolCall(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	toolJSON, err := json.Marshal(`{"tool":"Read","parameters":{"file_path":"README.md"}}`)
	if err != nil {
		t.Fatalf("marshal tool json: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "0:"+string(toolJSON)+"\n")
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":3}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	var events []upstream.SSEMessage
	err = client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "read readme"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events len=%d want 2", len(events))
	}
	if events[0].Type != "model.tool-call" {
		t.Fatalf("first event type=%q want model.tool-call", events[0].Type)
	}
	if got := events[0].Event["toolName"]; got != "Read" {
		t.Fatalf("toolName=%v want Read", got)
	}
	if got := events[0].Event["input"]; got != `{"file_path":"README.md"}` {
		t.Fatalf("input=%v want read input json", got)
	}
	if events[1].Type != "model.finish" {
		t.Fatalf("second event type=%q want model.finish", events[1].Type)
	}
}

func TestSendRequestWithPayload_ConvertsSplitBufferedJSONToolCallToModelToolCall(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	chunks := []string{
		`{"`,
		`tool":"`,
		`Edit","parameters":{"file`,
		`_path":"output.txt","ol`,
		`d_string":"","`,
		`new_string":"13141592712111"}}`,
	}
	encoded := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		raw, err := json.Marshal(chunk)
		if err != nil {
			t.Fatalf("marshal chunk %q: %v", chunk, err)
		}
		encoded = append(encoded, string(raw))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		for _, chunk := range encoded {
			_, _ = io.WriteString(w, "0:"+chunk+"\n")
		}
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":3}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	var events []upstream.SSEMessage
	err := client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我修改 output.txt，增加一行 13141592712111"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events len=%d want 2, events=%v", len(events), events)
	}
	if events[0].Type != "model.tool-call" {
		t.Fatalf("first event type=%q want model.tool-call", events[0].Type)
	}
	if got := events[0].Event["toolName"]; got != "Edit" {
		t.Fatalf("toolName=%v want Edit", got)
	}
	input, ok := events[0].Event["input"].(string)
	if !ok {
		t.Fatalf("input=%T want string", events[0].Event["input"])
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		t.Fatalf("unmarshal input: %v", err)
	}
	if payload["file_path"] != "output.txt" {
		t.Fatalf("file_path=%q want output.txt", payload["file_path"])
	}
	if payload["old_string"] != "" {
		t.Fatalf("old_string=%q want empty string", payload["old_string"])
	}
	if payload["new_string"] != "13141592712111" {
		t.Fatalf("new_string=%q want appended line", payload["new_string"])
	}
	if events[1].Type != "model.finish" {
		t.Fatalf("second event type=%q want model.finish", events[1].Type)
	}
	if got := events[1].Event["finishReason"]; got != "tool_use" {
		t.Fatalf("finishReason=%v want tool_use", got)
	}
}

func TestSendRequestWithPayload_ConvertsPrettyPrintedSplitJSONToolCallToModelToolCall(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	chunks := []string{
		"{\n",
		`  "tool": "Edit",` + "\n",
		`  "parameters": {` + "\n",
		`    "file_path": "test.txt",` + "\n",
		`    "old_string": "111\n222\n333\n444",` + "\n",
		`    "new_string": "111\n222\n333\n444\n555"` + "\n",
		"  }\n}",
	}
	encoded := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		raw, err := json.Marshal(chunk)
		if err != nil {
			t.Fatalf("marshal chunk %q: %v", chunk, err)
		}
		encoded = append(encoded, string(raw))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		for _, chunk := range encoded {
			_, _ = io.WriteString(w, "0:"+chunk+"\n")
		}
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":3}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	var events []upstream.SSEMessage
	err := client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "在 test.txt 中添加 555"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events len=%d want 2, events=%v", len(events), events)
	}
	if events[0].Type != "model.tool-call" {
		t.Fatalf("first event type=%q want model.tool-call", events[0].Type)
	}
	if got := events[0].Event["toolName"]; got != "Edit" {
		t.Fatalf("toolName=%v want Edit", got)
	}
	input, ok := events[0].Event["input"].(string)
	if !ok {
		t.Fatalf("input=%T want string", events[0].Event["input"])
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		t.Fatalf("unmarshal input: %v", err)
	}
	if payload["file_path"] != "test.txt" {
		t.Fatalf("file_path=%q want test.txt", payload["file_path"])
	}
	if payload["old_string"] != "111\n222\n333\n444" {
		t.Fatalf("old_string=%q want existing file content", payload["old_string"])
	}
	if payload["new_string"] != "111\n222\n333\n444\n555" {
		t.Fatalf("new_string=%q want appended file content", payload["new_string"])
	}
	if events[1].Type != "model.finish" {
		t.Fatalf("second event type=%q want model.finish", events[1].Type)
	}
	if got := events[1].Event["finishReason"]; got != "tool_use" {
		t.Fatalf("finishReason=%v want tool_use", got)
	}
}

func TestSendRequestWithPayload_ConvertsLeadingJSONToolCallsWithTrailingSummary(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	chunk, err := json.Marshal("{\n  \"tool_calls\": [\n    {\n      \"tool\": \"Edit\",\n      \"parameters\": {\n        \"file_path\": \"/tmp/cc-agent/sb1-sg78wfbc/project/calculator.py\",\n        \"old_string\": \"return a + b\",\n        \"new_string\": \"return add(a, b)\"\n      }\n    }\n  ]\n}\n\n完成！已添加科学计算功能。")
	if err != nil {
		t.Fatalf("marshal mixed chunk: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "0:"+string(chunk)+"\n")
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":3}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	var events []upstream.SSEMessage
	err = client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计算功能"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events len=%d want 2, events=%v", len(events), events)
	}
	if events[0].Type != "model.tool-call" {
		t.Fatalf("first event type=%q want model.tool-call", events[0].Type)
	}
	if got := events[0].Event["toolName"]; got != "Edit" {
		t.Fatalf("toolName=%v want Edit", got)
	}
	input, ok := events[0].Event["input"].(string)
	if !ok {
		t.Fatalf("input=%T want string", events[0].Event["input"])
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		t.Fatalf("unmarshal input: %v", err)
	}
	if payload["file_path"] != "/tmp/cc-agent/sb1-sg78wfbc/project/calculator.py" {
		t.Fatalf("file_path=%q want sandbox calculator path", payload["file_path"])
	}
	if payload["old_string"] != "return a + b" {
		t.Fatalf("old_string=%q want original code", payload["old_string"])
	}
	if payload["new_string"] != "return add(a, b)" {
		t.Fatalf("new_string=%q want replacement code", payload["new_string"])
	}
	if events[1].Type != "model.finish" {
		t.Fatalf("second event type=%q want model.finish", events[1].Type)
	}
}

func TestSendRequestWithPayload_FlushesUnclosedJSONCodeFenceAsToolCall(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	chunk, err := json.Marshal("```json\n{\"tool_calls\":[{\"function\":\"Read\",\"parameters\":{\"file_path\":\"README.md\"}}]}")
	if err != nil {
		t.Fatalf("marshal chunk: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "0:"+string(chunk)+"\n")
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":3}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	var events []upstream.SSEMessage
	err = client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "inspect readme"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events len=%d want 2", len(events))
	}
	if events[0].Type != "model.tool-call" {
		t.Fatalf("first event type=%q want model.tool-call", events[0].Type)
	}
	if got := events[0].Event["toolName"]; got != "Read" {
		t.Fatalf("toolName=%v want Read", got)
	}
}

func TestSendRequestWithPayload_DropsNarrationBeforeToolCall(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	preamble, err := json.Marshal("看起来这些工具结果是从你本地环境回传的。现在直接执行提交：")
	if err != nil {
		t.Fatalf("marshal preamble: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "0:"+string(preamble)+"\n")
		_, _ = io.WriteString(w, "9:{\"toolName\":\"Bash\",\"args\":{\"command\":\"git add -A && git commit -m \\\"Update bolt client and config\\\" && git push\",\"description\":\"Stage, commit and push all changes\"}}\n")
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"tool_use\",\"usage\":{\"promptTokens\":5,\"completionTokens\":7}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	var events []upstream.SSEMessage
	err = client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "上传到 git"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events len=%d want 2, events=%v", len(events), events)
	}
	if events[0].Type != "model.tool-call" {
		t.Fatalf("first event type=%q want model.tool-call", events[0].Type)
	}
	if got := events[0].Event["toolName"]; got != "Bash" {
		t.Fatalf("toolName=%v want Bash", got)
	}
	if got := events[0].Event["input"]; got != `{"command":"git add -A \u0026\u0026 git commit -m \"Update bolt client and config\" \u0026\u0026 git push","description":"Stage, commit and push all changes"}` {
		t.Fatalf("input=%v want bash git input", got)
	}
	if events[1].Type != "model.finish" {
		t.Fatalf("second event type=%q want model.finish", events[1].Type)
	}
	if got := events[1].Event["finishReason"]; got != "tool_use" {
		t.Fatalf("finishReason=%v want tool_use", got)
	}
}

func TestSendRequestWithPayload_StopsAfterFirstStructuredToolCall(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "9:{\"toolName\":\"Bash\",\"args\":{\"command\":\"git status --short\",\"description\":\"Check git status\"}}\n")
		_, _ = io.WriteString(w, "a:{\"toolCallId\":\"toolu_1\",\"toolName\":\"Bash\",\"args\":{\"command\":\"git status --short\"},\"result\":{\"type\":\"error\",\"content\":\"fatal: not a git repository\"}}\n")
		_, _ = io.WriteString(w, "0:\"please run git init manually\"\n")
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":7}}\n")
		_, _ = io.WriteString(w, "d:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":7}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	var events []upstream.SSEMessage
	err := client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "上传到 git"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events len=%d want 2, events=%v", len(events), events)
	}
	if events[0].Type != "model.tool-call" {
		t.Fatalf("first event type=%q want model.tool-call", events[0].Type)
	}
	if got := events[0].Event["toolName"]; got != "Bash" {
		t.Fatalf("toolName=%v want Bash", got)
	}
	if got := events[0].Event["input"]; got != `{"command":"git status --short","description":"Check git status"}` {
		t.Fatalf("input=%v want git status tool input", got)
	}
	if events[1].Type != "model.finish" {
		t.Fatalf("second event type=%q want model.finish", events[1].Type)
	}
	if got := events[1].Event["finishReason"]; got != "tool_use" {
		t.Fatalf("finishReason=%v want tool_use", got)
	}
}

func TestSendRequestWithPayload_PreservesMarkdownCodeFenceLanguageAcrossChunks(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	chunk1, err := json.Marshal("计算器已创建，运行方式：\n```")
	if err != nil {
		t.Fatalf("marshal chunk1: %v", err)
	}
	chunk2, err := json.Marshal("bash\npython calculator.py\n```")
	if err != nil {
		t.Fatalf("marshal chunk2: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "0:"+string(chunk1)+"\n")
		_, _ = io.WriteString(w, "0:"+string(chunk2)+"\n")
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":9}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	var events []upstream.SSEMessage
	err = client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "写一个计算器"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("events len=%d want 3", len(events))
	}
	if events[0].Type != "model.text-delta" || events[1].Type != "model.text-delta" {
		t.Fatalf("unexpected event types: %v, %v", events[0].Type, events[1].Type)
	}
	if events[2].Type != "model.finish" {
		t.Fatalf("third event type=%q want model.finish", events[2].Type)
	}
	got := events[0].Event["delta"].(string) + events[1].Event["delta"].(string)
	want := "计算器已创建，运行方式：\n```bash\npython calculator.py\n```"
	if got != want {
		t.Fatalf("delta=%q want %q", got, want)
	}
}

func TestSendRequestWithPayload_StripsBoltToolTranscriptFromFinalText(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	chunk, err := json.Marshal("Read 1 file (ctrl+o to expand)\n\n● Write(calculator.py)\n  ⎿  Wrote 42 lines to calculator.py\n       1 def add(a, b): return a + b\n\n● 计算器已创建，运行方式：\n```bash\npython calculator.py\n```")
	if err != nil {
		t.Fatalf("marshal chunk: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "0:"+string(chunk)+"\n")
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":9}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	var events []upstream.SSEMessage
	err = client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "写一个计算器"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("events len=%d want 3", len(events))
	}
	if events[0].Type != "model.text-delta" || events[1].Type != "model.text-delta" {
		t.Fatalf("unexpected event types: %v, %v", events[0].Type, events[1].Type)
	}
	if events[2].Type != "model.finish" {
		t.Fatalf("third event type=%q want model.finish", events[2].Type)
	}
	got := events[0].Event["delta"].(string) + events[1].Event["delta"].(string)
	if strings.Contains(got, "ctrl+o to expand") || strings.Contains(got, "Write(calculator.py)") || strings.Contains(got, "Wrote 42 lines") {
		t.Fatalf("delta still contains tool transcript: %q", got)
	}
	want := "计算器已创建，运行方式：\n```bash\npython calculator.py\n```"
	if got != want {
		t.Fatalf("delta=%q want %q", got, want)
	}
}

func TestSendRequestWithPayload_HandlesBoltToolInvocationFramesAndFinalDMarker(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "9:{\"toolCallId\":\"toolu_1\",\"toolName\":\"Bash\",\"args\":{\"command\":\"ls /tmp/cc-agent/sb1-demo/project/\",\"description\":\"List project files\"}}\n")
		_, _ = io.WriteString(w, "a:{\"toolCallId\":\"toolu_1\",\"toolName\":\"Bash\",\"args\":{\"command\":\"ls /tmp/cc-agent/sb1-demo/project/\",\"description\":\"List project files\"},\"result\":\"README.md\"}\n")
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"unknown\",\"isContinued\":false,\"usage\":{\"promptTokens\":3,\"completionTokens\":27}}\n")
		_, _ = io.WriteString(w, "0:\"沙箱中\"\n")
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"isContinued\":false,\"usage\":{\"promptTokens\":5,\"completionTokens\":386}}\n")
		_, _ = io.WriteString(w, "d:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":386}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	var events []upstream.SSEMessage
	err := client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "inspect project"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events len=%d want 2, events=%v", len(events), events)
	}
	if events[0].Type != "model.tool-call" {
		t.Fatalf("first event type=%q want model.text-delta", events[0].Type)
	}
	if got := events[0].Event["toolName"]; got != "Bash" {
		t.Fatalf("toolName=%v want Bash", got)
	}
	if events[1].Type != "model.finish" {
		t.Fatalf("second event type=%q want model.finish", events[1].Type)
	}
	if got := events[1].Event["finishReason"]; got != "tool_use" {
		t.Fatalf("finishReason=%v want tool_use", got)
	}
}

func TestSendRequestWithPayload_UsesPreparedInputEstimateInFinishUsage(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "0:\"hello\"\n")
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":7}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: "d:\\Code\\Orchids-2api",
		Tools: []interface{}{
			map[string]interface{}{"name": "Read"},
			map[string]interface{}{"name": "Bash"},
		},
		System: []prompt.SystemItem{
			{Type: "text", Text: "keep this custom instruction"},
		},
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "inspect project"}},
		},
	}

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	want := EstimateInputTokens(req)
	var events []upstream.SSEMessage
	if err := client.SendRequestWithPayload(context.Background(), req, func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}, nil); err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events len=%d want 2", len(events))
	}
	usage, ok := events[1].Event["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("finish usage=%#v", events[1].Event["usage"])
	}
	if got := usage["inputTokens"]; got != want.Total {
		t.Fatalf("inputTokens=%v want %d", got, want.Total)
	}
}

func TestSendRequestWithPayload_StreamsVisibleTextBeforeUpstreamCompletes(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "0:\"hello\"\n")
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(250 * time.Millisecond)
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":7}}\n")
		_, _ = io.WriteString(w, "d:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":7}}\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "demo",
	}, nil)

	gotFirstVisible := make(chan upstream.SSEMessage, 1)
	done := make(chan error, 1)
	go func() {
		done <- client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
			Model: "claude-opus-4-6",
			Messages: []prompt.Message{
				{Role: "user", Content: prompt.MessageContent{Text: "hello"}},
			},
		}, func(msg upstream.SSEMessage) {
			if msg.Type == "model.text-delta" {
				select {
				case gotFirstVisible <- msg:
				default:
				}
			}
		}, nil)
	}()

	select {
	case msg := <-gotFirstVisible:
		if got := msg.Event["delta"]; got != "hello" {
			t.Fatalf("delta=%v want hello", got)
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("expected visible text to stream before upstream completed")
	}

	if err := <-done; err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}
}

func TestSendRequestWithPayload_ParsesStructuredToolCallEnvelope(t *testing.T) {
	prevURL := boltAPIURL
	t.Cleanup(func() { boltAPIURL = prevURL })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "9:{\"tool_calls\":[{\"function\":\"Read\",\"parameters\":{\"file_path\":\"README.md\"}}]}\n")
		_, _ = io.WriteString(w, "e:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":5,\"completionTokens\":3}}\n")
	}))
	defer srv.Close()
	boltAPIURL = srv.URL

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	var events []upstream.SSEMessage
	err := client.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "inspect readme"}},
		},
	}, func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}, nil)
	if err != nil {
		t.Fatalf("SendRequestWithPayload() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events len=%d want 2", len(events))
	}
	if events[0].Type != "model.tool-call" {
		t.Fatalf("first event type=%q want model.tool-call", events[0].Type)
	}
	if got := events[0].Event["toolName"]; got != "Read" {
		t.Fatalf("toolName=%v want Read", got)
	}
	if got := events[0].Event["input"]; got != `{"file_path":"README.md"}` {
		t.Fatalf("input=%v want read input json", got)
	}
}

func TestEstimateInputTokens_SplitsPromptBuckets(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: "d:\\Code\\Orchids-2api",
		Tools: []interface{}{
			map[string]interface{}{"name": "Read"},
			map[string]interface{}{"name": "Bash"},
		},
		System: []prompt.SystemItem{
			{Type: "text", Text: "keep this custom instruction"},
		},
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "inspect project"}},
			{Role: "assistant", Content: prompt.MessageContent{Text: "I will inspect the repository."}},
		},
	}

	got := EstimateInputTokens(req)
	if got.BasePromptTokens <= 0 {
		t.Fatalf("BasePromptTokens=%d want >0", got.BasePromptTokens)
	}
	if got.ToolsTokens <= 0 {
		t.Fatalf("ToolsTokens=%d want >0", got.ToolsTokens)
	}
	if got.SystemContextTokens <= 0 {
		t.Fatalf("SystemContextTokens=%d want >0", got.SystemContextTokens)
	}
	if got.HistoryTokens <= 0 {
		t.Fatalf("HistoryTokens=%d want >0", got.HistoryTokens)
	}
	if got.Total != got.BasePromptTokens+got.SystemContextTokens+got.ToolsTokens+got.HistoryTokens {
		t.Fatalf("Total=%d does not match bucket sum", got.Total)
	}
}

func TestEstimateInputTokens_AggressivelyCompactsLongFocusedReadHistory(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我给 calculator.py 添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_read",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": "calculator.py"},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_read",
						Content:   strings.Repeat("1234567890", 1200),
					}},
				},
			},
		},
	}

	got := EstimateInputTokens(req)
	if got.HistoryTokens <= 0 {
		t.Fatalf("HistoryTokens=%d want >0", got.HistoryTokens)
	}
	if got.HistoryTokens >= 1200 {
		t.Fatalf("HistoryTokens=%d want aggressive compaction below 1200", got.HistoryTokens)
	}
}

func TestPrepareRequest_AddsWorkspaceAndToolInstructions(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: "d:\\Code\\Orchids-2api",
		Tools: []interface{}{
			map[string]interface{}{"name": "Read"},
			map[string]interface{}{"function": map[string]interface{}{"name": "Bash"}},
		},
		System: []prompt.SystemItem{
			{Type: "text", Text: "You are Claude Code, Anthropic's official CLI for Claude."},
			{Type: "text", Text: "keep this custom instruction"},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if !strings.Contains(boltReq.GlobalSystemPrompt, `d:\Code\Orchids-2api`) {
		t.Fatalf("system prompt missing workdir: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "Read(file_path, limit?, offset?)") {
		t.Fatalf("system prompt missing Read tool hint: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "keep this custom instruction") {
		t.Fatalf("system prompt dropped custom instruction: %s", boltReq.GlobalSystemPrompt)
	}
}

func TestPrepareRequest_DoesNotAdvertiseBoltToolsWhenRequestOmitsTools(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: "d:\\Code\\Orchids-2api",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加 科学计数法"}},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	for _, marker := range []string{
		"Read(file_path, limit?, offset?)",
		"Write(file_path, content)",
		"Edit(file_path, old_string, new_string, replace_all?)",
		"Bash(command, description?, timeout?, run_in_background?)",
		"Glob(path, pattern)",
		"Grep(path, pattern",
	} {
		if strings.Contains(boltReq.GlobalSystemPrompt, marker) {
			t.Fatalf("system prompt should not advertise bolt tools when request omitted tools; found marker %q in %s", marker, boltReq.GlobalSystemPrompt)
		}
	}
	if strings.Contains(boltReq.GlobalSystemPrompt, "TodoWrite(content)") {
		t.Fatalf("system prompt should not advertise TodoWrite by default: %s", boltReq.GlobalSystemPrompt)
	}
}

func TestPrepareRequest_DropsMCPBoilerplateButKeepsUsefulContext(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		System: []prompt.SystemItem{
			{Type: "text", Text: "You are Claude Code, Anthropic's official CLI for Claude."},
			{Type: "text", Text: strings.Join([]string{
				"You are an interactive agent that helps users with software engineering tasks.",
				"IMPORTANT: Assist with authorized security testing, defensive security, CTF challenges, and educational contexts.",
				"# System",
				"- All text you output outside of tool use is displayed to the user.",
				"# Using your tools",
				"- Do NOT use the Bash to run commands when a relevant dedicated tool is provided.",
				"# MCP Server Instructions",
				"## context7",
				"Use this server to retrieve up-to-date documentation and code examples for any library.",
				"gitStatus: This is the git status at the start of the conversation.",
				"Current branch: main",
				"Status:",
				"M internal/bolt/client.go",
				"Recent commits:",
				"0823630 Improve Grok, Bolt, and Warp Request Handling",
				"# VSCode Extension Context",
				"keep this custom instruction",
			}, "\n")},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if strings.Contains(boltReq.GlobalSystemPrompt, "# MCP Server") {
		t.Fatalf("system prompt should drop MCP heading boilerplate: %s", boltReq.GlobalSystemPrompt)
	}
	if strings.Contains(boltReq.GlobalSystemPrompt, "## context7") {
		t.Fatalf("system prompt should drop MCP server name boilerplate: %s", boltReq.GlobalSystemPrompt)
	}
	if strings.Contains(boltReq.GlobalSystemPrompt, "Do NOT use the Bash to run commands") {
		t.Fatalf("system prompt should drop claude code tool boilerplate: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "gitStatus: This is the git status at the start of the conversation.") {
		t.Fatalf("system prompt should preserve git snapshot context: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "Current branch: main") {
		t.Fatalf("system prompt should preserve current branch context: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "Recent commits:") {
		t.Fatalf("system prompt should preserve recent commit context: %s", boltReq.GlobalSystemPrompt)
	}
	if strings.Contains(boltReq.GlobalSystemPrompt, "# VSCode Extension Context") {
		t.Fatalf("system prompt should drop VSCode heading boilerplate: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "keep this custom instruction") {
		t.Fatalf("system prompt should preserve custom instruction: %s", boltReq.GlobalSystemPrompt)
	}
}

func TestPrepareRequest_CompactsOpenClawSystemPrompt(t *testing.T) {
	openClawSystem := strings.Join([]string{
		"You are a personal assistant running inside OpenClaw.",
		"## Tooling",
		"Tool availability (filtered by policy):",
		"- read: Read file contents",
		"- write: Create or overwrite files",
		"- edit: Make precise edits to files",
		"## Safety",
		"Prioritize safety and human oversight over completion.",
		"## Current Date & Time",
		"Current date: 2026-03-29",
		"Current timezone: Asia/Shanghai",
		"## Workspace",
		"Primary workspace: /workspace/demo",
		"Current workspace: /workspace/demo",
		"## Skills (mandatory)",
		"<skill>",
		"<name>weather</name>",
		"<description>Get current weather and forecasts via wttr.in or Open-Meteo.</description>",
		"<location>/usr/lib/node_modules/openclaw/skills/weather/SKILL.md</location>",
		"</skill>",
		"# Project Context",
		"## /home/zhangdailin/.openclaw/workspace/AGENTS.md",
		"# AGENTS.md - Your Workspace",
		strings.Repeat("workspace instructions\n", 600),
		"## /home/zhangdailin/.openclaw/workspace/MEMORY.md",
		"# MEMORY.md",
		"## 输出风格偏好",
		"默认使用简体中文，避免冗长。",
	}, "\n")

	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		System: []prompt.SystemItem{
			{Type: "text", Text: openClawSystem},
		},
	}

	before := estimateBoltTextTokens(openClawSystem)
	boltReq, estimate := prepareRequest(req, "sb1-demo")
	after := estimate.SystemContextTokens

	if after <= 0 {
		t.Fatalf("SystemContextTokens=%d want >0", after)
	}
	if after >= before/2 {
		t.Fatalf("SystemContextTokens=%d want to be less than half of original %d; prompt=%s", after, before, boltReq.GlobalSystemPrompt)
	}
	if strings.Contains(boltReq.GlobalSystemPrompt, "## Skills (mandatory)") {
		t.Fatalf("system prompt should drop OpenClaw skills catalog: %s", boltReq.GlobalSystemPrompt)
	}
	if strings.Contains(boltReq.GlobalSystemPrompt, "workspace instructions") {
		t.Fatalf("system prompt should drop embedded workspace document dumps: %s", boltReq.GlobalSystemPrompt)
	}
	if strings.Contains(boltReq.GlobalSystemPrompt, "<skill>") {
		t.Fatalf("system prompt should drop skill XML blocks: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "Current date: 2026-03-29") {
		t.Fatalf("system prompt should keep current date context: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "Primary workspace: /workspace/demo") {
		t.Fatalf("system prompt should keep workspace path context: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "默认使用简体中文，避免冗长。") {
		t.Fatalf("system prompt should preserve concise output preference: %s", boltReq.GlobalSystemPrompt)
	}
}

func TestSanitizeBoltSystemText_TruncatesOversizedSystemContext(t *testing.T) {
	text := strings.Join([]string{
		"You are a personal assistant running inside OpenClaw.",
		"## Safety",
		"Keep the user safe.",
		strings.Repeat("A", maxBoltSystemTextChars),
		"## 输出风格偏好",
		"保持简洁。",
	}, "\n")

	got := sanitizeBoltSystemText(text)
	if len(got) > maxBoltSystemTextChars+64 {
		t.Fatalf("len(got)=%d want bounded near %d", len(got), maxBoltSystemTextChars)
	}
	if !strings.Contains(got, "[system context truncated]") {
		t.Fatalf("sanitized text should include truncation marker: %s", got)
	}
	if !strings.Contains(got, "Keep the user safe.") {
		t.Fatalf("sanitized text should preserve head content: %s", got)
	}
	if !strings.Contains(got, "保持简洁。") {
		t.Fatalf("sanitized text should preserve tail content: %s", got)
	}
}

func TestPrepareRequest_AddsGitExecutionInstructionsForUploadIntent(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: "d:\\Code\\Orchids-2api",
		Tools: []interface{}{
			map[string]interface{}{"name": "Read"},
			map[string]interface{}{"name": "Bash"},
		},
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "上传到 git"}},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if !strings.Contains(boltReq.GlobalSystemPrompt, "Bash") {
		t.Fatalf("system prompt missing Bash tool hint: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "必须实际调用 Bash") {
		t.Fatalf("system prompt missing bash execution requirement: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, `d:\Code\Orchids-2api`) {
		t.Fatalf("system prompt missing workdir: %s", boltReq.GlobalSystemPrompt)
	}
}

func TestPrepareRequest_AddsEditFollowupExecutionInstructions(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: "d:\\Code\\Orchids-2api",
		Tools: []interface{}{
			map[string]interface{}{"name": "Read"},
			map[string]interface{}{"name": "Write"},
			map[string]interface{}{"name": "Edit"},
		},
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if !strings.Contains(boltReq.GlobalSystemPrompt, "Edit(file_path, old_string, new_string, replace_all?)") {
		t.Fatalf("system prompt missing Edit tool hint: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "Write(file_path, content)") {
		t.Fatalf("system prompt missing Write tool hint: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "必须实际调用 Edit/Write 完成") {
		t.Fatalf("system prompt missing file-mutation execution requirement: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "Read 只用于获取后续操作需要的上下文") {
		t.Fatalf("system prompt missing read-followup instruction: %s", boltReq.GlobalSystemPrompt)
	}
}

func TestBuildBoltWorkspacePrompt_IncludesGitRepoHint(t *testing.T) {
	workdir := t.TempDir()
	prompt := strings.Join(buildBoltWorkspacePrompt(workdir), "\n")
	if !strings.Contains(prompt, workdir) {
		t.Fatalf("workspace prompt missing workdir path: %s", prompt)
	}
}

func TestPrepareRequest_AddsHistoryAwarePathRecoveryInstructions(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: "d:\\Code\\Orchids-2api",
		Tools: []interface{}{
			map[string]interface{}{"name": "Read"},
			map[string]interface{}{"name": "Bash"},
			map[string]interface{}{"name": "Glob"},
		},
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "\u8fd9\u4e2a\u9879\u76ee\u662f\u5e72\u4ec0\u4e48\u7684"}},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if !strings.Contains(boltReq.GlobalSystemPrompt, `d:\Code\Orchids-2api`) {
		t.Fatalf("system prompt missing workdir: %s", boltReq.GlobalSystemPrompt)
	}
	if !strings.Contains(boltReq.GlobalSystemPrompt, "Read(file_path, limit?, offset?)") {
		t.Fatalf("system prompt missing Read tool hint: %s", boltReq.GlobalSystemPrompt)
	}
}

func TestPrepareRequest_AddsEmptyProjectDirectCreateInstructions(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: `C:\Users\zhangdailin\Desktop\新建文件夹 (2)`,
		Tools: []interface{}{
			map[string]interface{}{"name": "Read"},
			map[string]interface{}{"name": "Write"},
			map[string]interface{}{"name": "Bash"},
			map[string]interface{}{"name": "Glob"},
		},
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "\u5e2e\u6211\u7528python\u5199\u4e00\u4e2a\u8ba1\u7b97\u5668"}},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if !strings.Contains(boltReq.GlobalSystemPrompt, "Write(file_path, content)") {
		t.Fatalf("system prompt missing Write tool hint: %s", boltReq.GlobalSystemPrompt)
	}
}

func TestPrepareRequest_TrimsSupersededEmptyProjectClarificationHistory(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "随便写点东西"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Text: "这是一个空项目。请告诉我你想要构建什么？",
				},
			},
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_glob",
							Name:  "Glob",
							Input: map[string]interface{}{"path": ".", "pattern": "**/*"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_glob",
							Content:   "No files found",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[0].Content; got != "帮我用python写一个计算器" {
		t.Fatalf("first message content=%q want latest concrete request", got)
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "No files found") {
		t.Fatalf("second message content=%q want latest empty-project tool result", got)
	}
	for _, msg := range boltReq.Messages {
		if strings.Contains(msg.Content, "这是一个空项目") || strings.Contains(msg.Content, "随便写点东西") {
			t.Fatalf("expected stale empty-project clarification history to be trimmed, got messages=%#v", boltReq.Messages)
		}
	}
}

func TestPrepareRequest_DropsMisleadingNoFilesFoundProbeAfterSuccessfulWrite(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_write",
							Name:  "Write",
							Input: map[string]interface{}{"file_path": "calculator.py", "content": "print(1)"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_write",
							Content:   "File created successfully at: calculator.py",
						},
					},
				},
			},
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_glob",
							Name:  "Glob",
							Input: map[string]interface{}{"path": ".", "pattern": "**/*.{js,ts,tsx,jsx,vue}"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_glob",
							Content:   "No files found",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 3 {
		t.Fatalf("messages len=%d want 3, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	for _, msg := range boltReq.Messages {
		if strings.Contains(msg.Content, "No files found") {
			t.Fatalf("expected misleading no-files-found probe to be trimmed, got messages=%#v", boltReq.Messages)
		}
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "Write(calculator.py)") {
		t.Fatalf("second message content=%q want preserved successful write history", got)
	}
	if got := boltReq.Messages[2].Content; got != "帮我添加科学计数法" {
		t.Fatalf("third message content=%q want follow-up edit request only", got)
	}
	if got := boltReq.Messages[2].Content; strings.Contains(got, "这是新的修改请求") {
		t.Fatalf("third message content=%q should not include local mutation guard", got)
	}
}

func TestPrepareRequest_DropsSupersededNoFilesFoundProbeAfterLaterPositiveGlob(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: "C:\\Users\\zhangdailin\\Desktop\\1212",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_glob_frontend",
							Name:  "Glob",
							Input: map[string]interface{}{"path": ".", "pattern": "**/*.{js,jsx,ts,tsx,vue}"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_glob_frontend",
							Content:   "No files found",
						},
					},
				},
			},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_glob_all",
							Name:  "Glob",
							Input: map[string]interface{}{"path": ".", "pattern": "**/*"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_glob_all",
							Content:   "C:\\Users\\zhangdailin\\Desktop\\1212\\calculator.py",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[1].Content; strings.Contains(got, "No files found") {
		t.Fatalf("expected superseded empty probe to be trimmed, got: %q", got)
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "calculator.py") {
		t.Fatalf("expected later positive glob result to remain, got: %q", got)
	}
}

func TestPrepareRequest_DropsPositiveProjectProbeAfterLaterRead(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_glob",
							Name:  "Glob",
							Input: map[string]interface{}{"path": ".", "pattern": "**/*"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_glob",
							Content:   "calculator.py",
						},
					},
				},
			},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_read",
							Name:  "Read",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_read",
							Content:   "1→def add(a, b):\n2→    return a + b\n",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[1].Content; strings.Contains(got, "calculator.py\n") && !strings.Contains(got, "def add(a, b):") {
		t.Fatalf("expected superseded positive probe to be trimmed after later read, got: %q", got)
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "def add(a, b):") {
		t.Fatalf("expected later read result to remain, got: %q", got)
	}
}

func TestPrepareRequest_DropsRedundantPositiveProjectProbeAfterEarlierRead(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "给 calculator.py 添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_read",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": "calculator.py"},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_read",
						Content:   "1→def add(a, b):\n2→    return a + b\n",
					}},
				},
			},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_glob",
						Name:  "Glob",
						Input: map[string]interface{}{"path": ".", "pattern": "**/*"},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_glob",
						Content:   "calculator.py",
					}},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "def add(a, b):") {
		t.Fatalf("expected earlier read result to remain, got: %q", got)
	}
}

func TestPrepareRequest_EncodesToolResultsAsUserContentAndDropsAssistantToolInvocations(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "这个项目是干什么的"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_read",
							Name:  "Read",
							Input: map[string]interface{}{"file_path": "README.md"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_read",
							Content:   "# Orchids-2api\n一个基于 Go 的多通道代理服务。",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2", len(boltReq.Messages))
	}
	if got := boltReq.Messages[1].Role; got != "user" {
		t.Fatalf("last role=%q want user", got)
	}
	if !strings.Contains(boltReq.Messages[1].Content, "一个基于 Go 的多通道代理服务") {
		t.Fatalf("expected tool result to be serialized into user content, got: %q", boltReq.Messages[1].Content)
	}
	if strings.Contains(boltReq.Messages[1].Content, "toolInvocation") {
		t.Fatalf("did not expect tool invocation metadata in user content, got: %q", boltReq.Messages[1].Content)
	}
	if len(boltReq.Messages[0].Parts) != 0 {
		t.Fatalf("expected first user message to have no parts, got: %#v", boltReq.Messages[0].Parts)
	}
	if len(boltReq.Messages[1].Parts) != 0 {
		t.Fatalf("expected tool-result follow-up user message to have no parts, got: %#v", boltReq.Messages[1].Parts)
	}
}

func TestPrepareRequest_PreservesMultiTurnEditHistoryAfterWrite(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_write",
							Name:  "Write",
							Input: map[string]interface{}{"file_path": "calculator.py", "content": "print(1)"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_write",
							Content:   "File created successfully at: calculator.py",
						},
					},
				},
			},
			{Role: "assistant", Content: prompt.MessageContent{Text: "完成！计算器已创建在项目目录中。"}},
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 3 {
		t.Fatalf("messages len=%d want 3, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[0].Content; got != "帮我用python写一个计算器" {
		t.Fatalf("first message content=%q want original create request", got)
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "Write(calculator.py)") {
		t.Fatalf("second message content=%q want preserved successful write result", got)
	}
	if got := boltReq.Messages[2].Content; got != "帮我添加科学计数法" {
		t.Fatalf("third message content=%q want follow-up edit request", got)
	}
}

func TestPrepareRequest_DropsSupersededAssistantCompletionSummaryBeforeLaterEditFollowup(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_write",
							Name:  "Write",
							Input: map[string]interface{}{"file_path": "calculator.py", "content": "print(1)"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_write",
							Content:   "File created successfully at: calculator.py",
						},
					},
				},
			},
			{Role: "assistant", Content: prompt.MessageContent{Text: "计算器已创建完成，文件为 `calculator.py`。运行方式：python calculator.py。支持加、减、乘、除，输入 quit 退出。"}},
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_read",
							Name:  "Read",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_read",
							Content:   "1→def add(a, b):\n2→    return a + b\n",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 3 {
		t.Fatalf("messages len=%d want 3, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	for _, msg := range boltReq.Messages {
		if strings.Contains(msg.Content, "计算器已创建完成") {
			t.Fatalf("expected stale assistant completion summary to be trimmed, got messages=%#v", boltReq.Messages)
		}
	}
	if got := boltReq.Messages[1].Content; got != "帮我添加科学计数法" {
		t.Fatalf("second message content=%q want latest explicit edit request", got)
	}
	if got := boltReq.Messages[2].Content; !strings.Contains(got, "[工具结果]") {
		t.Fatalf("third message content=%q want neutral tool-result marker for follow-up", got)
	}
	if got := boltReq.Messages[2].Content; !strings.Contains(got, "def add(a, b)") {
		t.Fatalf("third message content=%q want latest read result", got)
	}
}

func TestPrepareRequest_DropsInjectedFailureAssistantNoiseBeforeLaterBoltFollowup(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮他添加科学计数法"}},
			{Role: "assistant", Content: prompt.MessageContent{Text: "Request failed: all available accounts for this channel are currently rate-limited. Please wait for cooldown or add another valid account. (selector: no enabled accounts available for channel: bolt, last error: bolt API error: status=429, body={\"code\":\"rate-limited\"})"}},
			{Role: "user", Content: prompt.MessageContent{Text: "给他添加科学计数法"}},
			{Role: "assistant", Content: prompt.MessageContent{Text: "Request failed: all available accounts for this channel are currently rate-limited. Please wait for cooldown or add another valid account. (selector: no enabled accounts available for channel: bolt, last error: bolt API error: status=429, body={\"code\":\"rate-limited\"})"}},
			{Role: "user", Content: prompt.MessageContent{Text: "给他添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_read",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": "calculator.py"},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_read",
						Content:   "1→def add(a, b):\n2→    return a + b\n",
					}},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 3 {
		t.Fatalf("messages len=%d want 3, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	for _, msg := range boltReq.Messages {
		if strings.Contains(msg.Content, "all available accounts for this channel are currently rate-limited") {
			t.Fatalf("expected injected failure assistant text to be trimmed, got messages=%#v", boltReq.Messages)
		}
	}
	if got := boltReq.Messages[0].Content; got != "帮他添加科学计数法" {
		t.Fatalf("first message content=%q want original task", got)
	}
	if got := boltReq.Messages[1].Content; got != "给他添加科学计数法" {
		t.Fatalf("second message content=%q want latest retry task", got)
	}
	if got := boltReq.Messages[2].Content; !strings.Contains(got, "[工具结果]") {
		t.Fatalf("third message content=%q want neutral tool-result marker for read follow-up", got)
	}
	if got := boltReq.Messages[2].Content; !strings.Contains(got, "def add(a, b)") {
		t.Fatalf("third message content=%q want read result", got)
	}
}

func TestPrepareRequest_DropsDuplicateStandaloneRetriesSeparatedOnlyByNoContentPlaceholder(t *testing.T) {
	workdir := t.TempDir()
	testPath := filepath.Join(workdir, "test.txt")
	if err := os.WriteFile(testPath, []byte("111\n222\n333\n444\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: workdir,
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "在 test.txt 中添加 555"}},
			{Role: "assistant", Content: prompt.MessageContent{Text: "Request failed: all available accounts for this channel are currently rate-limited. Please wait for cooldown or add another valid account. (selector: no enabled accounts available for channel: bolt, last error: bolt API error: status=429, body={\"code\":\"rate-limited\"})"}},
			{Role: "user", Content: prompt.MessageContent{Text: "在 test.txt 中添加 555"}},
			{Role: "assistant", Content: prompt.MessageContent{Text: "(no content)"}},
			{Role: "user", Content: prompt.MessageContent{Text: "在 test.txt 中添加 555"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_read",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": testPath},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_read",
						Content:   "1\t111\n2\t222\n3\t333\n4\t444\n",
					}},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}

	joined := make([]string, 0, len(boltReq.Messages))
	for _, msg := range boltReq.Messages {
		joined = append(joined, msg.Content)
	}
	all := strings.Join(joined, "\n\n")
	if strings.Contains(all, "(no content)") {
		t.Fatalf("expected no-content placeholder to be dropped, got: %q", all)
	}
	if strings.Contains(all, "all available accounts for this channel are currently rate-limited") {
		t.Fatalf("expected rate-limit assistant noise to be dropped, got: %q", all)
	}
	if got := boltReq.Messages[0].Content; got != "在 test.txt 中添加 555" {
		t.Fatalf("first message content=%q want latest standalone retry only", got)
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "当前任务: 在 test.txt 中添加 555") {
		t.Fatalf("second message content=%q want active task echoed in read follow-up", got)
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "111\n222\n333\n444") {
		t.Fatalf("second message content=%q want read result preserved", got)
	}
}

func TestPrepareRequest_DropsLostReadContextAssistantNoiseAndUnchangedReadSummary(t *testing.T) {
	workdir := t.TempDir()
	testPath := filepath.Join(workdir, "test.txt")
	if err := os.WriteFile(testPath, []byte("111\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: workdir,
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "在 test.txt 中添加 222"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_read_1",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": testPath},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_read_1",
						Content:   "1\t111\n",
					}},
				},
			},
			{Role: "assistant", Content: prompt.MessageContent{Text: "我理解了。请告诉我你对 test.txt 文件的新修改需求是什么？"}},
			{Role: "user", Content: prompt.MessageContent{Text: "在 test.txt 中添加 222"}},
			{Role: "assistant", Content: prompt.MessageContent{Text: "我需要看到 `test.txt` 的内容才能继续修改。请告诉我文件里有什么内容，或者你想怎么修改它？"}},
			{Role: "user", Content: prompt.MessageContent{Text: "在 test.txt 中添加 222"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_read_2",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": testPath},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_read_2",
						Content:   "File unchanged since last read. The content from the earlier Read tool_result in this conversation is still current — refer to that instead of re-reading.",
					}},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 4 {
		t.Fatalf("messages len=%d want 4, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}

	joined := make([]string, 0, len(boltReq.Messages))
	for _, msg := range boltReq.Messages {
		if msg.Role == "assistant" {
			t.Fatalf("expected lost-context assistant text to be dropped, got messages=%#v", boltReq.Messages)
		}
		joined = append(joined, msg.Content)
	}
	all := strings.Join(joined, "\n\n")
	if strings.Contains(all, "新修改需求是什么") || strings.Contains(all, "请告诉我文件里有什么内容") {
		t.Fatalf("expected lost-context assistant prompts to be removed, got: %q", all)
	}
	if strings.Contains(all, "File unchanged since last read") {
		t.Fatalf("expected unchanged read summary to be dropped, got: %q", all)
	}
	if !strings.Contains(all, "111") {
		t.Fatalf("expected earlier read content to remain available, got: %q", all)
	}
	if got := boltReq.Messages[len(boltReq.Messages)-1].Content; got != "在 test.txt 中添加 222" {
		t.Fatalf("last message content=%q want latest user request", got)
	}
}

func TestPrepareRequest_DropsStaleReadbackSummaryBeforeLaterRetry(t *testing.T) {
	workdir := t.TempDir()
	testPath := filepath.Join(workdir, "test.txt")
	if err := os.WriteFile(testPath, []byte("111\n222\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: workdir,
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "在 test.txt 中添加 222"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_read_1",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": testPath},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_read_1",
						Content:   "1\t111\n",
					}},
				},
			},
			{Role: "assistant", Content: prompt.MessageContent{Text: "The file contains: `111`"}},
			{Role: "user", Content: prompt.MessageContent{Text: "在 test.txt 中添加 333"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_read_2",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": testPath},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_read_2",
						Content:   "1\t111\n2\t222\n",
					}},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	joined := make([]string, 0, len(boltReq.Messages))
	for _, msg := range boltReq.Messages {
		joined = append(joined, msg.Content)
	}
	all := strings.Join(joined, "\n\n")
	if strings.Contains(all, "The file contains") {
		t.Fatalf("expected stale readback summary to be dropped, got: %q", all)
	}
	if got := boltReq.Messages[len(boltReq.Messages)-1].Content; !strings.Contains(got, "当前任务: 在 test.txt 中添加 333") {
		t.Fatalf("last message content=%q want active task echoed in read follow-up", got)
	}
}

func TestPrepareRequest_DropsStaleAssistantRawToolCallJSONBeforeLaterRetry(t *testing.T) {
	workdir := t.TempDir()
	testPath := filepath.Join(workdir, "test.txt")
	if err := os.WriteFile(testPath, []byte("111\n222\n333\n444\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: workdir,
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "在 test.txt 中添加 555"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_read_1",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": testPath},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_read_1",
						Content:   "1\t111\n2\t222\n3\t333\n4\t444\n",
					}},
				},
			},
			{Role: "assistant", Content: prompt.MessageContent{Text: "{\n  \"tool\": \"Edit\",\n  \"parameters\": {\n    \"file_path\": \"test.txt\",\n    \"old_string\": \"111\\n222\\n333\\n444\",\n    \"new_string\": \"111\\n222\\n333\\n444\\n555\"\n  }\n}"}},
			{Role: "user", Content: prompt.MessageContent{Text: "在 test.txt 中添加 555"}},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	joined := make([]string, 0, len(boltReq.Messages))
	for _, msg := range boltReq.Messages {
		joined = append(joined, msg.Content)
	}
	all := strings.Join(joined, "\n\n")
	if strings.Contains(all, "\"tool\": \"Edit\"") {
		t.Fatalf("expected stale assistant raw tool json to be dropped, got: %q", all)
	}
	if got := boltReq.Messages[len(boltReq.Messages)-1].Content; got != "在 test.txt 中添加 555" {
		t.Fatalf("last message content=%q want latest user retry", got)
	}
}

func TestPrepareRequest_DropsUnsupportedSimToolResultNoise(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "上传到 git"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_git",
							Name:  "Bash",
							Input: map[string]interface{}{"command": "git status --short"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_git",
							Content:   "M internal/bolt/client.go",
						},
						{
							Type:      "tool_result",
							ToolUseID: "tool_git",
							Content:   "UNSUPPORTED_SIM_COMMAND: ",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2", len(boltReq.Messages))
	}
	if strings.Contains(boltReq.Messages[1].Content, "UNSUPPORTED_SIM_COMMAND") {
		t.Fatalf("expected unsupported sim tool result to be dropped, got: %q", boltReq.Messages[1].Content)
	}
	if !strings.Contains(boltReq.Messages[1].Content, "M internal/bolt/client.go") {
		t.Fatalf("expected valid tool result to remain, got: %q", boltReq.Messages[1].Content)
	}
}

func TestPrepareRequest_MarksToolResultOnlyFollowupAsContinuation(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "上传到 git"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_git",
							Name:  "Bash",
							Input: map[string]interface{}{"command": "git status --short"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_git",
							Content:   "M internal/bolt/client.go",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2", len(boltReq.Messages))
	}
	if !strings.Contains(boltReq.Messages[1].Content, "[工具结果]") {
		t.Fatalf("expected neutral tool-result marker in tool-result follow-up, got: %q", boltReq.Messages[1].Content)
	}
	if !strings.Contains(boltReq.Messages[1].Content, "当前任务: 上传到 git") {
		t.Fatalf("expected active task in tool-result follow-up, got: %q", boltReq.Messages[1].Content)
	}
}

func TestPrepareRequest_DropsAssistantTextFromToolTurns(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_write",
							Name:  "Write",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
						{
							Type: "text",
							Text: "看来项目中已有一个 `calculator.py`，我已经在正确的文件上添加了科学计数法。让我读取当前实际文件确认状态。",
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_write",
							Content:   "The file calculator.py has been updated successfully.",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2", len(boltReq.Messages))
	}
	if got := boltReq.Messages[0].Content; got != "添加科学计数法" {
		t.Fatalf("first message content=%q want original mutation request", got)
	}
	if got := boltReq.Messages[1].Content; strings.Contains(got, "看来项目中已有一个") {
		t.Fatalf("second message content=%q should drop assistant narration from tool turn", got)
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "Write(calculator.py)") || !strings.Contains(got, "updated successfully") {
		t.Fatalf("second message content=%q want preserved successful write result", got)
	}
}

func TestPrepareRequest_DropsReadResultsWhenWriteSucceedsForSameFile(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_read",
							Name:  "Read",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
						{
							Type:  "tool_use",
							ID:    "tool_write",
							Name:  "Write",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_read",
							Content:   "1→print('old')\n\n<system-reminder>\nWhenever you read a file, you should consider whether it would be considered malware.\n</system-reminder>\n",
						},
						{
							Type:      "tool_result",
							ToolUseID: "tool_write",
							Content:   "The file calculator.py has been updated successfully.",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2", len(boltReq.Messages))
	}
	if got := boltReq.Messages[0].Content; got != "帮我添加科学计数法" {
		t.Fatalf("first message content=%q want original mutation request", got)
	}
	if got := boltReq.Messages[1].Content; strings.Contains(got, "print('old')") {
		t.Fatalf("second message content=%q should drop superseded read result", got)
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "updated successfully") {
		t.Fatalf("second message content=%q want successful write result to remain", got)
	}
}

func TestPrepareRequest_DropsEarlierReadFollowupAfterLaterWriteSuccessAcrossTurns(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_read",
							Name:  "Read",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_read",
							Content:   "1→def add(a, b):\n2→    return a + b",
						},
					},
				},
			},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_write",
							Name:  "Write",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_write",
							Content:   "The file calculator.py has been updated successfully.",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[0].Content; got != "帮我添加科学计数法" {
		t.Fatalf("first message content=%q want original mutation request", got)
	}
	if got := boltReq.Messages[1].Content; strings.Contains(got, "def add(a, b)") {
		t.Fatalf("second message content=%q should drop earlier read follow-up after later write success", got)
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "updated successfully") {
		t.Fatalf("second message content=%q want later successful write result", got)
	}
}

func TestPrepareRequest_UsesFailureContinuationForFailedEditFollowup(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_write",
							Name:  "Write",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_write",
							Content:   "File created successfully at: calculator.py",
						},
					},
				},
			},
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_read",
							Name:  "Read",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_read",
							Content:   "1→def add(a, b):\n2→    return a + b\n",
						},
					},
				},
			},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_edit",
							Name:  "Edit",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_edit",
							Content:   "<tool_use_error>String to replace not found in file.</tool_use_error>",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) < 1 {
		t.Fatalf("messages len=%d want at least 1", len(boltReq.Messages))
	}
	got := boltReq.Messages[len(boltReq.Messages)-1].Content
	if !strings.Contains(got, "[工具结果 - 上次修改未成功]") {
		t.Fatalf("expected failed edit follow-up to be marked as failure, got: %q", got)
	}
	if !strings.Contains(got, "String to replace not found in file.") {
		t.Fatalf("expected failed edit follow-up to keep the upstream error detail, got: %q", got)
	}
}

func TestPrepareRequest_DropsSupersededEditFailureAfterLaterReadRetry(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_read_1",
							Name:  "Read",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_read_1",
							Content:   "1→first snapshot\n2→return a + b\n",
						},
					},
				},
			},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_edit_1",
							Name:  "Edit",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_edit_1",
							Content:   "<tool_use_error>String to replace not found in file.\nString: old attempt</tool_use_error>",
						},
					},
				},
			},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_read_2",
							Name:  "Read",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_read_2",
							Content:   "1→second snapshot\n2→return add_scientific(a, b)\n",
						},
					},
				},
			},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_edit_2",
							Name:  "Edit",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_edit_2",
							Content:   "<tool_use_error>String to replace not found in file.\nString: second attempt</tool_use_error>",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 3 {
		t.Fatalf("messages len=%d want 3, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[1].Content; strings.Contains(got, "first snapshot") {
		t.Fatalf("expected superseded read result to be trimmed, got: %q", got)
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "second snapshot") {
		t.Fatalf("expected latest read result to remain, got: %q", got)
	}
	if got := boltReq.Messages[2].Content; strings.Contains(got, "old attempt") {
		t.Fatalf("expected stale edit failure to be trimmed after later read retry, got: %q", got)
	}
	if got := boltReq.Messages[2].Content; !strings.Contains(got, "second attempt") {
		t.Fatalf("expected latest edit failure to remain, got: %q", got)
	}
}

func TestPrepareRequest_DropsEarlierRepeatedEditFailureForSameFile(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_edit_1",
							Name:  "Edit",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_edit_1",
							Content:   "<tool_use_error>String to replace not found in file.\nString: stale-1</tool_use_error>",
						},
					},
				},
			},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_edit_2",
							Name:  "Edit",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_edit_2",
							Content:   "<tool_use_error>String to replace not found in file.\nString: stale-2</tool_use_error>",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[1].Content; strings.Contains(got, "stale-1") {
		t.Fatalf("expected earlier repeated edit failure to be trimmed, got: %q", got)
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "stale-2") {
		t.Fatalf("expected latest repeated edit failure to remain, got: %q", got)
	}
}

func TestBuildBoltToolUsagePrompt_IncludesMutationFailureRecoveryRule(t *testing.T) {
	got := strings.Join(buildBoltToolUsagePrompt([]string{"Read", "Write", "Edit", "Bash"}, []prompt.Message{
		{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
	}), "\n")
	for _, name := range []string{"Read", "Write", "Edit", "Bash"} {
		if !strings.Contains(got, name) {
			t.Fatalf("tool prompt missing tool name %q: %s", name, got)
		}
	}
}

func TestBuildBoltToolUsagePrompt_NonCodingRequestStaysNeutral(t *testing.T) {
	got := strings.Join(buildBoltToolUsagePrompt([]string{"Read", "Write", "Edit"}, []prompt.Message{
		{Role: "user", Content: prompt.MessageContent{Text: "\u73b0\u5728\u4e0a\u6d77\u7684\u5929\u6c14\u600e\u4e48\u6837"}},
	}), "\n")
	for _, name := range []string{"Read", "Write", "Edit"} {
		if !strings.Contains(got, name) {
			t.Fatalf("tool prompt missing tool name %q: %s", name, got)
		}
	}
}

func TestPrepareRequest_ReadFollowupContinuationPrefersEditAndNoPreamble(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_write",
							Name:  "Write",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_write",
							Content:   "File created successfully at: calculator.py",
						},
					},
				},
			},
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_read",
							Name:  "Read",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_read",
							Content:   "1→def add(a, b):\n2→    return a + b\n",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) < 1 {
		t.Fatalf("messages len=%d want at least 1", len(boltReq.Messages))
	}
	got := boltReq.Messages[len(boltReq.Messages)-1].Content
	if !strings.Contains(got, "[工具结果]") {
		t.Fatalf("expected minimal tool-result marker in read follow-up, got: %q", got)
	}
	if !strings.Contains(got, "Read(calculator.py)") {
		t.Fatalf("expected read tool context in continuation, got: %q", got)
	}
	if !strings.Contains(got, "当前任务: 帮我添加科学计数法") {
		t.Fatalf("expected active task in read follow-up continuation, got: %q", got)
	}
}

func TestPrepareRequest_LeavesProjectPromptEmptyToAvoidDuplicatingGlobalPrompt(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: "d:\\Code\\Orchids-2api",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
		},
		Tools: []interface{}{
			map[string]interface{}{"name": "Read"},
			map[string]interface{}{"name": "Edit"},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if strings.TrimSpace(boltReq.GlobalSystemPrompt) == "" {
		t.Fatal("expected global system prompt to remain populated")
	}
	if strings.TrimSpace(boltReq.ProjectPrompt) != "" {
		t.Fatalf("expected project prompt to stay empty to avoid duplicating global prompt, got: %q", boltReq.ProjectPrompt)
	}
}

func TestPrepareRequest_DropsSupersededSuccessfulMutationAfterLaterRead(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_write",
						Name:  "Write",
						Input: map[string]interface{}{"file_path": "calculator.py"},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_write",
						Content:   "File created successfully at: calculator.py",
					}},
				},
			},
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_read",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": "calculator.py"},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_read",
						Content:   "1→def add(a, b):\n2→    return a + b\n",
					}},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 3 {
		t.Fatalf("messages len=%d want 3, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[1].Content; got != "帮我添加科学计数法" {
		t.Fatalf("second message content=%q want latest standalone task", got)
	}
	if got := boltReq.Messages[2].Content; strings.Contains(got, "File created successfully at: calculator.py") {
		t.Fatalf("expected stale successful write result to be trimmed after later read, got: %q", got)
	}
	if got := boltReq.Messages[2].Content; !strings.Contains(got, "def add(a, b)") {
		t.Fatalf("expected latest read result to remain, got: %q", got)
	}
}

func TestPrepareRequest_FollowupMutationAfterSuccessfulCreateGetsGuard(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_write",
						Name:  "Write",
						Input: map[string]interface{}{"file_path": "calculator.py"},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_write",
						Content:   "File created successfully at: calculator.py",
					}},
				},
			},
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 3 {
		t.Fatalf("messages len=%d want 3, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[1].Content; !strings.Contains(got, "Write(calculator.py)") {
		t.Fatalf("second message content=%q want preserved successful write history", got)
	}
	got := boltReq.Messages[2].Content
	if got != "帮我添加科学计数法" {
		t.Fatalf("expected latest task to remain as standalone user message, got: %q", got)
	}
	if strings.Contains(got, "这是新的修改请求") || strings.Contains(got, "不要直接总结") {
		t.Fatalf("expected no local mutation guard in follow-up task, got: %q", got)
	}
}

func TestPrepareRequest_DropsStaleMissingWorkspaceHistory(t *testing.T) {
	workdir := t.TempDir()
	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: workdir,
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_write",
						Name:  "Write",
						Input: map[string]interface{}{"file_path": "calculator.py"},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_write",
						Content:   "The file calculator.py has been updated successfully.",
					}},
				},
			},
			{
				Role:    "assistant",
				Content: prompt.MessageContent{Text: "计算器已经完整实现，文件 `calculator.py` 可直接运行。"},
			},
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
			{
				Role:    "user",
				Content: prompt.MessageContent{Text: "上一轮你在没有任何新的成功 Write/Edit 工具结果的情况下直接声称已经完成，这是错误的。你上一轮只有 Read 结果，没有任何成功的 Write/Edit，因此不能根据读取结果声称已经添加完功能。请直接基于刚读到的`calculator.py`内容继续调用 Edit/Write 完成修改；不要把“文件存在”、“Glob 找到了文件”、或更早创建成功当成当前任务已经完成。除非先出现新的成功 Write/Edit 工具结果，否则不要输出“已更新”“已完成”“可以运行”等完成总结。如果决定调用工具，本回合第一个非空输出字符必须直接是 `{`。"},
			},
			{
				Role:    "user",
				Content: prompt.MessageContent{Text: "你刚刚已经成功 Read 过`calculator.py`，但又再次请求 Read 同一路径，这属于无效重复读取。当前任务是继续修改代码，不是继续确认文件是否存在。除非上一份 Read 结果因为截断而确实缺少你马上要修改的那一小段必要文本，否则不要再次 Read 同一路径。请直接基于已经读到的`calculator.py`内容继续调用 Edit/Write 完成修改；如果文件已存在，优先 Edit。在出现新的成功 Write/Edit 工具结果之前，不要输出“已完成”“已更新”“可以运行”等总结。如果决定调用工具，本回合第一个非空输出字符必须直接是 `{`。"},
			},
		},
		Tools: []interface{}{
			map[string]interface{}{"name": "Read"},
			map[string]interface{}{"name": "Write"},
			map[string]interface{}{"name": "Edit"},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[0].Content; got != "帮我用python写一个计算器" {
		t.Fatalf("first message content=%q want original create intent", got)
	}
	if got := boltReq.Messages[1].Content; got != "帮我添加科学计数法" {
		t.Fatalf("second message content=%q want latest edit intent", got)
	}
	if got := strings.Join([]string{boltReq.Messages[0].Content, boltReq.Messages[1].Content}, "\n"); strings.Contains(got, "已更新") || strings.Contains(got, "可直接运行") || strings.Contains(got, "你刚刚已经成功 Read 过`calculator.py`") {
		t.Fatalf("expected stale deleted-file history to be trimmed from remaining messages, got: %q", got)
	}
}

func TestFormatBoltToolResultContinuation_CompressesGeneralFollowupPrompt(t *testing.T) {
	got := formatBoltToolResultContinuation(false, false, nil, "")
	if !strings.Contains(got, "[工具结果]") {
		t.Fatalf("expected minimal tool-result marker, got: %q", got)
	}
	if strings.Contains(got, "基于这些结果继续回答") {
		t.Fatalf("expected no verbose continuation guidance, got: %q", got)
	}
	if len([]rune(got)) > 30 {
		t.Fatalf("expected minimal continuation to stay very short, got len=%d content=%q", len([]rune(got)), got)
	}
}

func TestFormatBoltToolResultContinuation_IncludesPriorToolContext(t *testing.T) {
	got := formatBoltToolResultContinuation(false, false, &boltSerializedToolResult{
		ToolName: "Read",
		ToolPath: "/usr/lib/node_modules/openclaw/skills/weather/SKILL.md",
	}, "在 test.txt 中添加 333")
	if !strings.Contains(got, "[工具结果]") {
		t.Fatalf("expected tool-result marker, got: %q", got)
	}
	if !strings.Contains(got, "当前任务: 在 test.txt 中添加 333") {
		t.Fatalf("expected active user task in continuation, got: %q", got)
	}
	if !strings.Contains(got, "Read(/usr/lib/node_modules/openclaw/skills/weather/SKILL.md)") {
		t.Fatalf("expected prior tool context in continuation, got: %q", got)
	}
}

func TestPrepareRequest_TruncatesLongReadResults(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我分析这个文件"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_read",
							Name:  "Read",
							Input: map[string]interface{}{"file_path": "README.md"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_read",
							Content:   strings.Repeat("1234567890", 800),
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2", len(boltReq.Messages))
	}
	got := boltReq.Messages[1].Content
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("expected long read result to be truncated, got: %q", got)
	}
	if len([]rune(got)) >= len([]rune(strings.Repeat("1234567890", 800))) {
		t.Fatalf("expected truncated payload to be shorter, got len=%d", len([]rune(got)))
	}
}

func TestPrepareRequest_KeepsLargerReadWindowForFocusedFile(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我给 calculator.py 添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_read",
							Name:  "Read",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_read",
							Content:   strings.Repeat("1234567890", 180),
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2", len(boltReq.Messages))
	}
	got := boltReq.Messages[1].Content
	if strings.Contains(got, "truncated read output") {
		t.Fatalf("expected focused file read result to keep a larger window, got: %q", got)
	}
	if !strings.Contains(got, strings.Repeat("1234567890", 120)) {
		t.Fatalf("expected focused file read result to retain long content, got: %q", got)
	}
}

func TestPrepareRequest_TruncatedFocusedReadResultDoesNotEncourageImmediateReread(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我给 calculator.py 添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_read",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": "calculator.py"},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_read",
						Content:   strings.Repeat("1234567890", 700),
					}},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2", len(boltReq.Messages))
	}
	got := boltReq.Messages[1].Content
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("expected focused file read result to still truncate when very long, got: %q", got)
	}
}

func TestPrepareRequest_DropsInvalidPathResultsAndSupersededEditErrors(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_abs_read",
							Name:  "Read",
							Input: map[string]interface{}{"file_path": "C:\\Users\\zhangdailin\\Desktop\\111\\calculator.py"},
						},
						{
							Type:  "tool_use",
							ID:    "tool_edit",
							Name:  "Edit",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
						{
							Type:  "tool_use",
							ID:    "tool_abs_write",
							Name:  "Write",
							Input: map[string]interface{}{"file_path": "C:\\Users\\zhangdailin\\Desktop\\111\\calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_abs_read",
							Content:   "1→import math",
						},
						{
							Type:      "tool_result",
							ToolUseID: "tool_edit",
							Content:   "<tool_use_error>String to replace not found in file.</tool_use_error>",
						},
						{
							Type:      "tool_result",
							ToolUseID: "tool_abs_write",
							Content:   "The file C:\\Users\\zhangdailin\\Desktop\\111\\calculator.py has been updated successfully.",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 1 {
		t.Fatalf("messages len=%d want 1, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[0].Content; got != "添加科学计数法" {
		t.Fatalf("first message content=%q want original user prompt only", got)
	}
}

func TestPrepareRequest_RelativizesWorkspaceAbsoluteGlobResults(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: "C:\\Users\\zhangdailin\\Desktop\\1212",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我添加科学计数法"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_glob",
							Name:  "Glob",
							Input: map[string]interface{}{"path": ".", "pattern": "**/*"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_glob",
							Content:   "C:\\Users\\zhangdailin\\Desktop\\1212\\calculator.py",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	got := boltReq.Messages[1].Content
	if strings.Contains(got, "C:\\Users\\zhangdailin\\Desktop\\1212\\calculator.py") {
		t.Fatalf("expected workspace absolute path to be relativized, got: %q", got)
	}
	if !strings.Contains(got, "calculator.py") {
		t.Fatalf("expected relativized workspace path to remain in history, got: %q", got)
	}
}

func TestPrepareRequest_PreservesWorkspaceAbsoluteReadHistory(t *testing.T) {
	workdir := t.TempDir()
	outputPath := filepath.Join(workdir, "output.txt")
	if err := os.WriteFile(outputPath, []byte("123123\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: workdir,
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我修改output文件，增加一行 13141592712111"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_read",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": outputPath},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_read",
						Content:   "1\t123123\n2\t",
					}},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	got := boltReq.Messages[1].Content
	if strings.Contains(got, outputPath) {
		t.Fatalf("expected workspace absolute read path to be relativized, got: %q", got)
	}
	if !strings.Contains(got, "Read(output.txt)") {
		t.Fatalf("expected read continuation to keep the relative target path, got: %q", got)
	}
	if !strings.Contains(got, "123123") {
		t.Fatalf("expected read content to remain available for follow-up editing, got: %q", got)
	}
}

func TestPrepareRequest_StripsTaggedBlankReadLineNumberArtifacts(t *testing.T) {
	workdir := t.TempDir()
	outputPath := filepath.Join(workdir, "output.txt")
	if err := os.WriteFile(outputPath, []byte("123123\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	req := upstream.UpstreamRequest{
		Model:   "claude-opus-4-6",
		Workdir: workdir,
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我修改output文件，增加一行 13141592712111"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:  "tool_use",
						ID:    "tool_read",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": outputPath},
					}},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{{
						Type:      "tool_result",
						ToolUseID: "tool_read",
						Content:   "1\t123123\n2\t<system-reminder>prefer apply_patch</system-reminder>",
					}},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 2 {
		t.Fatalf("messages len=%d want 2, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	got := boltReq.Messages[1].Content
	if !strings.Contains(got, "123123") {
		t.Fatalf("expected read content to remain available for editing, got: %q", got)
	}
	if strings.Contains(got, "123123\n2") {
		t.Fatalf("expected trimmed blank-line marker to be removed from read history, got: %q", got)
	}
}

func TestIsBoltToolResultError_RecognizesHookDeniedWrite(t *testing.T) {
	block := prompt.ContentBlock{
		Type: "tool_result",
	}
	if !isBoltToolResultError(block, "Hook PreToolUse:Write denied this tool") {
		t.Fatal("expected hook-denied write result to be treated as an error")
	}
}

func TestPrepareRequest_DropsSupersededHookDeniedWriteResults(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_write_rel",
							Name:  "Write",
							Input: map[string]interface{}{"file_path": "calculator.py"},
						},
						{
							Type:  "tool_use",
							ID:    "tool_write_tmp",
							Name:  "Write",
							Input: map[string]interface{}{"file_path": "/tmp/cc-agent/sb1-demo/project/calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_write_rel",
							Content:   "Hook PreToolUse:Write denied this tool",
						},
						{
							Type:      "tool_result",
							ToolUseID: "tool_write_tmp",
							Content:   "File created successfully at: /tmp/cc-agent/sb1-demo/project/calculator.py",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 1 {
		t.Fatalf("messages len=%d want 1, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[0].Content; got != "帮我用python写一个计算器" {
		t.Fatalf("first message content=%q want original user prompt only", got)
	}
}

func TestPrepareRequest_DropsSandboxWriteResultsFromHistory(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "继续"}},
			{
				Role: "assistant",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool_write_tmp",
							Name:  "Write",
							Input: map[string]interface{}{"file_path": "/tmp/cc-agent/sb1-demo/project/calculator.py"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: prompt.MessageContent{
					Blocks: []prompt.ContentBlock{
						{
							Type:      "tool_result",
							ToolUseID: "tool_write_tmp",
							Content:   "File created successfully at: /tmp/cc-agent/sb1-demo/project/calculator.py",
						},
					},
				},
			},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 1 {
		t.Fatalf("messages len=%d want 1, messages=%#v", len(boltReq.Messages), boltReq.Messages)
	}
	if got := boltReq.Messages[0].Content; got != "继续" {
		t.Fatalf("first message content=%q want original user prompt only", got)
	}
}

func TestSupportedBoltToolNames_DoesNotDefaultWhenRequestOmitsTools(t *testing.T) {
	if got := supportedBoltToolNames(nil); got != nil {
		t.Fatalf("supportedBoltToolNames(nil) = %#v want nil", got)
	}
}

func TestSupportedBoltToolNames_DoesNotInventCoreToolsWhenOnlyUnsupportedToolsExist(t *testing.T) {
	tools := []interface{}{
		map[string]interface{}{"name": "TodoWrite"},
	}

	if got := supportedBoltToolNames(tools); got != nil {
		t.Fatalf("supportedBoltToolNames(unsupported) = %#v want nil", got)
	}
}

func TestSupportedBoltToolNames_AllowsSkill(t *testing.T) {
	tools := []interface{}{
		map[string]interface{}{"name": "Skill"},
		map[string]interface{}{"name": "Read"},
	}

	got := supportedBoltToolNames(tools)
	want := []string{"Read", "Skill"}
	if len(got) != len(want) {
		t.Fatalf("supportedBoltToolNames(skill) len=%d want=%d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("supportedBoltToolNames(skill)[%d]=%q want %q (%#v)", i, got[i], want[i], got)
		}
	}
}

func TestSupportedBoltToolNames_MapsAgentToTask(t *testing.T) {
	tools := []interface{}{
		map[string]interface{}{"name": "Read"},
		map[string]interface{}{"name": "Agent"},
	}

	got := supportedBoltToolNames(tools)
	want := []string{"Read", "Task"}
	if len(got) != len(want) {
		t.Fatalf("supportedBoltToolNames(agent) len=%d want=%d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("supportedBoltToolNames(agent)[%d]=%q want %q (%#v)", i, got[i], want[i], got)
		}
	}
}

func TestSupportedBoltToolNames_MapsCommonOpenClawAliases(t *testing.T) {
	tools := []interface{}{
		map[string]interface{}{"name": "read"},
		map[string]interface{}{"name": "write"},
		map[string]interface{}{"name": "edit"},
		map[string]interface{}{"name": "exec"},
		map[string]interface{}{"name": "glob"},
		map[string]interface{}{"name": "grep"},
		map[string]interface{}{"name": "sessions_spawn"},
		map[string]interface{}{"name": "use_skill"},
		map[string]interface{}{"name": "browser"},
	}

	got := supportedBoltToolNames(tools)
	want := []string{"Read", "Write", "Edit", "Bash", "Glob", "Grep", "Task", "Skill"}
	if len(got) != len(want) {
		t.Fatalf("supportedBoltToolNames(common aliases) len=%d want=%d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("supportedBoltToolNames(common aliases)[%d]=%q want %q (%#v)", i, got[i], want[i], got)
		}
	}
}

func TestPrepareRequest_AdvertisesTaskWhenClientDeclaresAgent(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []prompt.Message{{Role: "user", Content: prompt.MessageContent{Text: "explore this repo"}}},
		Tools: []interface{}{
			map[string]interface{}{"name": "Agent"},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if !strings.Contains(boltReq.GlobalSystemPrompt, "Task") {
		t.Fatalf("global prompt missing Task hint: %q", boltReq.GlobalSystemPrompt)
	}
}

func TestPrepareRequest_AdvertisesSkillWhenClientDeclaresSkill(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []prompt.Message{{Role: "user", Content: prompt.MessageContent{Text: "\u4eca\u5929\u626c\u5dde\u5929\u6c14\u600e\u4e48\u6837"}}},
		Tools: []interface{}{
			map[string]interface{}{"name": "Skill"},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if !strings.Contains(boltReq.GlobalSystemPrompt, "Skill") {
		t.Fatalf("global prompt missing Skill hint: %q", boltReq.GlobalSystemPrompt)
	}
}

func TestPrepareRequest_PreservesFreshTaskFirstPromptSignal(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model:         "claude-sonnet-4-6",
		IsFirstPrompt: true,
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if !boltReq.IsFirstPrompt {
		t.Fatal("expected prepareRequest to keep upstream first-prompt signal")
	}
}

func TestPrepareRequest_SkipsToolRoleMessages(t *testing.T) {
	req := upstream.UpstreamRequest{
		Model: "claude-opus-4-6",
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "hi"}},
			{Role: "assistant", Content: prompt.MessageContent{Text: "hello"}},
			{
				Role: "tool",
				Content: prompt.MessageContent{
					Text: "Model tried to call unavailable tool 'WebSearch'. Available tools: builtin_web_search.",
				},
			},
			{Role: "user", Content: prompt.MessageContent{Text: "现在上海的天气怎么样"}},
		},
	}

	boltReq, _ := prepareRequest(req, "sb1-demo")
	if len(boltReq.Messages) != 3 {
		t.Fatalf("messages len=%d want 3", len(boltReq.Messages))
	}
	for _, msg := range boltReq.Messages {
		if msg.Role == "tool" {
			t.Fatalf("unexpected tool role message in bolt request: %#v", msg)
		}
		if strings.Contains(msg.Content, "unavailable tool") {
			t.Fatalf("unexpected tool error content in bolt request: %#v", msg)
		}
	}
}

func TestFetchRootData_UsesSessionCookie(t *testing.T) {
	prevRootURL := boltRootDataURL
	t.Cleanup(func() { boltRootDataURL = prevRootURL })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "_data=root" {
			t.Fatalf("unexpected query: %q", r.URL.RawQuery)
		}
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "__session=session-token") {
			t.Fatalf("cookie=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"token":"root-token","user":{"id":"user_1","email":"bolt@example.com","totalBoltTokenPurchases":1000000}}`)
	}))
	defer srv.Close()
	boltRootDataURL = srv.URL + "?_data=root"

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	data, err := client.FetchRootData(context.Background())
	if err != nil {
		t.Fatalf("FetchRootData() error = %v", err)
	}
	if data.Token != "root-token" || data.User == nil || data.User.ID != "user_1" {
		t.Fatalf("unexpected root data: %+v", data)
	}
	if data.User.TotalBoltTokenPurchases != 1_000_000 {
		t.Fatalf("totalBoltTokenPurchases=%v want 1000000", data.User.TotalBoltTokenPurchases)
	}
}

func TestFetchRateLimits_UsesSessionCookieAndUserPath(t *testing.T) {
	prevRateURL := boltRateLimitsURL
	prevTeamsRateURL := boltTeamsRateLimitsURL
	t.Cleanup(func() {
		boltRateLimitsURL = prevRateURL
		boltTeamsRateLimitsURL = prevTeamsRateURL
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rate-limits/user" {
			t.Fatalf("unexpected path: %q", r.URL.Path)
		}
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "__session=session-token") {
			t.Fatalf("cookie=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"maxPerMonth":10000000,"regularTokens":{"available":10000000,"used":255061},"purchased":{"available":1000000,"used":0},"rewardTokens":{"available":0,"used":0},"specialTokens":{"available":0,"used":0},"referralTokens":{"free":{"available":0,"used":0},"paid":{"available":0,"used":0}},"totalThisMonth":255061,"totalToday":255061}`)
	}))
	defer srv.Close()

	boltRateLimitsURL = srv.URL + "/api/rate-limits/user"
	boltTeamsRateLimitsURL = srv.URL + "/api/rate-limits/teams"

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	data, err := client.FetchRateLimits(context.Background(), 0)
	if err != nil {
		t.Fatalf("FetchRateLimits() error = %v", err)
	}
	if data.MaxPerMonth != 10_000_000 {
		t.Fatalf("maxPerMonth=%v want 10000000", data.MaxPerMonth)
	}
	if data.RegularTokens == nil || data.RegularTokens.Used != 255061 {
		t.Fatalf("regularTokens=%+v", data.RegularTokens)
	}
}

func TestFetchRateLimits_UsesTeamPathWhenOrganizationSelected(t *testing.T) {
	prevRateURL := boltRateLimitsURL
	prevTeamsRateURL := boltTeamsRateLimitsURL
	t.Cleanup(func() {
		boltRateLimitsURL = prevRateURL
		boltTeamsRateLimitsURL = prevTeamsRateURL
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rate-limits/teams/42" {
			t.Fatalf("unexpected path: %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"maxPerMonth":26000000,"regularTokens":{"available":26000000,"used":0},"purchased":{"available":0,"used":0},"rewardTokens":{"available":0,"used":0},"specialTokens":{"available":0,"used":0},"referralTokens":{"free":{"available":0,"used":0},"paid":{"available":0,"used":0}},"totalThisMonth":0,"totalToday":0}`)
	}))
	defer srv.Close()

	boltRateLimitsURL = srv.URL + "/api/rate-limits/user"
	boltTeamsRateLimitsURL = srv.URL + "/api/rate-limits/teams"

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	data, err := client.FetchRateLimits(context.Background(), 42)
	if err != nil {
		t.Fatalf("FetchRateLimits() error = %v", err)
	}
	if data.MaxPerMonth != 26_000_000 {
		t.Fatalf("maxPerMonth=%v want 26000000", data.MaxPerMonth)
	}
}

func TestCreateEmptyProject_UsesBearerTokenAndReturnsSlug(t *testing.T) {
	prevProjectsURL := boltProjectsCreateURL
	t.Cleanup(func() { boltProjectsCreateURL = prevProjectsURL })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%q want POST", r.Method)
		}
		if r.URL.Path != "/api/projects/sb1/fork" {
			t.Fatalf("path=%q want /api/projects/sb1/fork", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer api-token" {
			t.Fatalf("authorization=%q want bearer token", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"appFiles":{}`) {
			t.Fatalf("request body missing empty appFiles: %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":64985691,"slug":"sb1-demo-new"}`)
	}))
	defer srv.Close()

	boltProjectsCreateURL = srv.URL + "/api/projects/sb1/fork"

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		Token:         "api-token",
		ProjectID:     "sb1-demo",
	}, nil)

	projectID, err := client.CreateEmptyProject(context.Background())
	if err != nil {
		t.Fatalf("CreateEmptyProject() error = %v", err)
	}
	if projectID != "sb1-demo-new" {
		t.Fatalf("projectID=%q want sb1-demo-new", projectID)
	}
}

func TestCreateEmptyProject_FetchesRootTokenWhenMissing(t *testing.T) {
	prevProjectsURL := boltProjectsCreateURL
	prevRootURL := boltRootDataURL
	t.Cleanup(func() {
		boltProjectsCreateURL = prevProjectsURL
		boltRootDataURL = prevRootURL
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/root":
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "__session=session-token") {
				t.Fatalf("cookie=%q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"token":"root-token"}`)
		case "/api/projects/sb1/fork":
			if got := r.Header.Get("Authorization"); got != "Bearer root-token" {
				t.Fatalf("authorization=%q want bearer root-token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"slug":"sb1-created-from-root"}`)
		default:
			t.Fatalf("unexpected path: %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	boltRootDataURL = srv.URL + "/root"
	boltProjectsCreateURL = srv.URL + "/api/projects/sb1/fork"

	client := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-token",
		ProjectID:     "sb1-demo",
	}, nil)

	projectID, err := client.CreateEmptyProject(context.Background())
	if err != nil {
		t.Fatalf("CreateEmptyProject() error = %v", err)
	}
	if projectID != "sb1-created-from-root" {
		t.Fatalf("projectID=%q want sb1-created-from-root", projectID)
	}
}

func TestNewFromAccount_ReusesSharedHTTPClient(t *testing.T) {
	cfg := &config.Config{
		RequestTimeout: 30,
		ProxyHTTP:      "http://proxy.local:3128",
		ProxyUser:      "user",
		ProxyPass:      "pass",
	}

	clientA := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-a",
		ProjectID:     "sb1-a",
	}, cfg)
	clientB := NewFromAccount(&store.Account{
		AccountType:   "bolt",
		SessionCookie: "session-b",
		ProjectID:     "sb1-b",
	}, cfg)

	if clientA.httpClient != clientB.httpClient {
		t.Fatal("expected bolt clients with same transport config to reuse shared http client")
	}
	if !clientA.sharedHTTPClient || !clientB.sharedHTTPClient {
		t.Fatal("expected sharedHTTPClient flag to be set")
	}
}
