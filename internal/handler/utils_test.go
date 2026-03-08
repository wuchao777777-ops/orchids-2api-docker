package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orchids-api/internal/prompt"
)

func TestConversationKeyForRequestPriority(t *testing.T) {
	baseReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://example.com/orchids/v1/messages", nil)
		r.RemoteAddr = "203.0.113.9:1234"
		r.Header.Set("User-Agent", "test-agent")
		return r
	}

	tests := []struct {
		name       string
		req        ClaudeRequest
		headerKey  string
		headerVal  string
		remoteAddr string
		userAgent  string
		want       string
	}{
		{
			name: "conversation_id highest priority",
			req: ClaudeRequest{
				ConversationID: "cid",
				Metadata: map[string]interface{}{
					"user_id": "u1",
				},
			},
			headerKey: "X-Conversation-Id",
			headerVal: "header",
			want:      "cid",
		},
		{
			name: "metadata conversation_id before header",
			req: ClaudeRequest{
				Metadata: map[string]interface{}{
					"conversation_id": "meta",
				},
			},
			headerKey: "X-Conversation-Id",
			headerVal: "header",
			want:      "meta",
		},
		{
			name: "header before metadata user_id",
			req: ClaudeRequest{
				Metadata: map[string]interface{}{
					"user_id": "u1",
				},
			},
			headerKey: "X-Conversation-Id",
			headerVal: "header",
			want:      "header",
		},
		{
			name: "no explicit session key returns empty",
			req: ClaudeRequest{
				Metadata: map[string]interface{}{
					"user_id": "u1",
				},
			},
			want: "",
		},
		{
			name: "no fallback to host and user agent",
			req:  ClaudeRequest{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := baseReq()
			if tt.headerKey != "" {
				r.Header.Set(tt.headerKey, tt.headerVal)
			}
			if tt.remoteAddr != "" {
				r.RemoteAddr = tt.remoteAddr
			}
			if tt.userAgent != "" {
				r.Header.Set("User-Agent", tt.userAgent)
			}
			if got := conversationKeyForRequest(r, tt.req); got != tt.want {
				t.Fatalf("conversationKeyForRequest() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractWorkdirFromRequestPriority(t *testing.T) {
	baseReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://example.com/warp/v1/messages", nil)
		return r
	}

	tests := []struct {
		name string
		req  ClaudeRequest
		hdr  map[string]string
		want string
		src  string
	}{
		{
			name: "metadata wins",
			req:  ClaudeRequest{Metadata: map[string]interface{}{"workdir": "/meta/path"}},
			hdr:  map[string]string{"X-Workdir": "/header/path"},
			want: "/meta/path",
			src:  "metadata",
		},
		{
			name: "header fallback",
			req:  ClaudeRequest{},
			hdr:  map[string]string{"X-Workdir": "/header/path"},
			want: "/header/path",
			src:  "header",
		},
		{
			name: "system fallback",
			req:  ClaudeRequest{System: SystemItems{{Type: "text", Text: "Primary working directory: /system/path"}}},
			want: "/system/path",
			src:  "system",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := baseReq()
			for k, v := range tt.hdr {
				r.Header.Set(k, v)
			}
			got, src := extractWorkdirFromRequest(r, tt.req)
			if got != tt.want || src != tt.src {
				t.Fatalf("extractWorkdirFromRequest() = (%q,%q), want (%q,%q)", got, src, tt.want, tt.src)
			}
		})
	}
}

func TestIsTopicClassifierRequest(t *testing.T) {
	req := ClaudeRequest{
		System: SystemItems{
			{
				Type: "text",
				Text: "Analyze if this message indicates a new conversation topic. Format your response as a JSON object with two fields: 'isNewTopic' and 'title'.",
			},
		},
	}
	if !isTopicClassifierRequest(req) {
		t.Fatalf("expected topic classifier request to be detected")
	}

	nonClassifier := ClaudeRequest{
		System: SystemItems{{Type: "text", Text: "You are Claude Code"}},
	}
	if isTopicClassifierRequest(nonClassifier) {
		t.Fatalf("expected non-topic-classifier request")
	}
}

func TestClassifyTopicRequest(t *testing.T) {
	tests := []struct {
		name      string
		messages  []prompt.Message
		wantIsNew bool
	}{
		{
			name: "single user message treated as new topic",
			messages: []prompt.Message{
				{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
			},
			wantIsNew: true,
		},
		{
			name: "same user message treated as same topic",
			messages: []prompt.Message{
				{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
				{Role: "assistant", Content: prompt.MessageContent{Text: "好的"}},
				{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
			},
			wantIsNew: false,
		},
		{
			name: "greeting treated as same topic",
			messages: []prompt.Message{
				{Role: "user", Content: prompt.MessageContent{Text: "帮我用python写一个计算器"}},
				{Role: "assistant", Content: prompt.MessageContent{Text: "好的"}},
				{Role: "user", Content: prompt.MessageContent{Text: "hi"}},
			},
			wantIsNew: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ClaudeRequest{Messages: tt.messages}
			gotNew, title := classifyTopicRequest(req)
			if gotNew != tt.wantIsNew {
				t.Fatalf("classifyTopicRequest() isNewTopic = %v, want %v", gotNew, tt.wantIsNew)
			}
			if gotNew && strings.TrimSpace(title) == "" {
				t.Fatalf("expected non-empty title for new topic")
			}
			if !gotNew && title != "" {
				t.Fatalf("expected empty title when not a new topic, got %q", title)
			}
		})
	}
}

func TestBuildLocalSuggestion(t *testing.T) {
	tests := []struct {
		name     string
		messages []prompt.Message
		want     string
	}{
		{
			name: "chinese follow up offer returns chinese suggestion",
			messages: []prompt.Message{
				{Role: "user", Content: prompt.MessageContent{Text: "继续处理这个问题"}},
				{Role: "assistant", Content: prompt.MessageContent{Text: "已经定位完了。如果你要，我下一步可以直接帮你提交修复。"}},
				{Role: "user", Content: prompt.MessageContent{Text: "[SUGGESTION MODE: Suggest what the user might naturally type next into Claude Code.]"}},
			},
			want: "可以",
		},
		{
			name: "non obvious next step stays silent",
			messages: []prompt.Message{
				{Role: "user", Content: prompt.MessageContent{Text: "当前运行的目录"}},
				{Role: "assistant", Content: prompt.MessageContent{Text: "当前运行目录：`/Users/dailin/Documents/GitHub/TEST`"}},
				{Role: "user", Content: prompt.MessageContent{Text: "[SUGGESTION MODE: Suggest what the user might naturally type next into Claude Code.]"}},
			},
			want: "",
		},
		{
			name: "english follow up offer returns english suggestion",
			messages: []prompt.Message{
				{Role: "user", Content: prompt.MessageContent{Text: "check the logs"}},
				{Role: "assistant", Content: prompt.MessageContent{Text: "I found the issue. If you'd like, I can restart the server and verify it."}},
				{Role: "user", Content: prompt.MessageContent{Text: "[SUGGESTION MODE: Suggest what the user might naturally type next into Claude Code.]"}},
			},
			want: "go ahead",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildLocalSuggestion(tt.messages); got != tt.want {
				t.Fatalf("buildLocalSuggestion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripSystemRemindersForMode_StripsLocalCommandMetadata(t *testing.T) {
	text := "<local-command-caveat>Caveat</local-command-caveat>\n<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args></command-args>\n<local-command-stdout>Set model to opus</local-command-stdout>\n[SUGGESTION MODE: Suggest what the user might naturally type next into Claude Code.]"
	got := stripSystemRemindersForMode(text)
	if strings.Contains(got, "<local-command-caveat>") || strings.Contains(got, "/model") || strings.Contains(got, "Set model to opus") {
		t.Fatalf("stripSystemRemindersForMode() should strip local command metadata, got %q", got)
	}
	if !strings.Contains(got, "[SUGGESTION MODE: Suggest what the user might naturally type next into Claude Code.]") {
		t.Fatalf("stripSystemRemindersForMode() should keep suggestion marker, got %q", got)
	}
}
