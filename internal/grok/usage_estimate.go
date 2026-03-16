package grok

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-json"
)

const (
	estimatedImagePromptTokens = 256
	estimatedAudioPromptTokens = 128
)

type chatUsageEstimate struct {
	promptTextTokens      int
	promptAudioTokens     int
	promptImageTokens     int
	completionTextTokens  int
	completionAudioTokens int
	reasoningTokens       int
}

func approxTokenCount(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	runes := utf8.RuneCountInString(text)
	if runes <= 0 {
		return 0
	}
	return max(1, (runes+3)/4)
}

func estimatePromptUsageFromRequest(req *ChatCompletionsRequest) chatUsageEstimate {
	var out chatUsageEstimate
	if req == nil {
		return out
	}
	for _, msg := range req.Messages {
		out.promptTextTokens += approxTokenCount(msg.Role)
		out.promptTextTokens += approxTokenCount(msg.Name)
		out.promptTextTokens += approxTokenCount(msg.ToolCallID)
		accumulatePromptContentUsage(&out, msg.Content)
		for _, tc := range msg.ToolCalls {
			out.promptTextTokens += approxTokenCount(tc.ID)
			out.promptTextTokens += approxTokenCount(tc.Type)
			if len(tc.Function) > 0 {
				if raw, err := json.Marshal(tc.Function); err == nil {
					out.promptTextTokens += approxTokenCount(string(raw))
				}
			}
		}
	}
	if len(req.Tools) > 0 {
		if raw, err := json.Marshal(req.Tools); err == nil {
			out.promptTextTokens += approxTokenCount(string(raw))
		}
	}
	if req.ToolChoice != nil {
		if raw, err := json.Marshal(req.ToolChoice); err == nil {
			out.promptTextTokens += approxTokenCount(string(raw))
		}
	}
	return out
}

func accumulatePromptContentUsage(out *chatUsageEstimate, content interface{}) {
	if out == nil || content == nil {
		return
	}
	switch v := content.(type) {
	case string:
		out.promptTextTokens += approxTokenCount(v)
	case []interface{}:
		for _, block := range v {
			accumulatePromptContentUsage(out, block)
		}
	case map[string]interface{}:
		blockType := strings.ToLower(strings.TrimSpace(fmt.Sprint(v["type"])))
		switch blockType {
		case "text", "input_text":
			out.promptTextTokens += approxTokenCount(parseLooseStringAny(v["text"]))
		case "image_url":
			out.promptImageTokens += estimatedImagePromptTokens
			if imageURL, ok := v["image_url"].(map[string]interface{}); ok {
				out.promptTextTokens += approxTokenCount(parseLooseStringAny(imageURL["detail"]))
			}
		case "input_audio":
			out.promptAudioTokens += estimatedAudioPromptTokens
		case "file":
			out.promptImageTokens += estimatedImagePromptTokens
			if fileData, ok := v["file"].(map[string]interface{}); ok {
				out.promptTextTokens += approxTokenCount(parseLooseStringAny(fileData["filename"]))
			}
		default:
			if raw, err := json.Marshal(v); err == nil {
				out.promptTextTokens += approxTokenCount(string(raw))
			}
		}
	default:
		out.promptTextTokens += approxTokenCount(fmt.Sprint(v))
	}
}

func estimateCompletionUsage(finalContent string, toolCalls []map[string]interface{}) chatUsageEstimate {
	var out chatUsageEstimate
	out.completionTextTokens += approxTokenCount(finalContent)
	if len(toolCalls) > 0 {
		if raw, err := json.Marshal(toolCalls); err == nil {
			out.completionTextTokens += approxTokenCount(string(raw))
		}
	}
	return out
}

func buildChatUsagePayload(req *ChatCompletionsRequest, finalContent string, toolCalls []map[string]interface{}) map[string]interface{} {
	prompt := estimatePromptUsageFromRequest(req)
	completion := estimateCompletionUsage(finalContent, toolCalls)
	promptTokens := prompt.promptTextTokens + prompt.promptAudioTokens + prompt.promptImageTokens
	completionTokens := completion.completionTextTokens + completion.completionAudioTokens + completion.reasoningTokens
	return map[string]interface{}{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      promptTokens + completionTokens,
		"prompt_tokens_details": map[string]interface{}{
			"cached_tokens": 0,
			"text_tokens":   prompt.promptTextTokens,
			"audio_tokens":  prompt.promptAudioTokens,
			"image_tokens":  prompt.promptImageTokens,
		},
		"completion_tokens_details": map[string]interface{}{
			"text_tokens":      completion.completionTextTokens,
			"audio_tokens":     completion.completionAudioTokens,
			"reasoning_tokens": completion.reasoningTokens,
		},
	}
}

func buildImageUsagePayload(prompt string, imageCount int) map[string]interface{} {
	promptTokens := approxTokenCount(prompt)
	completionTokens := max(0, imageCount) * 64
	return map[string]interface{}{
		"total_tokens":  promptTokens + completionTokens,
		"input_tokens":  promptTokens,
		"output_tokens": completionTokens,
		"input_tokens_details": map[string]interface{}{
			"text_tokens":  promptTokens,
			"image_tokens": 0,
		},
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"prompt_tokens_details": map[string]interface{}{
			"cached_tokens": 0,
			"text_tokens":   promptTokens,
			"audio_tokens":  0,
			"image_tokens":  0,
		},
		"completion_tokens_details": map[string]interface{}{
			"text_tokens":      completionTokens,
			"audio_tokens":     0,
			"reasoning_tokens": 0,
		},
	}
}
