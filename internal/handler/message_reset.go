package handler

import (
	"fmt"
	"strings"

	"orchids-api/internal/prompt"
)

// resetMessagesForNewWorkdir 在工作目录切换时保留当前用户消息，
// 并将之前的对话历史压缩为摘要注入到消息中，避免完全丢失上下文。
func resetMessagesForNewWorkdir(messages []prompt.Message) []prompt.Message {
	if len(messages) == 0 {
		return messages
	}

	// 找到最后一条用户消息
	lastUserIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(messages[i].Role, "user") {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx < 0 {
		return []prompt.Message{}
	}

	// 如果没有历史消息需要摘要，直接返回
	if lastUserIdx == 0 {
		return []prompt.Message{messages[lastUserIdx]}
	}

	// 构建简短摘要
	older := messages[:lastUserIdx]
	summary := buildWorkdirChangeSummary(older)

	if summary == "" {
		return []prompt.Message{messages[lastUserIdx]}
	}

	// 注入摘要作为 user 消息，让模型知道之前的上下文
	summaryMsg := prompt.Message{
		Role:    "user",
		Content: prompt.MessageContent{Text: fmt.Sprintf("[Previous conversation summary before working directory change]\n%s", summary)},
	}
	return []prompt.Message{summaryMsg, messages[lastUserIdx]}
}

// buildWorkdirChangeSummary 从历史消息中提取关键上下文摘要。
func buildWorkdirChangeSummary(messages []prompt.Message) string {
	if len(messages) == 0 {
		return ""
	}

	var parts []string
	for _, msg := range messages {
		text := msg.ExtractText()
		if text == "" {
			continue
		}
		// 截断过长的单条消息
		if len(text) > 200 {
			text = text[:200] + "..."
		}
		parts = append(parts, fmt.Sprintf("- %s: %s", msg.Role, text))
	}

	if len(parts) == 0 {
		return ""
	}

	// 限制总摘要条数，避免过长
	if len(parts) > 10 {
		parts = parts[len(parts)-10:]
	}

	return strings.Join(parts, "\n")
}


