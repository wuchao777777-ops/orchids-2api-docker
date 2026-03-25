package handler

import (
	"strings"

	"orchids-api/internal/bolt"
	"orchids-api/internal/prompt"
)

func shouldForceFreshBoltTask(req ClaudeRequest) bool {
	if isTopicClassifierRequest(req) || isTitleGenerationRequest(req) {
		return false
	}
	if lastUserIsToolResultFollowup(req.Messages) {
		return false
	}
	if shouldRestartBoltTaskFromHistory(req.Messages) {
		return true
	}
	return isStandaloneBoltPrompt(req.Messages)
}

func resetBoltMessagesForFreshTask(messages []prompt.Message) []prompt.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			return []prompt.Message{messages[i]}
		}
	}
	return messages
}

func isStandaloneBoltPrompt(messages []prompt.Message) bool {
	if len(messages) != 1 {
		return false
	}
	text, ok := extractStandaloneBoltUserText(messages[0])
	if !ok {
		return false
	}
	return !bolt.LooksLikeContinuationOnlyText(normalizeTopicText(stripSystemRemindersForMode(text)))
}

func shouldRestartBoltTaskFromHistory(messages []prompt.Message) bool {
	lastUserIdx, latestText, ok := latestStandaloneBoltUserPrompt(messages)
	if !ok {
		return false
	}
	if !looksLikeBoltFreshStartRequest(latestText) {
		return false
	}
	return hasSubstantiveBoltHistory(messages[:lastUserIdx])
}

func latestStandaloneBoltUserPrompt(messages []prompt.Message) (int, string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		text, ok := extractStandaloneBoltUserText(messages[i])
		if ok {
			return i, text, true
		}
		if strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			return -1, "", false
		}
	}
	return -1, "", false
}

func extractStandaloneBoltUserText(msg prompt.Message) (string, bool) {
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
		return "", false
	}
	if msg.Content.IsString() {
		text := strings.TrimSpace(stripSystemRemindersForMode(msg.Content.GetText()))
		return text, text != ""
	}

	blocks := msg.Content.GetBlocks()
	if len(blocks) == 0 {
		return "", false
	}

	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch strings.ToLower(strings.TrimSpace(block.Type)) {
		case "text":
			text := strings.TrimSpace(stripSystemRemindersForMode(block.Text))
			if text != "" {
				parts = append(parts, text)
			}
		default:
			return "", false
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), true
}

func looksLikeBoltFreshStartRequest(text string) bool {
	text = strings.TrimSpace(stripSystemRemindersForMode(text))
	if text == "" {
		return false
	}
	normalized := normalizeTopicText(text)
	if normalized == "" || bolt.LooksLikeContinuationOnlyText(normalized) {
		return false
	}

	markers := []string{
		"重新写", "重写", "重新做", "重做", "重建", "重新创建", "重新生成", "重新来", "重新开始", "从头开始", "从头写",
		"帮我写一个", "帮我写个", "帮我用", "写一个", "写个", "创建一个", "创建个", "新建一个", "新建个", "生成一个", "生成个",
		"做一个", "做个", "实现一个", "实现个",
		"start over", "start a new", "start new", "from scratch", "rewrite", "rebuild", "recreate",
		"create a", "create an", "create ", "build a", "build an", "build ", "write a", "write an", "write ",
	}
	lower := strings.ToLower(text)
	for _, marker := range markers {
		if strings.Contains(lower, marker) || strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func hasSubstantiveBoltHistory(messages []prompt.Message) bool {
	for _, msg := range messages {
		if msg.Content.IsString() {
			if strings.TrimSpace(stripSystemRemindersForMode(msg.Content.GetText())) != "" {
				return true
			}
			continue
		}

		for _, block := range msg.Content.GetBlocks() {
			switch strings.ToLower(strings.TrimSpace(block.Type)) {
			case "text":
				if strings.TrimSpace(stripSystemRemindersForMode(block.Text)) != "" {
					return true
				}
			case "tool_use", "tool_result", "thinking":
				return true
			default:
				if strings.TrimSpace(block.Type) != "" {
					return true
				}
			}
		}
	}
	return false
}
