package handler

import "orchids-api/internal/prompt"

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
