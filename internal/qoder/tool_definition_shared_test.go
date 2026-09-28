package qoder

import (
	"reflect"
	"testing"

	"github.com/goccy/go-json"
	"orchids-api/internal/upstream"
	"orchids-api/internal/util"
)

// TestNormalizeToolDefinitionsMatchesSharedHelper pins the shared
// normalization: Qoder no longer carries its own copy of the loop, so any
// drift between the channels would silently change one upstream's wire format.
func TestNormalizeToolDefinitionsMatchesSharedHelper(t *testing.T) {
	declarations := []interface{}{
		// Already in the OpenAI function envelope: kept as-is with type forced.
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "openai_tool",
				"description": "already normalized",
				"parameters":  map[string]interface{}{"type": "object", "properties": map[string]interface{}{"a": map[string]interface{}{"type": "string"}}},
			},
		},
		// Anthropic shape: name + input_schema -> function envelope.
		map[string]interface{}{
			"name":        "anthropic_tool",
			"description": "converted",
			"input_schema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}},
				"required":   []interface{}{"path"},
			},
		},
		// Anthropic shape without input_schema: default empty object schema.
		map[string]interface{}{"name": "no_schema"},
		// Names are trimmed; an empty name is dropped either way.
		map[string]interface{}{"name": "  spaced_name  "},
		map[string]interface{}{"name": "   "},
		map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": ""}},
		// Unknown shapes and non-objects are dropped.
		map[string]interface{}{"unrelated": true},
		"not-a-map",
		42,
		nil,
	}

	qoderGot := normalizeToolDefinitions(upstream.UpstreamRequest{Tools: declarations}, modelEntry{Key: "k"})
	workbuddyGot := util.NormalizeToolDefinitions(declarations)

	qoderJSON, err := json.Marshal(qoderGot)
	if err != nil {
		t.Fatalf("marshal qoder tools: %v", err)
	}
	workbuddyJSON, err := json.Marshal(workbuddyGot)
	if err != nil {
		t.Fatalf("marshal workbuddy tools: %v", err)
	}
	if !reflect.DeepEqual(qoderGot, workbuddyGot) {
		t.Fatalf("qoder and workbuddy normalization diverged:\nqoder:     %s\nworkbuddy: %s", qoderJSON, workbuddyJSON)
	}
	if len(qoderGot) != 4 {
		t.Fatalf("expected 4 kept declarations (openai envelope, anthropic schema, default schema, trimmed name), got %d: %s", len(qoderGot), qoderJSON)
	}

	// The NoTools / empty guard is Qoder's own behavior, not the shared helper's.
	if got := normalizeToolDefinitions(upstream.UpstreamRequest{NoTools: true, Tools: declarations}, modelEntry{Key: "k"}); got != nil {
		t.Fatalf("NoTools must return nil, got %#v", got)
	}
	if got := normalizeToolDefinitions(upstream.UpstreamRequest{}, modelEntry{Key: "k"}); got != nil {
		t.Fatalf("no declarations must return nil, got %#v", got)
	}
	// The shared helper itself stays non-nil so WorkBuddy's empty case is unchanged.
	if got := util.NormalizeToolDefinitions(nil); got == nil || len(got) != 0 {
		t.Fatalf("shared helper must return an empty non-nil slice for no tools, got %#v", got)
	}
}
