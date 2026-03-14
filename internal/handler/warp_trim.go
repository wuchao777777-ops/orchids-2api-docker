package handler

import (
	"fmt"
	"github.com/goccy/go-json"
	"log/slog"
	"unicode/utf8"

	"orchids-api/internal/prompt"
)

type toolResultRef struct {
	msgIndex   int
	blockIndex int
}

type warpToolResultBatch struct {
	Messages []prompt.Message
}

func splitWarpToolResults(messages []prompt.Message, batchSize int) ([]warpToolResultBatch, int) {
	if batchSize <= 0 {
		return []warpToolResultBatch{{Messages: cloneMessages(messages)}}, 0
	}

	turnIndex := lastToolResultTurnIndex(messages)
	if turnIndex < 0 {
		return []warpToolResultBatch{{Messages: cloneMessages(messages)}}, 0
	}

	refs := collectToolResultRefsForTurn(messages, turnIndex)
	total := len(refs)
	if total <= batchSize {
		return []warpToolResultBatch{{Messages: cloneMessages(messages)}}, total
	}

	var batches []warpToolResultBatch
	for end := batchSize; end <= total; end += batchSize {
		if end > total {
			end = total
		}
		keep := make(map[toolResultRef]struct{}, end)
		for _, ref := range refs[:end] {
			keep[ref] = struct{}{}
		}
		keepUserText := end == total
		batches = append(batches, warpToolResultBatch{
			Messages: filterToolResults(messages, turnIndex, keep, keepUserText),
		})
	}
	if total%batchSize != 0 {
		end := total
		if end > total {
			end = total
		}
		keep := make(map[toolResultRef]struct{}, end)
		for _, ref := range refs[:end] {
			keep[ref] = struct{}{}
		}
		batches = append(batches, warpToolResultBatch{
			Messages: filterToolResults(messages, turnIndex, keep, true),
		})
	}

	return batches, total
}

func lastToolResultTurnIndex(messages []prompt.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "user" || msg.Content.Blocks == nil {
			continue
		}
		for _, block := range msg.Content.Blocks {
			if block.Type == "tool_result" {
				return i
			}
		}
	}
	return -1
}

func collectToolResultRefsForTurn(messages []prompt.Message, turnIndex int) []toolResultRef {
	if turnIndex < 0 || turnIndex >= len(messages) {
		return nil
	}
	var refs []toolResultRef
	msg := messages[turnIndex]
	if msg.Content.Blocks == nil {
		return nil
	}
	for j, block := range msg.Content.Blocks {
		if block.Type == "tool_result" {
			refs = append(refs, toolResultRef{msgIndex: turnIndex, blockIndex: j})
		}
	}
	return refs
}

func filterToolResults(messages []prompt.Message, turnIndex int, keep map[toolResultRef]struct{}, keepUserText bool) []prompt.Message {
	trimmed := cloneMessages(messages)
	kept := make([]prompt.Message, 0, len(trimmed))

	for i, msg := range trimmed {
		if msg.Content.Blocks == nil {
			kept = append(kept, msg)
			continue
		}
		blocks := msg.Content.Blocks
		newBlocks := make([]prompt.ContentBlock, 0, len(blocks))
		for j, block := range blocks {
			if i == turnIndex && block.Type == "tool_result" {
				if _, ok := keep[toolResultRef{msgIndex: i, blockIndex: j}]; !ok {
					continue
				}
			}
			if i == turnIndex && block.Type == "text" && !keepUserText {
				continue
			}
			newBlocks = append(newBlocks, block)
		}
		msg.Content.Blocks = newBlocks
		if msg.Content.Text == "" && len(newBlocks) == 0 {
			continue
		}
		kept = append(kept, msg)
	}
	return kept
}

func cloneMessages(messages []prompt.Message) []prompt.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]prompt.Message, len(messages))
	for i, msg := range messages {
		out[i] = msg
		if msg.Content.Blocks == nil {
			continue
		}
		blocks := make([]prompt.ContentBlock, len(msg.Content.Blocks))
		copy(blocks, msg.Content.Blocks)
		out[i].Content.Blocks = blocks
	}
	return out
}

func compressToolResults(messages []prompt.Message, maxLen int, channel string) ([]prompt.Message, int) {
	if maxLen <= 0 {
		return messages, 0
	}
	compressed := cloneMessages(messages)
	compressedCount := 0

	for i := range compressed {
		msg := &compressed[i]
		if msg.Role != "user" || msg.Content.Blocks == nil {
			continue
		}

		for j := range msg.Content.Blocks {
			block := &msg.Content.Blocks[j]
			if block.Type == "tool_result" {
				switch content := block.Content.(type) {
				case string:
					if len(content) > maxLen {
						cutPoint := truncateUTF8(content, maxLen)
						block.Content = content[:cutPoint] + fmt.Sprintf("\n... [truncated %d bytes]", len(content)-cutPoint)
						compressedCount++
					}
				case []interface{}:
					// tool_result content can be []ContentBlock (decoded as []interface{})
					// Serialize to measure total size, truncate if needed
					raw, err := json.Marshal(content)
					if err == nil && len(raw) > maxLen {
						// Convert to string and truncate at a valid UTF-8 boundary
						s := string(raw)
						cutPoint := truncateUTF8(s, maxLen)
						block.Content = s[:cutPoint] + fmt.Sprintf("\n... [truncated %d bytes]", len(s)-cutPoint)
						compressedCount++
					}
				}
			}
		}
	}

	if compressedCount > 0 {
		slog.Info("Context compressed", "channel", channel, "compressed_blocks", compressedCount)
	}

	return compressed, compressedCount
}

// truncateUTF8 returns the largest index <= maxLen that does not split a UTF-8 character.
func truncateUTF8(s string, maxLen int) int {
	if maxLen >= len(s) {
		return len(s)
	}
	// Walk backwards from maxLen to find a valid UTF-8 boundary
	for maxLen > 0 && !utf8.RuneStart(s[maxLen]) {
		maxLen--
	}
	return maxLen
}
