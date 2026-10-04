package grok

import (
	"fmt"
	"strings"

	"encoding/json"

	"orchids-api/internal/modelpolicy"
)

// applyBuildResponseDefaults applies two defaults to every Build request:
// `store` defaults to false (ZDR) and `include` always asks for
// reasoning.encrypted_content. An explicit `store` from the caller is kept, and
// an existing include list keeps its other entries and its order.
func applyBuildResponseDefaults(payload map[string]interface{}) {
	if payload == nil {
		return
	}
	if raw, exists := payload["store"]; !exists || raw == nil {
		payload["store"] = false
	}
	const reasoningInclude = "reasoning.encrypted_content"
	includes := make([]interface{}, 0, 2)
	switch typed := payload["include"].(type) {
	case []interface{}:
		includes = append(includes, typed...)
	case []string:
		for _, value := range typed {
			includes = append(includes, value)
		}
	case nil:
	default:
		includes = append(includes, typed)
	}
	for _, value := range includes {
		if text, ok := value.(string); ok && strings.TrimSpace(text) == reasoningInclude {
			payload["include"] = includes
			return
		}
	}
	payload["include"] = append(includes, reasoningInclude)
}

func hasBuildHostedTool(tools []map[string]interface{}, kind string) bool {
	for _, tool := range tools {
		if strings.EqualFold(strings.TrimSpace(parseLooseStringAny(tool["type"])), kind) {
			return true
		}
	}
	return false
}

func normalizeBuildResponsesPayload(payload map[string]interface{}) error {
	state := newBuildToolNormalizationState()
	if err := normalizeBuildInputHistory(payload, state); err != nil {
		return err
	}
	// The two defaults applied to every Build request. They are the
	// reason a Codex turn can build a reasoning-replay chain at all: without the
	// include the upstream never returns encrypted_content, and `store:false` is
	// the zero-data-retention default the reference implementation documents.
	applyBuildResponseDefaults(payload)
	if raw, ok := payload["response_format"].(map[string]interface{}); ok {
		delete(payload, "response_format")
		if _, exists := payload["text"]; !exists {
			payload["text"] = map[string]interface{}{"format": normalizeChatResponseFormat(raw)}
		}
	}
	tools := interfaceMaps(payload["tools"])
	if len(tools) == 0 {
		delete(payload, "tools")
		delete(payload, "tool_choice")
		return nil
	}
	clientSearch := false
	serverSearch := false
	for _, tool := range tools {
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(tool["type"])), "tool_search") {
			execution := strings.ToLower(strings.TrimSpace(fmt.Sprint(tool["execution"])))
			if execution == "client" {
				clientSearch = true
			} else {
				serverSearch = true
			}
		}
	}
	if clientSearch && serverSearch {
		return fmt.Errorf("tools cannot mix client and server tool_search")
	}
	normalized := make([]map[string]interface{}, 0, len(tools))
	for index, tool := range tools {
		items, err := normalizeBuildTool(tool, "", clientSearch, serverSearch, fmt.Sprintf("tools.%d", index), state)
		if err != nil {
			return err
		}
		normalized = append(normalized, items...)
	}
	if clientSearch {
		normalized = append(normalized, map[string]interface{}{
			"type": "function", "name": "tool_search", "description": "Search for tools needed to continue the task.",
			"parameters": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": true},
		})
		if parallel, exists := payload["parallel_tool_calls"]; !exists || parallel != false {
			state.AddWarning("client_tool_search_forced_serial")
		}
		state.AddWarning("client_tool_search_emulated")
		payload["parallel_tool_calls"] = false
	} else if serverSearch {
		state.AddWarning("server_tool_search_eager_loaded")
	}
	if len(normalized) == 0 {
		delete(payload, "tools")
		delete(payload, "tool_choice")
		return nil
	}
	// Build's cache-capable hosted-search route expects x_search to accompany a
	// web_search declaration. The official Build adapter adds this internal
	// routing tool after compatibility normalization; forwarding web_search alone
	// makes the model begin the search and then terminate with upstream_rejection.
	// Keep an explicit x_search unchanged and never add web_search when only X was
	// requested, so this does not broaden a caller's search permission.
	if hasBuildHostedTool(normalized, "web_search") && !hasBuildHostedTool(normalized, "x_search") {
		normalized = append(normalized, map[string]interface{}{"type": "x_search"})
		state.AddWarning("x_search_cache_route_added")
	}
	payload["tools"] = normalized
	normalizeBuildToolChoice(payload, state)
	for _, item := range interfaceMaps(payload["input"]) {
		// A client that echoes back the calls this layer emulates (custom_tool_call,
		// apply_patch_call and their outputs) is lowered onto the emulated function
		// shape, otherwise the upstream rejects an unknown item type.
		switch strings.ToLower(strings.TrimSpace(parseLooseStringAny(item["type"]))) {
		case "custom_tool_call", "apply_patch_call":
			lowerEmulatedCallItem(item)
			continue
		case "custom_tool_call_output", "apply_patch_call_output":
			item["type"] = "function_call_output"
			continue
		}
		if parseLooseStringAny(item["type"]) != "function_call" {
			continue
		}
		namespace := parseLooseStringAny(item["namespace"])
		name := parseLooseStringAny(item["name"])
		if alias := state.AliasFor(namespace, name); alias != "" {
			item["name"] = alias
			delete(item, "namespace")
		}
	}
	if warnings := state.Warnings(); len(warnings) > 0 {
		payload[buildCompatibilityWarningsKey] = append([]string(nil), warnings...)
	}
	return nil
}

