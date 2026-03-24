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
	msg := messages[0]
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
		return false
	}
	if !msg.Content.IsString() {
		blocks := msg.Content.GetBlocks()
		if len(blocks) == 0 {
			return false
		}
		hasText := false
		for _, block := range blocks {
			switch strings.ToLower(strings.TrimSpace(block.Type)) {
			case "text":
				if strings.TrimSpace(stripSystemRemindersForMode(block.Text)) != "" {
					hasText = true
				}
			default:
				return false
			}
		}
		if !hasText {
			return false
		}
	}

	text := strings.TrimSpace(stripSystemRemindersForMode(msg.ExtractText()))
	if text == "" {
		return false
	}
	return !bolt.LooksLikeContinuationOnlyText(normalizeTopicText(stripSystemRemindersForMode(text)))
}
