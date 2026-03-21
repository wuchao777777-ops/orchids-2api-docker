package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goccy/go-json"

	"orchids-api/internal/bolt"
	"orchids-api/internal/config"
	"orchids-api/internal/prompt"
	"orchids-api/internal/upstream"
)

func TestHandleCountTokens_BoltUsesBoltEstimator(t *testing.T) {
	t.Parallel()

	h := NewWithLoadBalancer(&config.Config{DebugEnabled: false, DebugLogSSE: false}, nil)

	reqBody := ClaudeRequest{
		Model: "claude-opus-4-6",
		System: SystemItems{
			{Type: "text", Text: "keep this custom instruction"},
		},
		Messages: []prompt.Message{
			{Role: "user", Content: prompt.MessageContent{Text: "inspect project"}},
			{Role: "assistant", Content: prompt.MessageContent{Text: "I will inspect the repository."}},
		},
		Tools: []interface{}{
			map[string]interface{}{"name": "Read"},
			map[string]interface{}{"name": "Bash"},
		},
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://x/bolt/v1/messages/count_tokens", bytes.NewReader(raw))
	h.HandleCountTokens(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		InputTokens   int    `json:"input_tokens"`
		PromptProfile string `json:"prompt_profile"`
		Breakdown     struct {
			BasePromptTokens    int `json:"base_prompt_tokens"`
			SystemContextTokens int `json:"system_context_tokens"`
			HistoryTokens       int `json:"history_tokens"`
			ToolsTokens         int `json:"tools_tokens"`
		} `json:"breakdown"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}

	want := bolt.EstimateInputTokens(upstream.UpstreamRequest{
		Model:    reqBody.Model,
		Messages: reqBody.Messages,
		System:   []prompt.SystemItem(reqBody.System),
		Tools:    reqBody.Tools,
		NoTools:  len(reqBody.Tools) == 0,
	})

	if resp.PromptProfile != "bolt" {
		t.Fatalf("PromptProfile=%q want bolt", resp.PromptProfile)
	}
	if resp.InputTokens != want.Total {
		t.Fatalf("InputTokens=%d want %d", resp.InputTokens, want.Total)
	}
	if resp.Breakdown.BasePromptTokens != want.BasePromptTokens {
		t.Fatalf("BasePromptTokens=%d want %d", resp.Breakdown.BasePromptTokens, want.BasePromptTokens)
	}
	if resp.Breakdown.SystemContextTokens != want.SystemContextTokens {
		t.Fatalf("SystemContextTokens=%d want %d", resp.Breakdown.SystemContextTokens, want.SystemContextTokens)
	}
	if resp.Breakdown.HistoryTokens != want.HistoryTokens {
		t.Fatalf("HistoryTokens=%d want %d", resp.Breakdown.HistoryTokens, want.HistoryTokens)
	}
	if resp.Breakdown.ToolsTokens != want.ToolsTokens {
		t.Fatalf("ToolsTokens=%d want %d", resp.Breakdown.ToolsTokens, want.ToolsTokens)
	}
}