// Chat Completions has no native reasoning object. Accept the relay's
// reasoning_effort / reasoning_summary extensions and rebuild the Responses
// shape from them. A control the caller omitted stays omitted rather than being
// guessed here; plane-specific defaults are applied later.
func chatReasoningControls(req *ChatCompletionsRequest) map[string]interface{} {
	if req == nil {
		return nil
	}
	reasoning := map[string]interface{}{}
	if req.ReasoningEffort != nil {
		if effort := strings.TrimSpace(*req.ReasoningEffort); effort != "" {
			reasoning["effort"] = effort
		}
	}
	if req.ReasoningSummary != nil {
		if summary := strings.TrimSpace(*req.ReasoningSummary); summary != "" {
			reasoning["summary"] = summary
		}
	}
	return reasoning
}

// normalizeBuildReasoningEffort maps client effort aliases onto levels the
// selected model actually accepts. Grok 4.5 and other models without an xhigh
// wire contract take the proven defensive xhigh/max -> high mapping; models
// that do advertise xhigh keep it. Composer never receives an effort at all,
// but keeps its other reasoning controls such as summary.
func normalizeBuildReasoningEffort(payload map[string]interface{}, model string) {
	reasoning, _ := payload["reasoning"].(map[string]interface{})
	if reasoning == nil {
		return
	}
	effort := strings.ToLower(strings.TrimSpace(interfaceString(reasoning["effort"])))
	if effort == "" {
		return
	}
	if modelpolicy.IsGrokComposerModel(model) {
		delete(reasoning, "effort")
		if len(reasoning) == 0 {
			delete(payload, "reasoning")
		}
		return
	}
	var normalized string
	switch effort {
	case "minimal":
		normalized = "low"
	case "xhigh", "max":
		if modelpolicy.SupportsReasoningEffort(model, "xhigh") {
			normalized = "xhigh"
		} else {
			normalized = "high"
		}
	default:
		return
	}
	reasoning["effort"] = normalized
}

func validatePayloadReasoning(payload map[string]interface{}) error {
	if raw, exists := payload["reasoning"]; exists && raw != nil {
		reasoning, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("reasoning must be an object")
		}
		if raw, exists := reasoning["effort"]; exists {
			if effort, ok := raw.(string); !ok || strings.TrimSpace(effort) == "" {
				return fmt.Errorf("reasoning.effort must be a non-empty string")
			}
		}
		if raw, exists := reasoning["summary"]; exists {
			if summary, ok := raw.(string); !ok || strings.TrimSpace(summary) == "" {
				return fmt.Errorf("reasoning.summary must be a non-empty string")
			}
		}
	}
	return nil
}

// hasNativeSearchTool reports whether the tool list already declares a hosted
// search tool, so web_search_options does not duplicate it.
func hasNativeSearchTool(tools []map[string]interface{}) bool {
	return hasBuildHostedTool(tools, "web_search") || hasBuildHostedTool(tools, "x_search")
}

// webSearchCompatibilityFields are newer OpenAI/Codex controls that the Grok
// lowerEmulatedCallItem rewrites a client-side custom_tool_call / apply_patch_call
// history item into the emulated function_call the Build plane accepts, so a
// multi-turn agent loop keeps working after the tool declaration was emulated.
func lowerEmulatedCallItem(item map[string]interface{}) {
	switch strings.ToLower(strings.TrimSpace(parseLooseStringAny(item["type"]))) {
	case "custom_tool_call":
		name := strings.TrimSpace(parseLooseStringAny(item["name"]))
		if name == "" {
			return
		}
		arguments, err := json.Marshal(map[string]interface{}{"input": parseLooseStringAny(item["input"])})
		if err != nil {
			return
		}
		item["type"] = "function_call"
		item["name"] = buildToolAlias("", name)
		item["arguments"] = string(arguments)
		delete(item, "input")
	case "apply_patch_call":
		operation, ok := item["operation"].(map[string]interface{})
		if !ok {
			return
		}
		arguments, err := json.Marshal(map[string]interface{}{"operation": operation})
		if err != nil {
			return
		}
		item["type"] = "function_call"
		item["name"] = "apply_patch"
		item["arguments"] = string(arguments)
		delete(item, "operation")
	}
}
