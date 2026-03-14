package util

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const PersistedToolResultMaxBytes = 256 * 1024

// NormalizePersistedToolResultText expands Claude Code persisted-output wrappers
// into the saved tool-result body when the referenced path is safe to read.
func NormalizePersistedToolResultText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if expanded, ok := expandPersistedToolResultText(text); ok {
		return expanded
	}
	return text
}

func expandPersistedToolResultText(text string) (string, bool) {
	path, ok := extractPersistedToolResultPath(text)
	if !ok || !isSafePersistedToolResultPath(path) {
		return "", false
	}

	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return "", false
	}

	truncated := false
	if len(data) > PersistedToolResultMaxBytes {
		data = data[:PersistedToolResultMaxBytes]
		truncated = true
	}

	expanded := strings.TrimSpace(string(data))
	if expanded == "" {
		return "", false
	}
	if truncated {
		expanded += fmt.Sprintf("\n[persisted output truncated after %d bytes]", PersistedToolResultMaxBytes)
	}
	return expanded, true
}

func extractPersistedToolResultPath(text string) (string, bool) {
	const marker = "Full output saved to:"

	idx := strings.Index(text, marker)
	if idx < 0 {
		return "", false
	}
	rest := text[idx+len(marker):]
	line := rest
	if nl := strings.IndexAny(rest, "\r\n"); nl >= 0 {
		line = rest[:nl]
	}
	path := strings.TrimSpace(line)
	if path == "" {
		return "", false
	}
	return path, true
}

func isSafePersistedToolResultPath(path string) bool {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || !filepath.IsAbs(clean) {
		return false
	}
	if !strings.EqualFold(filepath.Ext(clean), ".txt") {
		return false
	}
	if !strings.EqualFold(filepath.Base(filepath.Dir(clean)), "tool-results") {
		return false
	}
	return pathContainsOrderedComponents(clean, ".claude", "projects")
}

func pathContainsOrderedComponents(path string, markers ...string) bool {
	if len(markers) == 0 {
		return true
	}
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	match := 0
	for _, part := range parts {
		if strings.EqualFold(part, markers[match]) {
			match++
			if match == len(markers) {
				return true
			}
		}
	}
	return false
}
