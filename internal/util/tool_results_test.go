package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizePersistedToolResultText_ExpandsSafePersistedOutput(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".claude", "projects", "demo", "tool-results")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(base, "tool.txt")
	if err := os.WriteFile(path, []byte("line one\nline two"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	raw := strings.Join([]string{
		"<persisted-output>",
		"Output too large. Full output saved to: " + path,
		"</persisted-output>",
	}, "\n")

	got := NormalizePersistedToolResultText(raw)
	if got != "line one\nline two" {
		t.Fatalf("NormalizePersistedToolResultText() = %q", got)
	}
}

func TestNormalizePersistedToolResultText_RejectsUnsafePath(t *testing.T) {
	raw := strings.Join([]string{
		"<persisted-output>",
		"Output too large. Full output saved to: /tmp/not-allowed.txt",
		"</persisted-output>",
	}, "\n")

	got := NormalizePersistedToolResultText(raw)
	if !strings.Contains(got, "Full output saved to: /tmp/not-allowed.txt") {
		t.Fatalf("expected unsafe path to remain unchanged, got %q", got)
	}
}
