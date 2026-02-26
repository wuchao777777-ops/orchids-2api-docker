package handler

import (
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"orchids-api/internal/prompt"
)

var envWorkdirRegex = regexp.MustCompile(`(?i)(?:primary\s+)?working directory:\s*([^\n\r]+)`)

func extractWorkdirFromSystem(system []prompt.SystemItem) string {
	for _, item := range system {
		if item.Type == "text" {
			matches := envWorkdirRegex.FindStringSubmatch(item.Text)
			if len(matches) > 1 {
				return strings.TrimSpace(matches[1])
			}
		}
	}
	return ""
}

func extractWorkdirFromRequest(r *http.Request, req ClaudeRequest) (string, string) {
	if req.Metadata != nil {
		if wd := metadataString(req.Metadata,
			"workdir", "working_directory", "workingDirectory", "cwd",
			"workspace", "workspace_path", "workspacePath",
			"project_root", "projectRoot",
		); wd != "" {
			return strings.TrimSpace(wd), "metadata"
		}
	}

	if wd := headerValue(r,
		"X-Workdir", "X-Working-Directory", "X-Cwd", "X-Workspace", "X-Project-Root",
	); wd != "" {
		return strings.TrimSpace(wd), "header"
	}

	if wd := extractWorkdirFromSystem(req.System); wd != "" {
		return strings.TrimSpace(wd), "system"
	}

	return "", ""
}

func channelFromPath(path string) string {
	if strings.HasPrefix(path, "/orchids/") {
		return "orchids"
	}
	if strings.HasPrefix(path, "/warp/") {
		return "warp"
	}
	if strings.HasPrefix(path, "/grok/v1/") {
		return "grok"
	}
	return ""
}

// mapModel 根据请求的 model 名称映射到 orchids 上游实际支持的模型
// 以当前 Orchids 公共模型为准（会随上游更新）：claude-sonnet-4-6 / claude-opus-4.6 / claude-haiku-4-5 等。
func mapModel(requestModel string) string {
	normalized := normalizeOrchidsModelKey(requestModel)
	if normalized == "" {
		return "claude-sonnet-4-6"
	}
	if mapped, ok := orchidsModelMap[normalized]; ok {
		return mapped
	}
	return "claude-sonnet-4-6"
}

func normalizeOrchidsModelKey(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(normalized, "claude-") {
		normalized = strings.ReplaceAll(normalized, "4.6", "4-6")
		normalized = strings.ReplaceAll(normalized, "4.5", "4-5")
	}
	return normalized
}

var orchidsModelMap = map[string]string{
	"claude-sonnet-4-5":          "claude-sonnet-4-6",
	"claude-sonnet-4-6":          "claude-sonnet-4-6",
	"claude-sonnet-4-5-thinking": "claude-sonnet-4-5-thinking",
	"claude-sonnet-4-6-thinking": "claude-sonnet-4-6",
	"claude-opus-4-6":            "claude-opus-4-6",
	"claude-opus-4-5":            "claude-opus-4-6",
	"claude-opus-4-5-thinking":   "claude-opus-4-5-thinking",
	"claude-opus-4-6-thinking":   "claude-opus-4-6",
	"claude-haiku-4-5":           "claude-haiku-4-5",
	"claude-sonnet-4-20250514":   "claude-sonnet-4-20250514",
	"claude-3-7-sonnet-20250219": "claude-3-7-sonnet-20250219",
	"gemini-3-flash":             "gemini-3-flash",
	"gemini-3-pro":               "gemini-3-pro",
	"gpt-5.3-codex":              "gpt-5.3-codex",
	"gpt-5.2-codex":              "gpt-5.2-codex",
	"gpt-5.2":                    "gpt-5.2",
	"grok-4.1-fast":              "grok-4.1-fast",
	"glm-5":                      "glm-5",
	"kimi-k2.5":                  "kimi-k2.5",
}

func conversationKeyForRequest(r *http.Request, req ClaudeRequest) string {
	if req.ConversationID != "" {
		return req.ConversationID
	}
	if req.Metadata != nil {
		if key := metadataString(req.Metadata, "conversation_id", "conversationId", "session_id", "sessionId", "thread_id", "threadId", "chat_id", "chatId"); key != "" {
			return key
		}
	}
	if key := headerValue(r, "X-Conversation-Id", "X-Session-Id", "X-Thread-Id", "X-Chat-Id"); key != "" {
		return key
	}
	return ""
}

func metadataString(metadata map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := metadata[key]; ok {
			if str, ok := value.(string); ok {
				str = strings.TrimSpace(str)
				if str != "" {
					return str
				}
			}
		}
	}
	return ""
}

