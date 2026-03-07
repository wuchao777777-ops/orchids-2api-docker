package orchids

import (
	"testing"

	"github.com/goccy/go-json"

	"orchids-api/internal/tiktoken"
)

func TestFallbackOrchidsToolCallID(t *testing.T) {
	id1 := fallbackOrchidsToolCallID("Write", `{"file_path":"/tmp/a.txt","content":"x"}`)
	id2 := fallbackOrchidsToolCallID("write", `{"file_path":"/tmp/a.txt","content":"x"}`)
	if id1 == "" {
		t.Fatalf("expected non-empty fallback id")
	}
	if id1 != id2 {
		t.Fatalf("expected stable/lowercased id, got %q vs %q", id1, id2)
	}
	if len(id1) < len("orchids_anon_") || id1[:len("orchids_anon_")] != "orchids_anon_" {
		t.Fatalf("unexpected prefix: %q", id1)
	}
}

func TestFallbackOrchidsToolCallID_EmptyTool(t *testing.T) {
	if got := fallbackOrchidsToolCallID("", `{}`); got != "" {
		t.Fatalf("expected empty id for empty tool name, got %q", got)
	}
}

func legacyEstimateCompactedToolsTokens(tools []interface{}) int {
	compacted := compactIncomingTools(tools)
	if len(compacted) == 0 {
		return 0
	}
	raw, err := json.Marshal(compacted)
	if err != nil {
		return 0
	}
	return tiktoken.EstimateTextTokens(string(raw))
}

func sampleIncomingTools() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "Write",
				"description": "write file content safely",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"file_path": map[string]interface{}{"type": "string"},
						"content":   map[string]interface{}{"type": "string", "description": "utf-8 内容"},
					},
				},
			},
		},
		map[string]interface{}{
			"name":        "Read",
			"description": "read file content",
			"input_schema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"file_path": map[string]interface{}{"type": "string"},
				},
			},
		},
	}
}

func TestEstimateCompactedToolsTokensMatchesLegacy(t *testing.T) {
	tools := sampleIncomingTools()
	if got, want := EstimateCompactedToolsTokens(tools), legacyEstimateCompactedToolsTokens(tools); got != want {
		t.Fatalf("EstimateCompactedToolsTokens=%d want=%d", got, want)
	}
}

func BenchmarkEstimateCompactedToolsTokens_Legacy(b *testing.B) {
	tools := sampleIncomingTools()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = legacyEstimateCompactedToolsTokens(tools)
	}
}

func BenchmarkEstimateCompactedToolsTokens_Current(b *testing.B) {
	tools := sampleIncomingTools()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = EstimateCompactedToolsTokens(tools)
	}
}
