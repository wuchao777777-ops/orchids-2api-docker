package orchids

import (
	"fmt"
	"strings"

	"orchids-api/internal/config"
	"orchids-api/internal/upstream"
)

type OrchidsMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type OrchidsRequest struct {
	Messages  []OrchidsMessage       `json:"messages,omitempty"`
	Model     interface{}            `json:"model,omitempty"`
	ModelName string                 `json:"modelName,omitempty"`
	System    string                 `json:"system,omitempty"`
	MaxTokens int                    `json:"maxTokens,omitempty"`
	Config    map[string]interface{} `json:"config,omitempty"`
}

type ChatCompletionRequest struct {
	Model        string
	Messages     []OrchidsConversationMessage
	System       string
	MaxTokens    int
	ThinkingMode string
	Config       map[string]interface{}
}

func convertToOrchidsRequestMessages(messages []OrchidsConversationMessage) []OrchidsMessage {
	if len(messages) == 0 {
		return nil
	}

	out := make([]OrchidsMessage, 0, len(messages))
	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			continue
		}
		out = append(out, OrchidsMessage{
			Role:    role,
			Content: msg.Content,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildChatCompletionRequest(req upstream.UpstreamRequest, cfg *config.Config) *ChatCompletionRequest {
	conversation := buildOrchidsConversationMessages(req.Messages)
	if len(conversation) == 0 {
		conversation = buildLegacyOrchidsConversationMessages(req.ChatHistory, req.Prompt)
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		modelName = normalizeOrchidsAgentModel(req.Model)
	}

	request := &ChatCompletionRequest{
		Model:        modelName,
		Messages:     conversation,
		System:       extractCodeFreeMaxSystemText(req.Messages, req.System),
		MaxTokens:    orchidsMaxTokens(cfg),
		ThinkingMode: orchidsThinkingMode(req),
	}
	if request.ThinkingMode != "" {
		request.Config = map[string]interface{}{
			"thinkingMode": request.ThinkingMode,
		}
	}
	return request
}

func ConvertToOrchidsRequest(req *ChatCompletionRequest) OrchidsRequest {
	if req == nil {
		return OrchidsRequest{}
	}

	return OrchidsRequest{
		Messages:  convertToOrchidsRequestMessages(req.Messages),
		Model:     req.Model,
		ModelName: req.Model,
		System:    req.System,
		MaxTokens: req.MaxTokens,
		Config:    req.Config,
	}
}

func buildOrchidsRequest(req upstream.UpstreamRequest, cfg *config.Config) OrchidsRequest {
	return ConvertToOrchidsRequest(buildChatCompletionRequest(req, cfg))
}

func buildLegacyOrchidsConversationMessages(chatHistory []interface{}, prompt string) []OrchidsConversationMessage {
	out := make([]OrchidsConversationMessage, 0, len(chatHistory)+1)

	for _, item := range chatHistory {
		role, content := legacyConversationMessage(item)
		if role == "" || content == "" {
			continue
		}
		out = append(out, OrchidsConversationMessage{
			Role:        role,
			ContentType: "string",
			Content:     content,
		})
	}

	if prompt = strings.TrimSpace(prompt); prompt != "" {
		out = append(out, OrchidsConversationMessage{
			Role:        "user",
			ContentType: "string",
			Content:     prompt,
		})
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

func legacyConversationMessage(item interface{}) (string, string) {
	switch value := item.(type) {
	case map[string]string:
		return strings.TrimSpace(value["role"]), strings.TrimSpace(value["content"])
	case map[string]interface{}:
		role, _ := value["role"].(string)
		content, _ := value["content"].(string)
		if content == "" {
			if raw, ok := value["content"]; ok && raw != nil {
				content = fmt.Sprint(raw)
			}
		}
		return strings.TrimSpace(role), strings.TrimSpace(content)
	default:
		return "", ""
	}
}
