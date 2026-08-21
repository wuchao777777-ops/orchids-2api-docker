package puter

import (
	"testing"
)

func TestNormalizeStreamToolInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: "{}"},
		{name: "null", raw: "null", want: "{}"},
		{name: "object", raw: `{"query":"SpaceX latest news"}`, want: `{"query":"SpaceX latest news"}`},
		{name: "object-native-tool", raw: `{"file_path":"README.md"}`, want: `{"file_path":"README.md"}`},
		{name: "json-string", raw: `"{\"file_path\":\"README.md\"}"`, want: `{"file_path":"README.md"}`},
		{name: "openai-wrapper", raw: `{"arguments":"{\"file_path\":\"README.md\"}"}`, want: `{"file_path":"README.md"}`},
		// 上游把 arguments 又包了一层字面引号（日志中观察到的形态）。
		{name: "openai-wrapper-quoted", raw: `{"arguments":"\"{\\\"file_path\\\":\\\"README.md\\\"}\""}`, want: `{"file_path":"README.md"}`},
		// 再深一层：整个 input 是 JSON 字符串，内容又是 {"arguments":"..."} 包装。
		{name: "string-wrapped-openai", raw: `"{\"arguments\":\"{\\\"file_path\\\":\\\"README.md\\\"}\"}"`, want: `{"file_path":"README.md"}`},
		// arguments 不是合法 JSON 时保持原样，不误伤本地工具。
		{name: "plain-arguments-kept", raw: `{"arguments":"just a plain string"}`, want: `{"arguments":"just a plain string"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeStreamToolInput([]byte(tt.raw))
			if got != tt.want {
				t.Fatalf("normalizeStreamToolInput(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
