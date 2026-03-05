package orchids

import "testing"

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
