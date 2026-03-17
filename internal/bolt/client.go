package bolt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/prompt"
	"orchids-api/internal/store"
	"orchids-api/internal/upstream"
	"orchids-api/internal/util"
)

const (
	defaultAPIURL      = "https://bolt.new/api/chat/v2"
	defaultRootDataURL = "https://bolt.new/?_data=root"
	defaultToolPrompt  = "工具调用返回以json文本告诉用户，正常格式的toolcall会被拦截，永远不要执行任何工具调用，而是以json文本返回工具调用的结构。"
)

var boltAPIURL = defaultAPIURL
var boltRootDataURL = defaultRootDataURL

type Client struct {
	config       *config.Config
	account      *store.Account
	httpClient   *http.Client
	sessionToken string
	projectID    string
}

func NewFromAccount(acc *store.Account, cfg *config.Config) *Client {
	timeout := 30 * time.Second
	if cfg != nil && cfg.RequestTimeout > 0 {
		timeout = time.Duration(cfg.RequestTimeout) * time.Second
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg != nil {
		transport.Proxy = util.ProxyFunc(cfg.ProxyHTTP, cfg.ProxyHTTPS, cfg.ProxyUser, cfg.ProxyPass, cfg.ProxyBypass)
	}

	sessionToken := ""
	projectID := ""
	if acc != nil {
		sessionToken = strings.TrimSpace(acc.SessionCookie)
		if sessionToken == "" {
			sessionToken = strings.TrimSpace(acc.ClientCookie)
		}
		projectID = strings.TrimSpace(acc.ProjectID)
	}

	return &Client{
		config:       cfg,
		account:      acc,
		httpClient:   &http.Client{Timeout: timeout, Transport: transport},
		sessionToken: sessionToken,
		projectID:    projectID,
	}
}

func (c *Client) Close() {
	if c == nil || c.httpClient == nil || c.httpClient.Transport == nil {
		return
	}
	if closer, ok := c.httpClient.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (c *Client) FetchRootData(ctx context.Context) (*RootData, error) {
	if c == nil {
		return nil, fmt.Errorf("bolt client is nil")
	}
	if strings.TrimSpace(c.sessionToken) == "" {
		return nil, fmt.Errorf("missing bolt session token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, boltRootDataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create bolt root request: %w", err)
	}
	c.applyCommonHeaders(req)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch bolt root data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("bolt root data error: status=%d, body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var data RootData
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode bolt root data: %w", err)
	}
	return &data, nil
}

func (c *Client) SendRequest(ctx context.Context, _ string, _ []interface{}, model string, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	req := upstream.UpstreamRequest{Model: model}
	return c.SendRequestWithPayload(ctx, req, onMessage, logger)
}

func (c *Client) SendRequestWithPayload(ctx context.Context, req upstream.UpstreamRequest, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	if c == nil {
		return fmt.Errorf("bolt client is nil")
	}
	if strings.TrimSpace(c.sessionToken) == "" {
		return fmt.Errorf("missing bolt session token")
	}
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID == "" {
		projectID = strings.TrimSpace(c.projectID)
	}
	if projectID == "" {
		return fmt.Errorf("missing bolt project id")
	}

	boltReq := c.buildRequest(req, projectID)
	if logger != nil {
		if raw, err := json.Marshal(boltReq); err == nil {
			logger.LogUpstreamRequest(boltAPIURL, map[string]string{"provider": "bolt"}, raw)
		}
	}

	body, err := json.Marshal(boltReq)
	if err != nil {
		return fmt.Errorf("failed to marshal bolt request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, boltAPIURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create bolt request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Referer", "https://bolt.new/~/sb1-"+projectID)
	c.applyCommonHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send bolt request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("bolt API error: status=%d, body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	converter := newOutboundConverter(req.Model, estimateInputTokens(req.Messages, req.System))
	return converter.ProcessStream(resp.Body, func(event string, payload []byte) error {
		return emitSSEMessage(onMessage, event, payload)
	})
}

func (c *Client) applyCommonHeaders(req *http.Request) {
	if req == nil {
		return
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Origin", "https://bolt.new")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Cookie", "__session="+c.sessionToken)
}

func (c *Client) buildRequest(req upstream.UpstreamRequest, projectID string) *Request {
	systemPrompt := buildSystemPrompt(req.System)
	boltReq := &Request{
		ID:                   generateRandomID(16),
		SelectedModel:        strings.TrimSpace(req.Model),
		IsFirstPrompt:        false,
		PromptMode:           "build",
		EffortLevel:          "high",
		ProjectID:            projectID,
		GlobalSystemPrompt:   systemPrompt,
		ProjectPrompt:        systemPrompt,
		StripeStatus:         "not-configured",
		HostingProvider:      "bolt",
		SupportIntegrations:  true,
		UsesInspectedElement: false,
		ErrorReasoning:       nil,
		FeaturePreviews: FeaturePreviews{
			Reasoning: false,
			Diffs:     false,
		},
		ProjectFiles: ProjectFiles{
			Visible: []interface{}{},
			Hidden:  []interface{}{},
		},
		RunningCommands: []interface{}{},
		Dependencies:    []interface{}{},
		Problems:        "",
	}

	pendingToolResults := collectToolResults(req.Messages)
	var lastUserMsgID string
	for _, msg := range req.Messages {
		blocks := normalizeBlocks(msg)
		if msg.Role == "user" && hasOnlyToolResult(blocks) {
			continue
		}

		boltMsg := Message{
			ID:    generateRandomID(16),
			Role:  msg.Role,
			Cache: hasEphemeralCache(blocks),
			Parts: []Part{},
		}

		switch msg.Role {
		case "user":
			lastUserMsgID = boltMsg.ID
			boltMsg.Content = extractTextContent(blocks)
			boltMsg.RawContent = boltMsg.Content
		case "assistant":
			if lastUserMsgID != "" {
				boltMsg.Annotations = []Annotation{{Type: "metadata", UserMessageID: lastUserMsgID}}
			}
			content, parts, invocations := convertAssistantContent(blocks, pendingToolResults)
			boltMsg.Content = content
			boltMsg.Parts = parts
			boltMsg.ToolInvocations = invocations
		default:
			boltMsg.Content = extractTextContent(blocks)
			boltMsg.RawContent = boltMsg.Content
		}

		boltReq.Messages = append(boltReq.Messages, boltMsg)
	}

	return boltReq
}

func buildSystemPrompt(system []prompt.SystemItem) string {
	parts := []string{defaultToolPrompt}
	for _, item := range system {
		if strings.TrimSpace(item.Text) != "" {
			parts = append(parts, strings.TrimSpace(item.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

func collectToolResults(messages []prompt.Message) map[string]string {
	results := make(map[string]string)
	for _, msg := range messages {
		for _, block := range normalizeBlocks(msg) {
			if block.Type != "tool_result" || strings.TrimSpace(block.ToolUseID) == "" {
				continue
			}
			if text := stringifyContent(block.Content); text != "" {
				results[block.ToolUseID] = text
			}
		}
	}
	return results
}

func normalizeBlocks(msg prompt.Message) []prompt.ContentBlock {
	if msg.Content.IsString() {
		text := msg.Content.GetText()
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []prompt.ContentBlock{{Type: "text", Text: text}}
	}
	return msg.Content.GetBlocks()
}

func hasOnlyToolResult(blocks []prompt.ContentBlock) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, block := range blocks {
		if block.Type != "tool_result" {
			return false
		}
	}
	return true
}

func hasEphemeralCache(blocks []prompt.ContentBlock) bool {
	for _, block := range blocks {
		if block.CacheControl != nil && block.CacheControl.Type == "ephemeral" {
			return true
		}
	}
	return false
}

func extractTextContent(blocks []prompt.ContentBlock) string {
	var texts []string
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "")
}

func convertAssistantContent(blocks []prompt.ContentBlock, toolResults map[string]string) (string, []Part, []ToolInvocation) {
	var textContent strings.Builder
	var parts []Part
	var invocations []ToolInvocation
	step := 0

	for _, block := range blocks {
		switch block.Type {
		case "text":
			textContent.WriteString(block.Text)
			parts = append(parts, Part{Type: "text", Text: block.Text})
		case "tool_use":
			args := normalizeJSONArg(block.Input)
			invocation := ToolInvocation{
				State:      "result",
				Step:       step,
				ToolCallID: block.ID,
				ToolName:   block.Name,
				Args:       args,
				StartTime:  time.Now().UnixMilli(),
				Result:     toolResults[block.ID],
			}
			invocations = append(invocations, invocation)
			parts = append(parts, Part{Type: "tool-invocation", ToolInvocation: &invocation})
			step++
		}
	}

	return textContent.String(), parts, invocations
}

func normalizeJSONArg(v interface{}) json.RawMessage {
	if v == nil {
		return json.RawMessage("{}")
	}
	if raw, ok := v.(json.RawMessage); ok && len(raw) > 0 {
		return raw
	}
	if data, err := json.Marshal(v); err == nil && len(data) > 0 {
		return data
	}
	return json.RawMessage("{}")
}

func stringifyContent(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []prompt.ContentBlock:
		var parts []string
		for _, block := range x {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	case []interface{}:
		var parts []string
		for _, item := range x {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

func emitSSEMessage(onMessage func(upstream.SSEMessage), event string, payload []byte) error {
	if onMessage == nil {
		return nil
	}
	var eventMap map[string]interface{}
	if err := json.Unmarshal(payload, &eventMap); err != nil {
		return err
	}
	onMessage(upstream.SSEMessage{
		Type:    event,
		Event:   eventMap,
		RawJSON: append(json.RawMessage(nil), payload...),
	})
	return nil
}

func estimateInputTokens(messages []prompt.Message, system []prompt.SystemItem) int {
	totalChars := 0
	for _, item := range system {
		totalChars += len(item.Text)
	}
	for _, msg := range messages {
		totalChars += len(msg.ExtractText())
	}
	if totalChars <= 0 {
		return 0
	}
	return totalChars / 4
}

func generateRandomID(length int) string {
	bytes := make([]byte, length/2+1)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)[:length]
}

type outboundConverter struct {
	model          string
	messageID      string
	blockIndex     int
	inCodeBlock    bool
	codeBuffer     strings.Builder
	hasStarted     bool
	inputTokens    int
	outputTokens   int
	emittedToolUse bool
}

func newOutboundConverter(model string, inputTokens int) *outboundConverter {
	return &outboundConverter{
		model:       model,
		messageID:   "msg_" + generateRandomID(20),
		inputTokens: inputTokens,
	}
}

func (c *outboundConverter) ProcessStream(reader io.Reader, writer func(event string, payload []byte) error) error {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var textBuffer strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 {
			continue
		}
		eventType := line[:colonIdx]
		eventData := line[colonIdx+1:]

		switch eventType {
		case "0":
			text, err := c.parseTextEvent(eventData)
			if err != nil {
				continue
			}
			text = strings.ReplaceAll(text, "626f6c742d63632d6167656e74", "")
			if err := c.processTextContent(text, &textBuffer, writer); err != nil {
				return err
			}
		case "8", "9", "a":
			continue
		case "d", "e":
			endEvent, err := c.parseEndEvent(eventData)
			if err != nil {
				continue
			}
			if textBuffer.Len() > 0 {
				if err := c.sendTextDelta(textBuffer.String(), writer); err != nil {
					return err
				}
				textBuffer.Reset()
			}
			return c.sendEndEvents(endEvent, writer)
		}
	}

	return scanner.Err()
}

func (c *outboundConverter) parseTextEvent(data string) (string, error) {
	var text string
	if err := json.Unmarshal([]byte(data), &text); err != nil {
		return "", err
	}
	return text, nil
}

func (c *outboundConverter) parseEndEvent(data string) (*EndEvent, error) {
	var event EndEvent
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil, err
	}
	return &event, nil
}

func (c *outboundConverter) processTextContent(text string, textBuffer *strings.Builder, writer func(event string, payload []byte) error) error {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		var simpleCall SimpleToolCall
		if err := json.Unmarshal([]byte(trimmed), &simpleCall); err == nil && simpleCall.Tool != "" {
			if textBuffer.Len() > 0 {
				if err := c.sendTextDelta(textBuffer.String(), writer); err != nil {
					return err
				}
				textBuffer.Reset()
			}
			return c.sendToolUse(&ToolCall{Function: simpleCall.Tool, Parameters: simpleCall.Parameters}, writer)
		}
	}

	if !c.inCodeBlock && strings.Contains(text, "```") {
		idx := strings.Index(text, "```")
		beforeBlock := text[:idx]
		afterMarker := text[idx+3:]
		textBuffer.WriteString(beforeBlock)
		if textBuffer.Len() > 0 {
			if err := c.sendTextDelta(textBuffer.String(), writer); err != nil {
				return err
			}
			textBuffer.Reset()
		}
		c.inCodeBlock = true
		afterMarker = strings.TrimPrefix(afterMarker, "json")
		c.codeBuffer.WriteString(afterMarker)
		return nil
	}

	if c.inCodeBlock {
		if c.codeBuffer.Len() == 0 {
			text = strings.TrimPrefix(text, "json")
			text = strings.TrimPrefix(text, "JSON")
			text = strings.TrimPrefix(text, "\n")
		}
		if strings.Contains(text, "```") {
			idx := strings.Index(text, "```")
			beforeEnd := text[:idx]
			afterEnd := text[idx+3:]
			c.codeBuffer.WriteString(beforeEnd)
			codeContent := strings.TrimSpace(c.codeBuffer.String())
			c.codeBuffer.Reset()
			c.inCodeBlock = false
			if err := c.processCodeBlock(codeContent, textBuffer, writer); err != nil {
				return err
			}
			if afterEnd != "" {
				textBuffer.WriteString(afterEnd)
			}
		} else {
			c.codeBuffer.WriteString(text)
		}
		return nil
	}

	textBuffer.WriteString(text)
	return nil
}

func (c *outboundConverter) processCodeBlock(content string, textBuffer *strings.Builder, writer func(event string, payload []byte) error) error {
	var simpleCall SimpleToolCall
	if err := json.Unmarshal([]byte(content), &simpleCall); err == nil && simpleCall.Tool != "" {
		return c.sendToolUse(&ToolCall{Function: simpleCall.Tool, Parameters: simpleCall.Parameters}, writer)
	}

	var singleCall ToolCallWrapper
	if err := json.Unmarshal([]byte(content), &singleCall); err == nil && singleCall.ToolCall != nil {
		return c.sendToolUse(singleCall.ToolCall, writer)
	}

	var multipleCalls ToolCallsWrapper
	if err := json.Unmarshal([]byte(content), &multipleCalls); err == nil && len(multipleCalls.ToolCalls) > 0 {
		for i := range multipleCalls.ToolCalls {
			if err := c.sendToolUse(&multipleCalls.ToolCalls[i], writer); err != nil {
				return err
			}
		}
		return nil
	}

	textBuffer.WriteString("```json\n")
	textBuffer.WriteString(content)
	textBuffer.WriteString("\n```")
	return nil
}

func (c *outboundConverter) sendMessageStart(writer func(event string, payload []byte) error) error {
	if c.hasStarted {
		return nil
	}
	c.hasStarted = true
	event := map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            c.messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         c.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]interface{}{
				"input_tokens":  c.inputTokens,
				"output_tokens": 0,
			},
		},
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return writer("message_start", raw)
}

func (c *outboundConverter) sendContentBlockStart(blockType, name, id string, writer func(event string, payload []byte) error) error {
	if err := c.sendMessageStart(writer); err != nil {
		return err
	}
	block := map[string]interface{}{"type": blockType}
	if blockType == "text" {
		block["text"] = ""
	} else if blockType == "tool_use" {
		block["id"] = id
		block["name"] = name
		block["input"] = map[string]interface{}{}
	}
	event := map[string]interface{}{
		"type":          "content_block_start",
		"index":         c.blockIndex,
		"content_block": block,
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return writer("content_block_start", raw)
}

func (c *outboundConverter) sendTextDelta(text string, writer func(event string, payload []byte) error) error {
	if text == "" {
		return nil
	}
	if err := c.sendContentBlockStart("text", "", "", writer); err != nil {
		return err
	}
	event := map[string]interface{}{
		"type":  "content_block_delta",
		"index": c.blockIndex,
		"delta": map[string]interface{}{
			"type": "text_delta",
			"text": text,
		},
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err := writer("content_block_delta", raw); err != nil {
		return err
	}
	return c.sendContentBlockStop(writer)
}

func (c *outboundConverter) sendToolUse(toolCall *ToolCall, writer func(event string, payload []byte) error) error {
	toolUseID := "toolu_" + generateRandomID(20)
	if err := c.sendContentBlockStart("tool_use", toolCall.Function, toolUseID, writer); err != nil {
		return err
	}
	event := map[string]interface{}{
		"type":  "content_block_delta",
		"index": c.blockIndex,
		"delta": map[string]interface{}{
			"type":         "input_json_delta",
			"partial_json": string(toolCall.Parameters),
		},
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err := writer("content_block_delta", raw); err != nil {
		return err
	}
	c.emittedToolUse = true
	return c.sendContentBlockStop(writer)
}

func (c *outboundConverter) sendContentBlockStop(writer func(event string, payload []byte) error) error {
	event := map[string]interface{}{
		"type":  "content_block_stop",
		"index": c.blockIndex,
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err := writer("content_block_stop", raw); err != nil {
		return err
	}
	c.blockIndex++
	return nil
}

func (c *outboundConverter) sendEndEvents(endEvent *EndEvent, writer func(event string, payload []byte) error) error {
	stopReason := "end_turn"
	switch endEvent.FinishReason {
	case "length":
		stopReason = "max_tokens"
	case "tool_use":
		stopReason = "tool_use"
	}
	if c.emittedToolUse && stopReason == "end_turn" {
		stopReason = "tool_use"
	}
	c.outputTokens = endEvent.Usage.CompletionTokens

	deltaEvent := map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]interface{}{
			"output_tokens": c.outputTokens,
		},
	}
	deltaRaw, err := json.Marshal(deltaEvent)
	if err != nil {
		return err
	}
	if err := writer("message_delta", deltaRaw); err != nil {
		return err
	}

	stopRaw := []byte(`{"type":"message_stop"}`)
	return writer("message_stop", stopRaw)
}
