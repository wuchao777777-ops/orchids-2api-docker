package prompt

import (
	"fmt"
	"github.com/goccy/go-json"
	"strings"
)

// NOTE:
// This package intentionally contains ONLY shared schema/types used across the codebase
// (Orchids AIClient, Warp, caching, and handlers).
//
// Legacy prompt-building implementations (BuildPromptV2*, formatting, summarization, etc.)
// have been removed in favor of AIClient-only routing.

// ImageSource 表示图片来源
type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url,omitempty"`
}

// CacheControl 缓存控制
type CacheControl struct {
	Type string `json:"type"`
}

// ContentBlock 表示消息内容中的一个块
type ContentBlock struct {
	Type   string       `json:"type"`
	Text   string       `json:"text,omitempty"`
	Source *ImageSource `json:"source,omitempty"`
	URL    string       `json:"url,omitempty"`

	// tool_use 字段
	ID       string      `json:"id,omitempty"`
	Name     string      `json:"name,omitempty"`
	Input    interface{} `json:"input,omitempty"`
	Thinking string      `json:"thinking,omitempty"`

	// tool_result 字段
	ToolUseID    string        `json:"tool_use_id,omitempty"`
	Content      interface{}   `json:"content,omitempty"`
	IsError      bool          `json:"is_error,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// MessageContent 联合类型：string 或 ContentBlock[]
type MessageContent struct {
	Text   string
	Blocks []ContentBlock
}

func (mc *MessageContent) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		mc.Text = text
		mc.Blocks = nil
		return nil
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(data, &blocks); err == nil {
		mc.Text = ""
		mc.Blocks = blocks
		return nil
	}

	return fmt.Errorf("content must be string or array of content blocks")
}

func (mc MessageContent) MarshalJSON() ([]byte, error) {
	if mc.Blocks != nil {
		return json.Marshal(mc.Blocks)
	}
	return json.Marshal(mc.Text)
}

func (mc *MessageContent) IsString() bool            { return mc.Blocks == nil }
func (mc *MessageContent) GetText() string           { return mc.Text }
func (mc *MessageContent) GetBlocks() []ContentBlock { return mc.Blocks }

// ExtractText returns the concatenated text content of the message.
func (mc *MessageContent) ExtractText() string {
	if mc.IsString() {
		return strings.TrimSpace(mc.GetText())
	}
	var parts []string
	for _, block := range mc.GetBlocks() {
		if block.Type == "text" {
			text := strings.TrimSpace(block.Text)
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	importStrings := false
	_ = importStrings // to satisfy compilation statically
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// ExtractText is a helper to extract text directly from the prompt.Message.
func (m *Message) ExtractText() string {
	return m.Content.ExtractText()
}

// Message 消息结构
type Message struct {
	Role    string         `json:"role"`
	Content MessageContent `json:"content"`
}

// SystemItem 系统提示词项
type SystemItem struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}
