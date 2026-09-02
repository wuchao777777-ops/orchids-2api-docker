package handler

import (
	"context"
	"errors"
	"strings"
	"time"

	"orchids-api/internal/prompt"
	"orchids-api/internal/store"
)

const puterReasoningReplayTTL = 6 * time.Hour

func isDeepSeekPuterModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "deepseek-")
}

func assistantToolCallIDs(message prompt.Message) []string {
	if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") || message.Content.IsString() {
		return nil
	}
	ids := make([]string, 0, 2)
	for _, block := range message.Content.GetBlocks() {
		if block.Type == "tool_use" && strings.TrimSpace(block.ID) != "" {
			ids = append(ids, strings.TrimSpace(block.ID))
		}
	}
	return ids
}

func assistantHasReasoning(message prompt.Message) bool {
	if strings.TrimSpace(message.ReasoningContent) != "" {
		return true
	}
	if message.Content.IsString() {
		return false
	}
	for _, block := range message.Content.GetBlocks() {
		if block.Type == "thinking" && strings.TrimSpace(block.Thinking) != "" {
			return true
		}
	}
	return false
}

// restorePuterReasoning mutates only the top-level reasoning field of the
// already-decoded request. The original content/tool blocks remain byte-for-
// byte equivalent for downstream conversion.
func (h *Handler) restorePuterReasoning(ctx context.Context, model string, messages []prompt.Message) (restored, missing int) {
	if h == nil || h.loadBalancer == nil || h.loadBalancer.Store == nil {
		return 0, 0
	}
	for i := range messages {
		if assistantHasReasoning(messages[i]) {
			continue
		}
		toolCallIDs := assistantToolCallIDs(messages[i])
		if len(toolCallIDs) == 0 {
			continue
		}
		found := false
		for _, toolCallID := range toolCallIDs {
			replay, err := h.loadBalancer.Store.GetPuterReasoningReplay(ctx, model, toolCallID)
			if err != nil {
				if errors.Is(err, store.ErrNoRows) {
					continue
				}
				break
			}
			if replay != nil && strings.TrimSpace(replay.ReasoningContent) != "" {
				messages[i].ReasoningContent = replay.ReasoningContent
				restored++
				found = true
				break
			}
		}
		if !found {
			missing++
		}
	}
	return restored, missing
}

func (h *Handler) savePuterReasoningForTool(ctx context.Context, model, toolCallID, reasoning string) error {
	if h == nil || h.loadBalancer == nil || h.loadBalancer.Store == nil {
		return nil
	}
	toolCallID = strings.TrimSpace(toolCallID)
	reasoning = strings.TrimSpace(reasoning)
	if toolCallID == "" || reasoning == "" {
		return nil
	}
	return h.loadBalancer.Store.SavePuterReasoningReplay(ctx, &store.StoredPuterReasoningReplay{
		Model:            strings.TrimSpace(model),
		ToolCallID:       toolCallID,
		ReasoningContent: reasoning,
	}, puterReasoningReplayTTL)
}
