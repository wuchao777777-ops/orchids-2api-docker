package handler

import (
	"context"
	"log/slog"
)

func (h *Handler) restoreBoltTools(ctx context.Context, conversationKey string) []interface{} {
	if h == nil || h.sessionStore == nil || conversationKey == "" {
		return nil
	}
	names, ok := h.sessionStore.GetBoltToolNames(ctx, conversationKey)
	if !ok || len(names) == 0 {
		return nil
	}
	return minimalIncomingToolsFromNames(names)
}

func (h *Handler) persistBoltTools(ctx context.Context, conversationKey string, tools []interface{}) {
	if h == nil || h.sessionStore == nil || conversationKey == "" {
		return
	}
	names := supportedToolNames(tools)
	if len(names) == 0 {
		return
	}
	h.sessionStore.SetBoltToolNames(ctx, conversationKey, names)
}

func minimalIncomingToolsFromNames(names []string) []interface{} {
	if len(names) == 0 {
		return nil
	}
	out := make([]interface{}, 0, len(names))
	for _, name := range names {
		out = append(out, map[string]interface{}{"name": name})
	}
	names = supportedToolNames(out)
	if len(names) == 0 {
		return nil
	}
	normalized := make([]interface{}, 0, len(names))
	for _, name := range names {
		normalized = append(normalized, map[string]interface{}{"name": name})
	}
	return normalized
}

func logBoltToolsRestored(conversationKey string, tools []interface{}) {
	slog.Debug(
		"bolt tools restored from session",
		"conversation_id", conversationKey,
		"tool_names", supportedToolNames(tools),
	)
}
