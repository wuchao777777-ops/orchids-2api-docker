package handler

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-json"

	"orchids-api/internal/prompt"
)

const (
	boltSanitizeRecentMessageWindow = 4
	boltSanitizeMaxOldTextRunes     = 1200
	boltSanitizeMaxToolResultRunes  = 480
)

type boltSanitizeStats struct {
	Changed            bool
	MessagesBefore     int
	MessagesAfter      int
	BlocksRemoved      int
	ToolResultsTrimmed int
	TextBlocksTrimmed  int
	CharsBefore        int
	CharsAfter         int
}

func sanitizeBoltMessages(messages []prompt.Message) ([]prompt.Message, boltSanitizeStats) {
	stats := boltSanitizeStats{
		MessagesBefore: len(messages),
		CharsBefore:    estimateBoltMessageChars(messages),
	}
	if len(messages) == 0 {
		return nil, stats
	}

	cloned := cloneMessages(messages)
	toolNames := collectBoltToolNamesByID(cloned)
	recentStart := len(cloned) - boltSanitizeRecentMessageWindow
	if recentStart < 0 {
		recentStart = 0
	}

	out := make([]prompt.Message, 0, len(cloned))
	for i, msg := range cloned {
		if i >= recentStart {
			out = append(out, msg)
			continue
		}

		sanitized, msgChanged, removed, toolTrimmed, textTrimmed := sanitizeOldBoltMessage(msg, toolNames)
		if msgChanged {
			stats.Changed = true
		}
		stats.BlocksRemoved += removed
		stats.ToolResultsTrimmed += toolTrimmed
		stats.TextBlocksTrimmed += textTrimmed
		if isEffectivelyEmptyBoltMessage(sanitized) {
			stats.Changed = true
			continue
		}
		out = append(out, sanitized)
	}

	stats.MessagesAfter = len(out)
	stats.CharsAfter = estimateBoltMessageChars(out)
	if stats.MessagesAfter != stats.MessagesBefore || stats.CharsAfter != stats.CharsBefore {
		stats.Changed = true
	}
	return out, stats
}

func sanitizeOldBoltMessage(msg prompt.Message, toolNames map[string]string) (prompt.Message, bool, int, int, int) {
	changed := false
	removed := 0
	toolTrimmed := 0
	textTrimmed := 0

	if msg.Content.IsString() {
		trimmed, didTrim := compactBoltText(msg.Content.GetText(), boltSanitizeMaxOldTextRunes)
		if didTrim {
			changed = true
			textTrimmed++
		}
		msg.Content = prompt.MessageContent{Text: trimmed}
		return msg, changed, removed, toolTrimmed, textTrimmed
	}

	blocks := msg.Content.GetBlocks()
	if len(blocks) == 0 {
		return msg, false, 0, 0, 0
	}

	kept := make([]prompt.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		switch strings.ToLower(strings.TrimSpace(block.Type)) {
		case "tool_result":
			next, keep, didTrim := compactBoltToolResultBlock(block, toolNames[block.ToolUseID])
			if !keep {
				changed = true
				removed++
				continue
			}
			if didTrim {
				changed = true
				toolTrimmed++
			}
			kept = append(kept, next)
		case "text":
			block.Text = strings.TrimSpace(block.Text)
			if block.Text == "" {
				changed = true
				removed++
				continue
			}
			trimmed, didTrim := compactBoltText(block.Text, boltSanitizeMaxOldTextRunes)
			if didTrim {
				changed = true
				textTrimmed++
			}
			block.Text = trimmed
			kept = append(kept, block)
		default:
			kept = append(kept, block)
		}
	}

	msg.Content = prompt.MessageContent{Blocks: kept}
	return msg, changed, removed, toolTrimmed, textTrimmed
}

func compactBoltToolResultBlock(block prompt.ContentBlock, toolName string) (prompt.ContentBlock, bool, bool) {
	summary, kind, keep := summarizeBoltToolResult(block, toolName)
	if !keep {
		return block, false, false
	}

	original := strings.TrimSpace(extractBoltBlockContentText(block.Content))
	if original == summary {
		return block, true, false
	}

	block.Content = summary
	block.IsError = block.IsError
	if kind != "" {
		block.HasInput = block.HasInput
	}
	return block, true, true
}

