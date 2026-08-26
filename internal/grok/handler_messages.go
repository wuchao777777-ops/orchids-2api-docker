package grok

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/goccy/go-json"

	"orchids-api/internal/middleware"
)

type anthropicMessagesRequest struct {
	Model       string                 `json:"model"`
	MaxTokens   int                    `json:"max_tokens"`
	Messages    []anthropicMessage     `json:"messages"`
	System      interface{}            `json:"system,omitempty"`
	Tools       []anthropicTool        `json:"tools,omitempty"`
	ToolChoice  interface{}            `json:"tool_choice,omitempty"`
	Stream      bool                   `json:"stream,omitempty"`
	Temperature *float64               `json:"temperature,omitempty"`
	TopP        *float64               `json:"top_p,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type anthropicTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema interface{} `json:"input_schema"`
}

// HandleMessages exposes an Anthropic Messages compatibility surface for the
// Grok channel while preserving the existing Chat routing and account logic.
func (h *Handler) HandleMessages(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req anthropicMessagesRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Model) == "" || len(req.Messages) == 0 {
		writeAnthropicError(w, http.StatusBadRequest, "model and messages are required")
		return
	}
	req.Model = normalizeModelID(req.Model)
	if !middleware.APIKeyAllowsModel(r.Context(), req.Model) {
		writeAnthropicError(w, http.StatusForbidden, "API key is not allowed to use model "+req.Model)
		return
	}
	if req.MaxTokens <= 0 {
		writeAnthropicError(w, http.StatusBadRequest, "max_tokens must be greater than zero")
		return
	}
	chat, err := anthropicRequestToChat(req)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, err.Error())
		return
	}
	body, err := json.Marshal(chat)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "failed to encode request")
		return
	}
	subReq := r.Clone(r.Context())
	subReq.Method = http.MethodPost
	subReq.URL.Path = "/grok/v1/chat/completions"
	subReq.Header = r.Header.Clone()
	subReq.Header.Set("Content-Type", "application/json")
	subReq.Body = io.NopCloser(bytes.NewReader(body))
	subReq.ContentLength = int64(len(body))

	if req.Stream {
		h.serveAnthropicMessageStream(w, subReq, req.Model)
		return
	}
	rec := newCaptureResponseWriter()
	h.HandleChatCompletions(rec, subReq)
	if rec.code < 200 || rec.code >= 300 {
		writeAnthropicUpstreamError(w, rec.code, rec.body.String())
		return
	}
	var chatResponse map[string]interface{}
	if err := json.Unmarshal(rec.body.Bytes(), &chatResponse); err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "invalid chat response")
		return
	}
	writeJSON(w, anthropicResponseFromChat(req.Model, chatResponse))
}

func anthropicRequestToChat(req anthropicMessagesRequest) (ChatCompletionsRequest, error) {
	messages := make([]ChatMessage, 0, len(req.Messages)+1)
	if system := anthropicSystemText(req.System); system != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: system})
	}
	for _, message := range req.Messages {
		converted, err := anthropicMessageToChat(message)
		if err != nil {
			return ChatCompletionsRequest{}, err
		}
		messages = append(messages, converted...)
	}
	tools := make([]ToolDef, 0, len(req.Tools))
	for _, tool := range req.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return ChatCompletionsRequest{}, fmt.Errorf("tool name is required")
		}
		parameters := tool.InputSchema
		if parameters == nil {
			parameters = map[string]interface{}{"type": "object"}
		}
		tools = append(tools, ToolDef{Type: "function", Function: map[string]interface{}{
			"name": name, "description": tool.Description, "parameters": parameters,
		}})
	}
	maxTokens := req.MaxTokens
	return ChatCompletionsRequest{
		Model:          req.Model,
		Messages:       messages,
		Stream:         req.Stream,
		StreamProvided: true,
		Temperature:    req.Temperature,
		TopP:           req.TopP,
		MaxTokens:      &maxTokens,
		Tools:          tools,
		ToolChoice:     anthropicToolChoiceToOpenAI(req.ToolChoice),
	}, nil
}

func anthropicSystemText(value interface{}) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, raw := range v {
			block, _ := raw.(map[string]interface{})
			if strings.EqualFold(strings.TrimSpace(fmt.Sprint(block["type"])), "text") {
				if text := strings.TrimSpace(fmt.Sprint(block["text"])); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func anthropicMessageToChat(message anthropicMessage) ([]ChatMessage, error) {
	role := strings.ToLower(strings.TrimSpace(message.Role))
	if role != "user" && role != "assistant" {
		return nil, fmt.Errorf("unsupported message role %q", message.Role)
	}
	if text, ok := message.Content.(string); ok {
		return []ChatMessage{{Role: role, Content: text}}, nil
	}
	blocks, ok := message.Content.([]interface{})
	if !ok {
		return nil, fmt.Errorf("message content must be a string or array")
	}
	content := make([]interface{}, 0, len(blocks))
	toolCalls := make([]ToolCall, 0)
	toolResults := make([]ChatMessage, 0)
	for _, raw := range blocks {
		block, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(fmt.Sprint(block["type"]))) {
		case "text":
			content = append(content, map[string]interface{}{"type": "text", "text": fmt.Sprint(block["text"])})
		case "image":
			url := anthropicSourceURL(block["source"])
			if url == "" {
				return nil, fmt.Errorf("image source is invalid")
			}
			content = append(content, map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": url}})
		case "document":
			url := anthropicSourceURL(block["source"])
			if url == "" {
				return nil, fmt.Errorf("document source is invalid")
			}
			content = append(content, map[string]interface{}{"type": "file_url", "file_url": map[string]interface{}{"url": url}})
		case "tool_use":
			if role != "assistant" {
				return nil, fmt.Errorf("tool_use is only valid in assistant messages")
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   strings.TrimSpace(fmt.Sprint(block["id"])),
				Type: "function",
				Function: map[string]interface{}{
					"name": block["name"], "arguments": block["input"],
				},
			})
		case "tool_result":
			if role != "user" {
				return nil, fmt.Errorf("tool_result is only valid in user messages")
			}
			toolResults = append(toolResults, ChatMessage{
				Role: "tool", ToolCallID: strings.TrimSpace(fmt.Sprint(block["tool_use_id"])),
				Content: anthropicBlockContentText(block["content"]),
			})
		}
	}
	out := make([]ChatMessage, 0, 1+len(toolResults))
	// Anthropic commonly places tool_result blocks before any follow-up user
	// text in the same message. OpenAI requires the corresponding tool-role
	// messages to precede that user message.
	out = append(out, toolResults...)
	if len(content) > 0 || len(toolCalls) > 0 {
		var normalizedContent interface{} = content
		if len(content) == 0 {
			normalizedContent = ""
		}
		out = append(out, ChatMessage{Role: role, Content: normalizedContent, ToolCalls: toolCalls})
	}
	if len(out) == 0 {
		out = append(out, ChatMessage{Role: role, Content: ""})
	}
	return out, nil
}

func anthropicSourceURL(raw interface{}) string {
	source, _ := raw.(map[string]interface{})
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(source["type"]))) {
	case "base64":
		mediaType := strings.TrimSpace(fmt.Sprint(source["media_type"]))
		data := strings.TrimSpace(fmt.Sprint(source["data"]))
		if mediaType != "" && data != "" {
			return "data:" + mediaType + ";base64," + data
		}
	case "url":
		return strings.TrimSpace(fmt.Sprint(source["url"]))
	}
	return ""
}

func anthropicBlockContentText(raw interface{}) string {
	if text, ok := raw.(string); ok {
		return text
	}
	blocks, _ := raw.([]interface{})
	parts := make([]string, 0, len(blocks))
	for _, item := range blocks {
		block, _ := item.(map[string]interface{})
		if text := fmt.Sprint(block["text"]); text != "<nil>" && text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func anthropicToolChoiceToOpenAI(raw interface{}) interface{} {
	choice, ok := raw.(map[string]interface{})
	if !ok {
		return raw
	}
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(choice["type"]))) {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "none":
		return "none"
	case "tool":
		return map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": choice["name"]}}
	default:
		return nil
	}
}

func anthropicResponseFromChat(model string, chat map[string]interface{}) map[string]interface{} {
	content := make([]interface{}, 0)
	stopReason := "end_turn"
	choices, _ := chat["choices"].([]interface{})
	if len(choices) > 0 {
		choice, _ := choices[0].(map[string]interface{})
		message, _ := choice["message"].(map[string]interface{})
		if thinking := strings.TrimSpace(fmt.Sprint(firstDefined(message["reasoning_content"], message["reasoning"]))); thinking != "" && thinking != "<nil>" {
			content = append(content, map[string]interface{}{"type": "thinking", "thinking": thinking})
		}
		if text := strings.TrimSpace(fmt.Sprint(message["content"])); text != "" && text != "<nil>" {
			content = append(content, map[string]interface{}{"type": "text", "text": text})
		}
		toolCalls, _ := message["tool_calls"].([]interface{})
		for _, raw := range toolCalls {
			call, _ := raw.(map[string]interface{})
			fn, _ := call["function"].(map[string]interface{})
			input := interface{}(map[string]interface{}{})
			switch args := fn["arguments"].(type) {
			case string:
				_ = json.Unmarshal([]byte(args), &input)
			case nil:
			default:
				input = args
			}
			content = append(content, map[string]interface{}{
				"type": "tool_use", "id": call["id"], "name": fn["name"], "input": input,
			})
		}
		stopReason = openAIFinishToAnthropic(fmt.Sprint(choice["finish_reason"]))
	}
	if len(content) == 0 {
		content = append(content, map[string]interface{}{"type": "text", "text": ""})
	}
	usage, _ := chat["usage"].(map[string]interface{})
	return map[string]interface{}{
		"id":   firstNonEmpty(interfaceString(chat["id"]), "msg_"+randomHex(12)),
		"type": "message", "role": "assistant", "model": model,
		"content": content, "stop_reason": stopReason, "stop_sequence": nil,
		"usage": anthropicUsageFromOpenAI(usage),
	}
}

func firstDefined(values ...interface{}) interface{} {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func interfaceString(value interface{}) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func openAIFinishToAnthropic(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tool_calls", "tool_use":
		return "tool_use"
	case "length", "max_tokens":
		return "max_tokens"
	case "stop", "end_turn", "", "<nil>":
		return "end_turn"
	default:
		return "end_turn"
	}
}

func anthropicUsageFromOpenAI(usage map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{
		"input_tokens":  interfaceToInt(usage["prompt_tokens"]),
		"output_tokens": interfaceToInt(usage["completion_tokens"]),
	}
	if details, ok := usage["prompt_tokens_details"].(map[string]interface{}); ok {
		if cached := interfaceToInt(details["cached_tokens"]); cached > 0 {
			result["cache_read_input_tokens"] = cached
		}
	}
	return result
}

type streamingChatWriter struct {
	header http.Header
	pipe   *io.PipeWriter
	status int
	once   sync.Once
	ready  chan struct{}
}

func newStreamingChatWriter(pipe *io.PipeWriter) *streamingChatWriter {
	return &streamingChatWriter{header: make(http.Header), pipe: pipe, ready: make(chan struct{})}
}

func (w *streamingChatWriter) Header() http.Header { return w.header }
func (w *streamingChatWriter) WriteHeader(status int) {
	w.once.Do(func() { w.status = status; close(w.ready) })
}
func (w *streamingChatWriter) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.pipe.Write(data)
}
func (w *streamingChatWriter) Flush() { w.WriteHeader(http.StatusOK) }

func (h *Handler) serveAnthropicMessageStream(w http.ResponseWriter, req *http.Request, model string) {
	reader, writer := io.Pipe()
	streamWriter := newStreamingChatWriter(writer)
	go func() {
		h.HandleChatCompletions(streamWriter, req)
		streamWriter.WriteHeader(http.StatusOK)
		_ = writer.Close()
	}()
	<-streamWriter.ready
	for key, values := range streamWriter.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	if streamWriter.status < 200 || streamWriter.status >= 300 {
		body, _ := io.ReadAll(reader)
		writeAnthropicUpstreamError(w, streamWriter.status, string(body))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_ = translateOpenAIChatStreamToAnthropic(w, reader, model)
}

type anthropicStreamState struct {
	id          string
	model       string
	nextIndex   int
	textIndex   int
	thinkIndex  int
	toolIndexes map[int]int
	open        map[int]bool
	usage       map[string]interface{}
	stopReason  string
}

func translateOpenAIChatStreamToAnthropic(w io.Writer, reader io.Reader, model string) error {
	state := &anthropicStreamState{
		id: "msg_" + randomHex(12), model: model, textIndex: -1, thinkIndex: -1,
		toolIndexes: map[int]int{}, open: map[int]bool{}, usage: map[string]interface{}{},
	}
	writeAnthropicSSE(w, "message_start", map[string]interface{}{
		"type": "message_start", "message": map[string]interface{}{
			"id": state.id, "type": "message", "role": "assistant", "model": model,
			"content": []interface{}{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
		},
	})

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if id := interfaceString(chunk["id"]); id != "" {
			state.id = id
		}
		if usage, ok := chunk["usage"].(map[string]interface{}); ok {
			state.usage = anthropicUsageFromOpenAI(usage)
		}
		choices, _ := chunk["choices"].([]interface{})
		for _, rawChoice := range choices {
			choice, _ := rawChoice.(map[string]interface{})
			delta, _ := choice["delta"].(map[string]interface{})
			if message, ok := choice["message"].(map[string]interface{}); ok && len(delta) == 0 {
				delta = message
			}
			if thinking := streamString(firstDefined(delta["reasoning_content"], delta["reasoning"])); thinking != "" {
				state.writeThinking(w, thinking)
			}
			if content := streamString(delta["content"]); content != "" {
				state.writeText(w, content)
			}
			if toolCalls, ok := delta["tool_calls"].([]interface{}); ok {
				for _, rawCall := range toolCalls {
					state.writeToolCall(w, rawCall)
				}
			}
			if finish := strings.TrimSpace(fmt.Sprint(choice["finish_reason"])); finish != "" && finish != "<nil>" {
				state.stopReason = openAIFinishToAnthropic(finish)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	state.finish(w)
	return nil
}

func streamString(value interface{}) string {
	text, _ := value.(string)
	return text
}

func (s *anthropicStreamState) startBlock(w io.Writer, block map[string]interface{}) int {
	index := s.nextIndex
	s.nextIndex++
	s.open[index] = true
	writeAnthropicSSE(w, "content_block_start", map[string]interface{}{
		"type": "content_block_start", "index": index, "content_block": block,
	})
	return index
}

func (s *anthropicStreamState) closeBlock(w io.Writer, index int) {
	if !s.open[index] {
		return
	}
	writeAnthropicSSE(w, "content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": index})
	delete(s.open, index)
}

func (s *anthropicStreamState) closeTextualBlocks(w io.Writer) {
	if s.textIndex >= 0 {
		s.closeBlock(w, s.textIndex)
		s.textIndex = -1
	}
	if s.thinkIndex >= 0 {
		s.closeBlock(w, s.thinkIndex)
		s.thinkIndex = -1
	}
}

func (s *anthropicStreamState) writeText(w io.Writer, text string) {
	if s.thinkIndex >= 0 {
		s.closeBlock(w, s.thinkIndex)
		s.thinkIndex = -1
	}
	if s.textIndex < 0 {
		s.textIndex = s.startBlock(w, map[string]interface{}{"type": "text", "text": ""})
	}
	writeAnthropicSSE(w, "content_block_delta", map[string]interface{}{
		"type": "content_block_delta", "index": s.textIndex,
		"delta": map[string]interface{}{"type": "text_delta", "text": text},
	})
}

func (s *anthropicStreamState) writeThinking(w io.Writer, thinking string) {
	if s.textIndex >= 0 {
		s.closeBlock(w, s.textIndex)
		s.textIndex = -1
	}
	if s.thinkIndex < 0 {
		s.thinkIndex = s.startBlock(w, map[string]interface{}{"type": "thinking", "thinking": "", "signature": ""})
	}
	writeAnthropicSSE(w, "content_block_delta", map[string]interface{}{
		"type": "content_block_delta", "index": s.thinkIndex,
		"delta": map[string]interface{}{"type": "thinking_delta", "thinking": thinking},
	})
}

func (s *anthropicStreamState) writeToolCall(w io.Writer, raw interface{}) {
	call, _ := raw.(map[string]interface{})
	callIndex := interfaceToInt(call["index"])
	fn, _ := call["function"].(map[string]interface{})
	blockIndex, exists := s.toolIndexes[callIndex]
	if !exists {
		s.closeTextualBlocks(w)
		id := firstNonEmpty(interfaceString(call["id"]), "toolu_"+randomHex(12))
		name := strings.TrimSpace(fmt.Sprint(fn["name"]))
		blockIndex = s.startBlock(w, map[string]interface{}{"type": "tool_use", "id": id, "name": name, "input": map[string]interface{}{}})
		s.toolIndexes[callIndex] = blockIndex
	}
	if args := streamString(fn["arguments"]); args != "" {
		writeAnthropicSSE(w, "content_block_delta", map[string]interface{}{
			"type": "content_block_delta", "index": blockIndex,
			"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": args},
		})
	}
}

func (s *anthropicStreamState) finish(w io.Writer) {
	indexes := make([]int, 0, len(s.open))
	for index := range s.open {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		s.closeBlock(w, index)
	}
	if s.stopReason == "" {
		if len(s.toolIndexes) > 0 {
			s.stopReason = "tool_use"
		} else {
			s.stopReason = "end_turn"
		}
	}
	writeAnthropicSSE(w, "message_delta", map[string]interface{}{
		"type": "message_delta", "delta": map[string]interface{}{"stop_reason": s.stopReason, "stop_sequence": nil},
		"usage": map[string]interface{}{"output_tokens": interfaceToInt(s.usage["output_tokens"])},
	})
	writeAnthropicSSE(w, "message_stop", map[string]interface{}{"type": "message_stop"})
}

func writeAnthropicSSE(w io.Writer, event string, payload interface{}) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeAnthropicError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"type": "error", "error": map[string]interface{}{"type": "invalid_request_error", "message": message},
	})
}

func writeAnthropicUpstreamError(w http.ResponseWriter, status int, body string) {
	if status < 400 {
		status = http.StatusBadGateway
	}
	message := strings.TrimSpace(body)
	if message == "" {
		message = http.StatusText(status)
	}
	writeAnthropicError(w, status, message)
}
