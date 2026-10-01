package util

import (
	"orchids-api/internal/testutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizePersistedToolResultText_ExpandsSafePersistedOutput(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".claude", "projects", "demo", "tool-results")
	testutil.NoError(t, os.MkdirAll(base, 0o755), "mkdir: %v")
	path := filepath.Join(base, "tool.txt")
	testutil.NoError(t, os.WriteFile(path, []byte("line one\nline two"), 0o644), "write file: %v")

	raw := strings.Join([]string{
		"<persisted-output>",
		"Output too large. Full output saved to: " + path,
		"</persisted-output>",
	}, "\n")

	got := NormalizePersistedToolResultText(raw)
	testutil.Equal(t, got, "line one\nline two")
}

func TestNormalizePersistedToolResultText_RejectsUnsafePath(t *testing.T) {
	raw := strings.Join([]string{
		"<persisted-output>",
		"Output too large. Full output saved to: /tmp/not-allowed.txt",
		"</persisted-output>",
	}, "\n")

	got := NormalizePersistedToolResultText(raw)
	testutil.MustContain(t, got, "Full output saved to: /tmp/not-allowed.txt")
}
