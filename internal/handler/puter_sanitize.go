package handler

import (
	"strings"

	"orchids-api/internal/prompt"
)

func sanitizePuterMessages(messages []prompt.Message) []prompt.Message {
	if len(messages) == 0 {
		return nil
	}

	cloned := cloneMessages(messages)
	out := make([]prompt.Message, 0, len(cloned))
	for _, msg := range cloned {
		if msg.Content.IsString() {
			text := strings.TrimSpace(stripSystemRemindersForMode(msg.Content.GetText()))
			if text == "" {
				continue
			}
			msg.Content = prompt.MessageContent{Text: text}
			out = append(out, msg)
			continue
		}

		blocks := msg.Content.GetBlocks()
		kept := make([]prompt.ContentBlock, 0, len(blocks))
		for _, block := range blocks {
			switch strings.ToLower(strings.TrimSpace(block.Type)) {
			case "text":
				text := strings.TrimSpace(stripSystemRemindersForMode(block.Text))
				if text == "" {
					continue
				}
				block.Text = text
			}
			kept = append(kept, block)
		}
		if len(kept) == 0 {
			continue
		}
		msg.Content = prompt.MessageContent{Blocks: kept}
		out = append(out, msg)
	}

	return out
}
