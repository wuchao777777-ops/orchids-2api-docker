package handler

import (
	"slices"

	"orchids-api/internal/prompt"
)

func splitWarpToolResults(messages []prompt.Message, batchSize int) ([][]prompt.Message, int) {
	if batchSize <= 0 {
		return [][]prompt.Message{cloneMessages(messages)}, 0
	}

	turnIndex := lastToolResultTurnIndex(messages)
	if turnIndex < 0 {
		return [][]prompt.Message{cloneMessages(messages)}, 0
	}

	refs := collectToolResultRefsForTurn(messages, turnIndex)
	total := len(refs)
	if total <= batchSize {
		return [][]prompt.Message{cloneMessages(messages)}, total
	}

	var batches [][]prompt.Message
	for end := batchSize; end <= total; end += batchSize {
		keep := make(map[int]struct{}, end)
		for _, ref := range refs[:end] {
			keep[ref] = struct{}{}
		}
		keepUserText := end == total
		batches = append(batches, filterToolResults(messages, turnIndex, keep, keepUserText))
	}
	if total%batchSize != 0 {
		keep := make(map[int]struct{}, total)
		for _, ref := range refs {
			keep[ref] = struct{}{}
		}
		batches = append(batches, filterToolResults(messages, turnIndex, keep, true))
	}

	return batches, total
}

func lastToolResultTurnIndex(messages []prompt.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "user" {
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

func collectToolResultRefsForTurn(messages []prompt.Message, turnIndex int) []int {
	var refs []int
	msg := messages[turnIndex]
	for j, block := range msg.Content.Blocks {
		if block.Type == "tool_result" {
			refs = append(refs, j)
		}
	}
	return refs
}

func filterToolResults(messages []prompt.Message, turnIndex int, keep map[int]struct{}, keepUserText bool) []prompt.Message {
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
				if _, ok := keep[j]; !ok {
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
		out[i].Content.Blocks = slices.Clone(msg.Content.Blocks)
	}
	return out
}
