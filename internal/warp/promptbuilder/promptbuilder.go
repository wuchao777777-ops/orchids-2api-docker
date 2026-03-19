package promptbuilder

import (
	"strings"

	"orchids-api/internal/prompt"
)

const (
	thinkingBudget  = 10000
	thinkingMin     = 1024
	thinkingMax     = 128000
	thinkingModeTag = "<thinking_mode>"
	thinkingLenTag  = "<max_thinking_length>"

	profileDefault  = "default"
	profileUltraMin = "ultra-min"
)

// Meta captures the prompt builder decisions that handler code needs to keep.
type Meta struct {
	Profile    string
	NoThinking bool
}

func BuildWithMeta(messages []prompt.Message, system []prompt.SystemItem, model string, noThinking bool, workdir string) (string, []map[string]string, Meta) {
	return BuildWithMetaAndTools(messages, system, model, noThinking, workdir, nil)
}

func BuildWithMetaAndTools(messages []prompt.Message, system []prompt.SystemItem, model string, noThinking bool, workdir string, tools []interface{}) (string, []map[string]string, Meta) {
	meta := Meta{Profile: profileDefault}

	systemText := extractSystemPrompt(messages)
	if strings.TrimSpace(systemText) == "" && len(system) > 0 {
		var sb strings.Builder
		for _, item := range system {
			if strings.TrimSpace(item.Text) == "" {
				continue
			}
			sb.WriteString(item.Text)
			sb.WriteString("\n")
		}
		systemText = sb.String()
	}
	systemText = stripSystemReminders(systemText)

	userText := stripSystemReminders(extractWarpUserMessage(messages))
	currentUserIdx := findCurrentUserMessageIndex(messages)
	userText = resolveCurrentUserTurnText(messages, currentUserIdx, userText)
	currentTurnToolResultOnly := isToolResultOnlyUserMessage(messages, currentUserIdx)
	supportedTools := supportedToolNames(tools)
	if currentTurnToolResultOnly && len(supportedTools) == 0 {
		userText = rewriteToolResultFollowUpForDirectAnswer(userText)
	}

	var historyMessages []prompt.Message
	if currentUserIdx >= 0 {
		historyMessages = messages[:currentUserIdx]
	} else {
		historyMessages = messages
	}
	chatHistory := convertWarpChatHistory(historyMessages)
	chatHistory = pruneExploratoryAssistantHistory(chatHistory, currentTurnToolResultOnly, len(supportedTools) == 0)

	meta.Profile = selectPromptProfileForTurn(userText, currentTurnToolResultOnly)
	meta.NoThinking = noThinking || currentTurnToolResultOnly || shouldDisableThinkingForProfile(meta.Profile)

	promptText := buildLocalAssistantPromptWithProfileAndTools(systemText, userText, model, workdir, meta.Profile, tools)
	if !meta.NoThinking && !isSuggestionModeText(userText) {
		promptText = injectThinkingPrefix(promptText)
	}

	return promptText, normalizeWarpPromptHistory(chatHistory), meta
}

func normalizeWarpPromptHistory(history []map[string]string) []map[string]string {
	if len(history) == 0 {
		return nil
	}
	out := make([]map[string]string, 0, len(history))
	for _, item := range history {
		role := strings.ToLower(strings.TrimSpace(item["role"]))
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(item["content"])
		if content == "" {
			continue
		}
		out = append(out, map[string]string{"role": role, "content": content})
	}
	return out
}