func headerValue(r *http.Request, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(r.Header.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func extractUserText(messages []prompt.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "user" {
			continue
		}
		if msg.Content.IsString() {
			return strings.TrimSpace(msg.Content.GetText())
		}
		var parts []string
		for _, block := range msg.Content.GetBlocks() {
			if block.Type == "text" {
				text := strings.TrimSpace(block.Text)
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

func lastUserIsToolResultOnly(messages []prompt.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "user" {
			continue
		}
		if msg.Content.IsString() {
			return false
		}
		blocks := msg.Content.GetBlocks()
		hasToolResult := false
		for _, block := range blocks {
			switch block.Type {
			case "tool_result":
				hasToolResult = true
			case "text":
				if strings.TrimSpace(block.Text) != "" {
					return false
				}
			default:
				if strings.TrimSpace(block.Type) != "" {
					return false
				}
			}
		}
		return hasToolResult
	}
	return false
}

func isSuggestionMode(messages []prompt.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "user" {
			continue
		}
		if msg.Content.IsString() {
			return containsSuggestionMode(msg.Content.GetText())
		}
		for _, block := range msg.Content.GetBlocks() {
			if block.Type == "text" {
				return containsSuggestionMode(block.Text)
			}
		}
		// 最近一条 user 消息没有文本内容，避免回溯旧的 suggestion prompt 误判
		return false
	}
	return false
}

func containsSuggestionMode(text string) bool {
	clean := stripSystemRemindersForMode(text)
	return strings.Contains(strings.ToLower(clean), "suggestion mode")
}

func isTopicClassifierRequest(req ClaudeRequest) bool {
	for _, item := range req.System {
		if strings.ToLower(strings.TrimSpace(item.Type)) != "text" {
			continue
		}
		lower := strings.ToLower(stripSystemRemindersForMode(item.Text))
		if strings.Contains(lower, "new conversation topic") &&
			strings.Contains(lower, "isnewtopic") &&
			strings.Contains(lower, "json object") &&
			strings.Contains(lower, "title") {
			return true
		}
	}
	return false
}

func classifyTopicRequest(req ClaudeRequest) (bool, string) {
	userTexts := extractUserTexts(req.Messages)
	if len(userTexts) == 0 {
		return false, ""
	}

	latest := strings.TrimSpace(userTexts[len(userTexts)-1])
	if latest == "" {
		return false, ""
	}

	prev := ""
	if len(userTexts) >= 2 {
		prev = strings.TrimSpace(userTexts[len(userTexts)-2])
	}

	if prev == "" {
		return true, generateTopicTitle(latest)
	}

	if isGreetingText(latest) {
		return false, ""
	}

	latestNorm := normalizeTopicText(latest)
	prevNorm := normalizeTopicText(prev)
	if latestNorm == "" || prevNorm == "" {
		return latest != prev, generateTopicTitle(latest)
	}
	if latestNorm == prevNorm || strings.Contains(latestNorm, prevNorm) || strings.Contains(prevNorm, latestNorm) {
		return false, ""
	}
	return true, generateTopicTitle(latest)
}

func extractUserTexts(messages []prompt.Message) []string {
	texts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if strings.ToLower(strings.TrimSpace(msg.Role)) != "user" {
			continue
		}
		if msg.Content.IsString() {
			text := strings.TrimSpace(stripSystemRemindersForMode(msg.Content.GetText()))
			if text != "" {
				texts = append(texts, text)
			}
			continue
		}
		var parts []string
		for _, block := range msg.Content.GetBlocks() {
			if strings.ToLower(strings.TrimSpace(block.Type)) != "text" {
				continue
			}
			text := strings.TrimSpace(stripSystemRemindersForMode(block.Text))
			if text != "" {
				parts = append(parts, text)
			}
		}
		merged := strings.TrimSpace(strings.Join(parts, "\n"))
		if merged != "" {
			texts = append(texts, merged)
		}
	}
	return texts
}

func isGreetingText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch lower {
	case "hi", "hello", "hey", "你好", "您好", "嗨", "在吗":
		return true
	default:
		return false
	}
}

func normalizeTopicText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func generateTopicTitle(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "New Topic"
	}
	words := strings.Fields(trimmed)
	if len(words) >= 2 {
		if len(words) > 3 {
			words = words[:3]
		}
		return strings.Join(words, " ")
	}
	runes := []rune(trimmed)
	if len(runes) > 10 {
		runes = runes[:10]
	}
	return strings.TrimSpace(string(runes))
}

// stripSystemRemindersForMode 移除 <system-reminder>...</system-reminder>，避免误判 plan/suggestion 模式
// 使用 LastIndex 查找结束标签，正确处理嵌套的字面量标签
func stripSystemRemindersForMode(text string) string {
	const startTag = "<system-reminder>"
	const endTag = "</system-reminder>"
	if !strings.Contains(text, startTag) {
		return text
	}
	var sb strings.Builder
	sb.Grow(len(text))
	i := 0
	for i < len(text) {
		start := strings.Index(text[i:], startTag)
		if start == -1 {
			sb.WriteString(text[i:])
			break
		}
		sb.WriteString(text[i : i+start])
		blockStart := i + start
		endStart := blockStart + len(startTag)
		end := strings.LastIndex(text[endStart:], endTag)
		if end == -1 {
			sb.WriteString(text[blockStart:])
			break
		}
		i = endStart + end + len(endTag)
	}
	return sb.String()
}