func summarizeBoltToolResult(block prompt.ContentBlock, toolName string) (string, string, bool) {
	text := strings.TrimSpace(extractBoltBlockContentText(block.Content))
	lowerText := strings.ToLower(text)
	lowerTool := strings.ToLower(strings.TrimSpace(toolName))

	if text == "" {
		return "", "", false
	}

	switch lowerTool {
	case "web_search", "web_fetch":
		return fmt.Sprintf("[historical %s result omitted to reduce prompt size]", lowerTool), lowerTool, true
	case "read":
		pathHint := firstNonEmpty(
			extractBoltPathHint(text),
			extractBoltPathHintFromJSON(block.Content),
		)
		if isBoltReadResultSafeToDrop(pathHint, lowerText) {
			label := "historical Read result omitted"
			if pathHint != "" {
				label += ": " + pathHint
			}
			return "[" + label + "]", lowerTool, true
		}
	case "grep", "glob", "ls", "bash", "run", "shell", "terminal":
		if utf8.RuneCountInString(text) > boltSanitizeMaxToolResultRunes {
			return fmt.Sprintf("[historical %s result omitted to reduce prompt size]", strings.TrimSpace(toolName)), lowerTool, true
		}
	}

	trimmed, didTrim := compactBoltText(text, boltSanitizeMaxToolResultRunes)
	if didTrim {
		return trimmed, lowerTool, true
	}
	return text, lowerTool, true
}

func compactBoltText(text string, maxRunes int) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", text != ""
	}
	if maxRunes <= 0 || utf8.RuneCountInString(trimmed) <= maxRunes {
		return trimmed, trimmed != text
	}
	runes := []rune(trimmed)
	head := maxRunes
	if head < 32 {
		head = maxRunes
	}
	return strings.TrimSpace(string(runes[:head])) + "\n[trimmed historical context]", true
}

func collectBoltToolNamesByID(messages []prompt.Message) map[string]string {
	out := make(map[string]string)
	for _, msg := range messages {
		if msg.Content.Blocks == nil {
			continue
		}
		for _, block := range msg.Content.Blocks {
			if strings.EqualFold(strings.TrimSpace(block.Type), "tool_use") {
				id := strings.TrimSpace(block.ID)
				name := strings.TrimSpace(block.Name)
				if id != "" && name != "" {
					out[id] = name
				}
			}
		}
	}
	return out
}

func extractBoltBlockContentText(content interface{}) string {
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	case []string:
		return strings.Join(v, "\n")
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			itemText := strings.TrimSpace(extractBoltBlockContentText(item))
			if itemText != "" {
				parts = append(parts, itemText)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]interface{}:
		parts := make([]string, 0, 4)
		for _, key := range []string{"text", "content", "message", "output", "stdout", "stderr"} {
			if raw, ok := v[key]; ok {
				itemText := strings.TrimSpace(extractBoltBlockContentText(raw))
				if itemText != "" {
					parts = append(parts, itemText)
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(raw)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(raw)
	}
}

func extractBoltPathHint(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		switch {
		case strings.Contains(lower, "memory/") && strings.Contains(lower, ".md"):
			return trimmed
		case strings.Contains(lower, "skill.md"):
			return trimmed
		case strings.Contains(lower, ".openclaw/"):
			return trimmed
		case strings.Contains(lower, "workspace/memory/"):
			return trimmed
		}
	}
	return ""
}

func extractBoltPathHintFromJSON(content interface{}) string {
	if rawMap, ok := content.(map[string]interface{}); ok {
		for _, key := range []string{"file_path", "path", "pathname"} {
			if text, ok := rawMap[key].(string); ok {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func isBoltReadResultSafeToDrop(pathHint, lowerText string) bool {
	lowerPath := strings.ToLower(strings.TrimSpace(pathHint))
	for _, marker := range []string{
		"/workspace/memory/",
		"\\workspace\\memory\\",
		"/memory/",
		"\\memory\\",
		"skill.md",
		".openclaw/",
		"feishu-channel-rules",
		"tool_call_result",
		"tool_call_result>",
	} {
		if strings.Contains(lowerPath, marker) || strings.Contains(lowerText, marker) {
			return true
		}
	}
	return utf8.RuneCountInString(lowerText) > boltSanitizeMaxToolResultRunes*2
}

func isEffectivelyEmptyBoltMessage(msg prompt.Message) bool {
	if msg.Content.IsString() {
		return strings.TrimSpace(msg.Content.GetText()) == ""
	}
	for _, block := range msg.Content.GetBlocks() {
		switch strings.ToLower(strings.TrimSpace(block.Type)) {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func estimateBoltMessageChars(messages []prompt.Message) int {
	total := 0
	for _, msg := range messages {
		if msg.Content.IsString() {
			total += len(msg.Content.GetText())
			continue
		}
		for _, block := range msg.Content.GetBlocks() {
			total += len(block.Text)
			total += len(extractBoltBlockContentText(block.Content))
		}
	}
	return total
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
