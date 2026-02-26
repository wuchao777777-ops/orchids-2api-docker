package handler

import "testing"

func TestMapModel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Opus 4.6 系列
		{"claude-opus-4-6", "claude-opus-4-6"},
		{"claude-opus-4.6", "claude-opus-4-6"},
		{"claude-opus-4-6-thinking", "claude-opus-4-6"},
		{"claude-opus-4.6-thinking", "claude-opus-4-6"},

		// Opus 4.5 系列
		{"claude-opus-4-5", "claude-opus-4-6"},
		{"claude-opus-4.5", "claude-opus-4-6"},
		{"claude-opus-4-5-thinking", "claude-opus-4-5-thinking"},
		{"claude-opus-4.5-thinking", "claude-opus-4-5-thinking"},

		// Sonnet 3.7 精确版本
		{"claude-3-7-sonnet-20250219", "claude-3-7-sonnet-20250219"},

		// Sonnet 4.5 系列
		{"claude-sonnet-4-5", "claude-sonnet-4-6"},
		{"claude-sonnet-4.5", "claude-sonnet-4-6"},
		{"claude-sonnet-4-5-thinking", "claude-sonnet-4-5-thinking"},
		{"claude-sonnet-4.5-thinking", "claude-sonnet-4-5-thinking"},

		// Sonnet 4.6 系列
		{"claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"claude-sonnet-4.6", "claude-sonnet-4-6"},
		{"claude-sonnet-4-6-thinking", "claude-sonnet-4-6"},
		{"claude-sonnet-4.6-thinking", "claude-sonnet-4-6"},

		// Sonnet 4 精确版本号
		{"claude-sonnet-4-20250514", "claude-sonnet-4-20250514"},

		// Haiku 4.5 系列
		{"claude-haiku-4-5", "claude-haiku-4-5"},
		{"claude-haiku-4.5", "claude-haiku-4-5"},

		// Gemini
		{"gemini-3-flash", "gemini-3-flash"},
		{"gemini-3-pro", "gemini-3-pro"},

		// GPT
		{"gpt-5.3-codex", "gpt-5.3-codex"},
		{"gpt-5.2-codex", "gpt-5.2-codex"},
		{"gpt-5.2", "gpt-5.2"},

		// 其他模型
		{"grok-4.1-fast", "grok-4.1-fast"},
		{"glm-5", "glm-5"},
		{"kimi-k2.5", "kimi-k2.5"},

		// 默认
		{"", "claude-sonnet-4-6"},
		{"unknown-model", "claude-sonnet-4-6"},

		// 大小写混合
		{"Claude-Opus-4-5", "claude-opus-4-6"},
		{"CLAUDE-SONNET-4-5-THINKING", "claude-sonnet-4-5-thinking"},
		{"Claude-Haiku-4.5", "claude-haiku-4-5"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mapModel(tt.input)
			if got != tt.want {
				t.Errorf("mapModel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
