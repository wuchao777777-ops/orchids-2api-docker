package chatwire

import (
	"strings"

	"encoding/json"

	"orchids-api/internal/util"
)

func (r *Request) UnmarshalJSON(data []byte) error {
	type rawRequest struct {
		Model               string                   `json:"model"`
		Messages            []Message                `json:"messages"`
		Stream              interface{}              `json:"stream"`
		Thinking            *string                  `json:"thinking,omitempty"`
		ReasoningEffort     *string                  `json:"reasoning_effort,omitempty"`
		ReasoningSummary    *string                  `json:"reasoning_summary,omitempty"`
		Temperature         interface{}              `json:"temperature,omitempty"`
		TopP                interface{}              `json:"top_p,omitempty"`
		MaxTokens           interface{}              `json:"max_tokens,omitempty"`
		MaxCompletionTokens interface{}              `json:"max_completion_tokens,omitempty"`
		ResponseFormat      map[string]interface{}   `json:"response_format,omitempty"`
		User                interface{}              `json:"user,omitempty"`
		SafetyIdentifier    interface{}              `json:"safety_identifier,omitempty"`
		ResponseText        map[string]interface{}   `json:"text,omitempty"`
		ResponsesTools      []map[string]interface{} `json:"x_responses_tools,omitempty"`
		Include             []string                 `json:"include,omitempty"`
		Tools               []ToolDef                `json:"tools,omitempty"`
		ToolChoice          interface{}              `json:"tool_choice,omitempty"`
		ParallelToolCalls   interface{}              `json:"parallel_tool_calls,omitempty"`
		Stop                interface{}              `json:"stop,omitempty"`
		PromptCacheKey      string                   `json:"prompt_cache_key,omitempty"`
		MCPServers          []map[string]interface{} `json:"mcp_servers,omitempty"`
		OutputConfig        map[string]interface{}   `json:"output_config,omitempty"`
		ThinkingConfig      map[string]interface{}   `json:"thinking_config,omitempty"`
	}

	var raw rawRequest
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var rawMap map[string]json.RawMessage
	_ = json.Unmarshal(data, &rawMap)
	streamRaw, streamProvided := rawMap["stream"]
	if streamProvided {
		s := strings.TrimSpace(string(streamRaw))
		if s == "" || strings.EqualFold(s, "null") {
			streamProvided = false
		}
	}
	stream, err := ParseLooseBoolAny(raw.Stream)
	if err != nil {
		return err
	}
	temp, err := ParseLooseFloatAny(raw.Temperature)
	if err != nil {
		return err
	}
	topP, err := ParseLooseFloatAny(raw.TopP)
	if err != nil {
		return err
	}
	maxTokens, err := ParseLooseIntAny(raw.MaxTokens)
	if err != nil {
		return err
	}
	maxCompletionTokens, err := ParseLooseIntAny(raw.MaxCompletionTokens)
	if err != nil {
		return err
	}
	parallelToolCalls, err := ParseLooseBoolAnyForField(raw.ParallelToolCalls, "parallel_tool_calls")
	if err != nil {
		return err
	}

	r.Model = raw.Model
	r.Messages = raw.Messages
	r.Stream = stream
	r.StreamProvided = streamProvided
	r.Thinking = raw.Thinking
	r.ReasoningEffort = raw.ReasoningEffort
	r.ReasoningSummary = raw.ReasoningSummary
	r.Temperature = temp
	r.TopP = topP
	if _, ok := rawMap["max_tokens"]; ok {
		r.MaxTokens = &maxTokens
	}
	if _, ok := rawMap["max_completion_tokens"]; ok {
		r.MaxCompletionTokens = &maxCompletionTokens
		// max_tokens is deprecated in favour of max_completion_tokens, so when a
		// caller sends both the newer field decides the output budget.
		r.MaxTokens = &maxCompletionTokens
	}
	r.ResponseFormat = raw.ResponseFormat
	r.SafetyIdentifier = util.FirstNonEmpty(ParseLooseStringAny(raw.SafetyIdentifier), ParseLooseStringAny(raw.User))
	r.ResponseText = raw.ResponseText
	r.ResponsesTools = append([]map[string]interface{}(nil), raw.ResponsesTools...)
	r.Include = append([]string(nil), raw.Include...)
	r.Tools = raw.Tools
	r.ToolChoice = raw.ToolChoice
	if _, ok := rawMap["parallel_tool_calls"]; ok {
		r.ParallelToolCalls = &parallelToolCalls
	}
	r.Stop, err = ParseStringList(raw.Stop, "stop")
	if err != nil {
		return err
	}
	r.PromptCacheKey = strings.TrimSpace(raw.PromptCacheKey)
	r.MCPServers = raw.MCPServers
	r.OutputConfig = raw.OutputConfig
	r.ThinkingConfig = raw.ThinkingConfig
	return nil
}
