package grok

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/middleware"
	"orchids-api/internal/store"
)

type captureResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func newCaptureResponseWriter() *captureResponseWriter {
	return &captureResponseWriter{header: make(http.Header), code: http.StatusOK}
}

func (w *captureResponseWriter) Header() http.Header {
	return w.header
}

func (w *captureResponseWriter) WriteHeader(code int) {
	if code != 0 {
		w.code = code
	}
}

func (w *captureResponseWriter) Write(p []byte) (int, error) {
	return w.body.Write(p)
}

func (w *captureResponseWriter) Flush() {}

type ResponsesCreateRequest struct {
	Model              string                   `json:"model"`
	Input              interface{}              `json:"input"`
	Instructions       string                   `json:"instructions,omitempty"`
	Stream             bool                     `json:"stream,omitempty"`
	StreamProvided     bool                     `json:"-"`
	Reasoning          map[string]interface{}   `json:"reasoning,omitempty"`
	Temperature        *float64                 `json:"temperature,omitempty"`
	TopP               *float64                 `json:"top_p,omitempty"`
	MaxOutputTokens    *int                     `json:"max_output_tokens,omitempty"`
	Tools              []map[string]interface{} `json:"tools,omitempty"`
	ToolChoice         interface{}              `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool                    `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID string                   `json:"previous_response_id,omitempty"`
	Store              *bool                    `json:"store,omitempty"`
	Metadata           map[string]interface{}   `json:"metadata,omitempty"`
	Truncation         string                   `json:"truncation,omitempty"`
	Include            []string                 `json:"include,omitempty"`
	Background         *bool                    `json:"background,omitempty"`
	PromptCacheKey     string                   `json:"prompt_cache_key,omitempty"`
}

func (r *ResponsesCreateRequest) UnmarshalJSON(data []byte) error {
	type rawResponsesCreateRequest struct {
		Model              interface{}              `json:"model"`
		Input              interface{}              `json:"input"`
		Instructions       interface{}              `json:"instructions,omitempty"`
		Stream             interface{}              `json:"stream,omitempty"`
		Reasoning          map[string]interface{}   `json:"reasoning,omitempty"`
		Temperature        interface{}              `json:"temperature,omitempty"`
		TopP               interface{}              `json:"top_p,omitempty"`
		MaxOutputTokens    interface{}              `json:"max_output_tokens,omitempty"`
		Tools              []map[string]interface{} `json:"tools,omitempty"`
		ToolChoice         interface{}              `json:"tool_choice,omitempty"`
		ParallelToolCalls  interface{}              `json:"parallel_tool_calls,omitempty"`
		PreviousResponseID interface{}              `json:"previous_response_id,omitempty"`
		Store              interface{}              `json:"store,omitempty"`
		Metadata           map[string]interface{}   `json:"metadata,omitempty"`
		Truncation         interface{}              `json:"truncation,omitempty"`
		Include            []string                 `json:"include,omitempty"`
		Background         interface{}              `json:"background,omitempty"`
		PromptCacheKey     interface{}              `json:"prompt_cache_key,omitempty"`
	}

	var raw rawResponsesCreateRequest
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	stream, err := parseLooseBoolAny(raw.Stream)
	if err != nil {
		return err
	}
	temp, err := parseLooseFloatAny(raw.Temperature)
	if err != nil {
		return err
	}
	topP, err := parseLooseFloatAny(raw.TopP)
	if err != nil {
		return err
	}
	maxOutputTokens, err := parseLooseIntAny(raw.MaxOutputTokens)
	if err != nil {
		return err
	}
	var rawMap map[string]json.RawMessage
	_ = json.Unmarshal(data, &rawMap)
	_, streamProvided := rawMap["stream"]
	var parallel *bool
	if _, ok := rawMap["parallel_tool_calls"]; ok {
		v, err := parseLooseBoolAnyForField(raw.ParallelToolCalls, "parallel_tool_calls")
		if err != nil {
			return err
		}
		parallel = &v
	}
	var store *bool
	if _, ok := rawMap["store"]; ok {
		v, err := parseLooseBoolAnyForField(raw.Store, "store")
		if err != nil {
			return err
		}
		store = &v
	}
	var background *bool
	if _, ok := rawMap["background"]; ok {
		v, err := parseLooseBoolAnyForField(raw.Background, "background")
		if err != nil {
			return err
		}
		background = &v
	}
	var maxOutput *int
	if _, ok := rawMap["max_output_tokens"]; ok {
		maxOutput = &maxOutputTokens
	}

	r.Model = parseLooseStringAny(raw.Model)
	r.Input = raw.Input
	r.Instructions = parseLooseStringAny(raw.Instructions)
	r.Stream = stream
	r.StreamProvided = streamProvided
	r.Reasoning = raw.Reasoning
	r.Temperature = temp
	r.TopP = topP
	r.MaxOutputTokens = maxOutput
	r.Tools = raw.Tools
	r.ToolChoice = raw.ToolChoice
	r.ParallelToolCalls = parallel
	r.PreviousResponseID = parseLooseStringAny(raw.PreviousResponseID)
	r.Store = store
	r.Metadata = raw.Metadata
	r.Truncation = parseLooseStringAny(raw.Truncation)
	r.Include = raw.Include
	r.Background = background
	r.PromptCacheKey = parseLooseStringAny(raw.PromptCacheKey)
	return nil
}

func (h *Handler) HandleResponses(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	// Keep the original object for the Build CLI route.  The CLI upstream
	// speaks Responses natively, so translating it through Chat Completions
	// would drop valid fields such as previous_response_id and metadata.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	var req ResponsesCreateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	req.Model = normalizeModelID(req.Model)
	if !requireAPIKeyModel(w, r, req.Model) {
		return
	}
	h.applyDefaultResponsesStream(&req)
	var nativePayload map[string]interface{}
	if err := json.Unmarshal(body, &nativePayload); err != nil || nativePayload == nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if _, provided := nativePayload["stream"]; !provided {
		nativePayload["stream"] = req.Stream
	}
	identityMessages, _ := responsesInputToMessages(req.Input)
	session := prepareGrokSession(r, req.Model, req.PromptCacheKey, identityMessages)
	if session.Key != "" {
		nativePayload["prompt_cache_key"] = session.Key
		r = r.WithContext(withGrokSession(r.Context(), session))
		if session.Replay {
			h.applyNativeReasoningReplay(req.Model, session.Key, nativePayload)
		}
	}

	spec, resolved := h.resolveConversationModel(r.Context(), req.Model)
	if resolved && modelRoutedToCLI(spec, h.cfg) {
		h.handleNativeCLIResponses(w, r, req.Model, spec, nativePayload)
		return
	}
	if !resolved {
		http.Error(w, modelNotFoundMessage(req.Model), http.StatusBadRequest)
		return
	}
	if !spec.SupportsConversation() {
		http.Error(w, fmt.Sprintf("model %s does not support responses", req.Model), http.StatusBadRequest)
		return
	}
	if previousID := strings.TrimSpace(req.PreviousResponseID); previousID != "" {
		owner := strings.TrimSpace(middleware.APIKeyFingerprint(r.Context()))
		if owner == "" {
			owner = "anonymous"
		}
		previous, lookupErr := h.getStoredResponse(r, previousID, owner)
		if lookupErr != nil {
			writeStoredResponseLookupError(w, lookupErr, "previous response not found")
			return
		}
		if previous == nil || len(previous.Body) == 0 || previous.Provider == ProviderBuild {
			writeResponsesAPIError(w, http.StatusNotFound, "response_not_found", "previous response not found")
			return
		}
		if previous.Provider != providerForModelSpec(spec) {
			writeResponsesAPIError(w, http.StatusBadRequest, "invalid_request_error", "previous response provider is incompatible")
			return
		}
		req.Input, err = expandStoredResponseInput(previous.Body, req.Input)
		if err != nil {
			writeResponsesAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		if previous.PromptCacheKey != "" {
			session = grokSessionContext{Key: previous.PromptCacheKey, Replay: true, Model: req.Model}
			req.PromptCacheKey = previous.PromptCacheKey
			r = r.WithContext(withGrokSession(r.Context(), session))
		}
	}
	if err := validateResponsesCompatibility(req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	chatReq, err := chatRequestFromResponses(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	raw, err := json.Marshal(chatReq)
	if err != nil {
		http.Error(w, "failed to build chat request", http.StatusInternalServerError)
		return
	}

	subReq := r.Clone(r.Context())
	subReq.Method = http.MethodPost
	subReq.URL.Path = "/v1/chat/completions"
	subReq.Header = make(http.Header)
	subReq.Header.Set("Content-Type", "application/json")
	subReq.Body = io.NopCloser(bytes.NewReader(raw))
	subReq.ContentLength = int64(len(raw))

	if chatReq.Stream {
		reader, writer := io.Pipe()
		streamWriter := newStreamingChatWriter(writer)
		go func() {
			h.HandleChatCompletions(streamWriter, subReq)
			streamWriter.WriteHeader(http.StatusOK)
			_ = writer.Close()
		}()
		<-streamWriter.ready
		if streamWriter.status < 200 || streamWriter.status >= 300 {
			body, _ := io.ReadAll(reader)
			for key, values := range streamWriter.header {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(streamWriter.status)
			_, _ = w.Write(body)
			return
		}
		writeResponsesStreamFromChatReaderRequest(w, req, reader)
		return
	}

	rec := newCaptureResponseWriter()
	h.HandleChatCompletions(rec, subReq)
	if rec.code < 200 || rec.code >= 300 {
		copyCapturedResponse(w, rec)
		return
	}
	var chat map[string]interface{}
	if err := json.Unmarshal(rec.body.Bytes(), &chat); err != nil {
		http.Error(w, "chat response parse error: "+err.Error(), http.StatusBadGateway)
		return
	}
	response := responsesObjectFromChat(req.Model, chat)
	if len(req.Metadata) > 0 {
		response["metadata"] = req.Metadata
	}
	if strings.TrimSpace(req.Truncation) != "" {
		response["truncation"] = req.Truncation
	}
	if req.Store != nil && *req.Store {
		encoded, encodeErr := json.Marshal(response)
		if encodeErr != nil {
			http.Error(w, "failed to store response", http.StatusInternalServerError)
			return
		}
		owner := strings.TrimSpace(middleware.APIKeyFingerprint(r.Context()))
		if owner == "" {
			owner = "anonymous"
		}
		if saveErr := h.saveStoredResponse(r, &store.StoredResponse{
			ResponseID: parseLooseStringAny(response["id"]), OwnerHash: owner, Model: req.Model,
			Provider: providerForModelSpec(spec), PromptCacheKey: sessionFromContext(r.Context()).Key,
			ContentType: "application/json", Body: encoded,
		}); saveErr != nil {
			http.Error(w, "failed to store response", http.StatusServiceUnavailable)
			return
		}
	}
	writeJSON(w, response)
}

func providerForModelSpec(spec ModelSpec) string {
	if spec.Upstream == UpstreamConsole || strings.TrimSpace(spec.ConsoleModel) != "" {
		return ProviderConsole
	}
	if spec.Upstream == UpstreamCLI {
		return ProviderBuild
	}
	return ProviderWeb
}

func expandStoredResponseInput(responseBody []byte, current interface{}) (interface{}, error) {
	var previous map[string]interface{}
	if json.Unmarshal(responseBody, &previous) != nil {
		return nil, fmt.Errorf("stored response is invalid")
	}
	output := interfaceSlice(previous["output"])
	if len(output) == 0 {
		return nil, fmt.Errorf("stored response has no output")
	}
	currentItems := make([]interface{}, 0)
	switch value := current.(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			currentItems = append(currentItems, map[string]interface{}{
				"type": "message", "role": "user", "content": []interface{}{map[string]interface{}{"type": "input_text", "text": value}},
			})
		}
	case []interface{}:
		currentItems = append(currentItems, value...)
	default:
		return nil, fmt.Errorf("input must be a string or an array")
	}
	if len(currentItems) == 0 {
		return nil, fmt.Errorf("input is required")
	}
	combined := make([]interface{}, 0, len(output)+len(currentItems))
	combined = append(combined, output...)
	combined = append(combined, currentItems...)
	return combined, nil
}

// handleNativeCLIResponses proxies Build OAuth Responses requests without a
// Chat-Completions compatibility conversion.  Besides preserving the official
// request and event schema, this keeps SSE streaming realtime and bounded by
// the normal HTTP backpressure instead of buffering the whole completion.
func (h *Handler) handleNativeCLIResponses(w http.ResponseWriter, r *http.Request, modelID string, spec ModelSpec, payload map[string]interface{}) {
	h.handleNativeCLIResponsesAt(w, r, modelID, spec, payload, "/responses", true)
}

func copyNativeCLIResponseHeaders(dst, src http.Header) {
	// Forward only end-to-end response metadata. Hop-by-hop headers must not be
	// copied because net/http owns the downstream connection.
	for _, key := range []string{"Content-Type", "Cache-Control", "X-Request-Id", "X-Request-ID"} {
		if values, ok := src[key]; ok {
			dst.Del(key)
			for _, value := range values {
				dst.Add(key, value)
			}
		}
	}
}

func streamNativeCLIResponse(w http.ResponseWriter, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}
	}
}

func validateResponsesCompatibility(req ResponsesCreateRequest) error {
	if req.Store != nil && *req.Store && req.Stream {
		return fmt.Errorf("store=true requires stream=false for this provider")
	}
	if truncation := strings.ToLower(strings.TrimSpace(req.Truncation)); truncation != "" && truncation != "auto" && truncation != "disabled" {
		return fmt.Errorf("truncation must be auto or disabled")
	}
	if req.Background != nil && *req.Background {
		return fmt.Errorf("background=true is not supported")
	}
	return nil
}

func (h *Handler) applyDefaultResponsesStream(req *ResponsesCreateRequest) {
	if req == nil || req.StreamProvided {
		return
	}
	req.Stream = h.defaultChatStream()
}

func chatRequestFromResponses(req ResponsesCreateRequest) (ChatCompletionsRequest, error) {
	model := normalizeModelID(req.Model)
	if strings.TrimSpace(model) == "" {
		return ChatCompletionsRequest{}, fmt.Errorf("model is required")
	}
	messages, err := responsesInputToMessages(req.Input)
	if err != nil {
		return ChatCompletionsRequest{}, err
	}
	if instructions := strings.TrimSpace(req.Instructions); instructions != "" {
		messages = append([]ChatMessage{{Role: "system", Content: instructions}}, messages...)
	}
	reasoningEffort := responsesReasoningEffort(req.Reasoning)
	out := ChatCompletionsRequest{
		Model:             model,
		Messages:          messages,
		Stream:            req.Stream,
		StreamProvided:    true,
		ReasoningEffort:   reasoningEffort,
		Temperature:       req.Temperature,
		TopP:              req.TopP,
		Tools:             responsesToolsToChatTools(req.Tools),
		ToolChoice:        responsesToolChoiceToChat(req.ToolChoice),
		ParallelToolCalls: req.ParallelToolCalls,
		MaxTokens:         req.MaxOutputTokens,
		PromptCacheKey:    req.PromptCacheKey,
	}
	return out, nil
}

func responsesInputToMessages(input interface{}) ([]ChatMessage, error) {
	switch v := input.(type) {
	case nil:
		return nil, fmt.Errorf("input is required")
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("input is required")
		}
		return []ChatMessage{{Role: "user", Content: v}}, nil
	case []interface{}:
		messages := make([]ChatMessage, 0, len(v))
		for _, raw := range v {
			item, _ := raw.(map[string]interface{})
			if item == nil {
				continue
			}
			itemType := strings.ToLower(strings.TrimSpace(fmt.Sprint(item["type"])))
			if itemType == "" {
				if strings.TrimSpace(fmt.Sprint(item["role"])) != "" {
					itemType = "message"
				}
			}
			switch itemType {
			case "function_call":
				name := parseLooseStringAny(item["name"])
				if name == "" {
					continue
				}
				args := "{}"
				if rawArgs := item["arguments"]; rawArgs != nil {
					switch x := rawArgs.(type) {
					case string:
						if strings.TrimSpace(x) != "" {
							args = strings.TrimSpace(x)
						}
					default:
						if buf, err := json.Marshal(x); err == nil {
							args = string(buf)
						}
					}
				}
				messages = append(messages, ChatMessage{
					Role:    "assistant",
					Content: nil,
					ToolCalls: []ToolCall{{
						ID:   strings.TrimSpace(fmt.Sprint(item["call_id"])),
						Type: "function",
						Function: map[string]interface{}{
							"name":      name,
							"arguments": args,
						},
					}},
				})
			case "function_call_output":
				messages = append(messages, ChatMessage{
					Role:       "tool",
					ToolCallID: strings.TrimSpace(fmt.Sprint(item["call_id"])),
					Content:    strings.TrimSpace(fmt.Sprint(item["output"])),
				})
			case "custom_tool_call":
				name := firstNonEmpty(parseLooseStringAny(item["name"]), "custom_tool")
				messages = append(messages, ChatMessage{Role: "assistant", Content: nil, ToolCalls: []ToolCall{{
					ID: firstNonEmpty(parseLooseStringAny(item["call_id"]), parseLooseStringAny(item["id"])), Type: "function",
					Function: map[string]interface{}{"name": name, "arguments": firstNonNil(item["input"], item["arguments"], "{}")},
				}}})
			case "custom_tool_call_output":
				messages = append(messages, ChatMessage{Role: "tool", ToolCallID: parseLooseStringAny(item["call_id"]), Content: parseLooseStringAny(item["output"])})
			case "reasoning":
				messages = append(messages, ChatMessage{
					Role: "assistant", Content: "",
					ReasoningContent: responsesReasoningSummary(item), ReasoningEncryptedContent: parseLooseStringAny(item["encrypted_content"]),
				})
			case "message":
				role := parseLooseStringAny(item["role"])
				if role == "" {
					role = "user"
				}
				messages = append(messages, ChatMessage{
					Role:    role,
					Content: normalizeResponsesMessageContent(item["content"]),
				})
			}
		}
		if len(messages) == 0 {
			return nil, fmt.Errorf("input is required")
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("input must be a string or an array")
	}
}

func responsesReasoningSummary(item map[string]interface{}) string {
	parts := make([]string, 0)
	for _, raw := range interfaceSlice(item["summary"]) {
		part, _ := raw.(map[string]interface{})
		if text := parseLooseStringAny(part["text"]); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "")
}

func normalizeResponsesMessageContent(content interface{}) interface{} {
	parts, ok := content.([]interface{})
	if !ok {
		return content
	}
	out := make([]interface{}, 0, len(parts))
	for _, raw := range parts {
		part, _ := raw.(map[string]interface{})
		if part == nil {
			continue
		}
		ptype := strings.ToLower(strings.TrimSpace(fmt.Sprint(part["type"])))
		switch ptype {
		case "input_text", "output_text":
			out = append(out, map[string]interface{}{"type": "text", "text": fmt.Sprint(part["text"])})
		case "input_image", "image":
			if url := responsesPartURL(part, []string{"image_url", "source"}, []string{"url"}); url != "" {
				out = append(out, map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": url}})
			}
		case "input_file", "file":
			url := responsesPartURL(part, []string{"file", "file_url", "source"}, []string{"url", "file_url", "data"})
			if url == "" {
				url = parseLooseStringAny(part["file_id"])
			}
			if url != "" {
				out = append(out, map[string]interface{}{"type": "file", "file": map[string]interface{}{"url": url}})
			}
		default:
			out = append(out, part)
		}
	}
	return out
}

func responsesPartURL(part map[string]interface{}, keys, nestedKeys []string) string {
	for _, key := range keys {
		raw := part[key]
		switch v := raw.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		case map[string]interface{}:
			for _, nestedKey := range nestedKeys {
				if s := parseLooseStringAny(v[nestedKey]); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func responsesToolsToChatTools(tools []map[string]interface{}) []ToolDef {
	out := make([]ToolDef, 0, len(tools))
	for _, tool := range tools {
		if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(tool["type"])), "function") {
			continue
		}
		if fn, _ := tool["function"].(map[string]interface{}); fn != nil {
			if strings.TrimSpace(fmt.Sprint(fn["name"])) != "" {
				out = append(out, ToolDef{Type: "function", Function: fn})
			}
			continue
		}
		name := parseLooseStringAny(tool["name"])
		if name == "" {
			continue
		}
		out = append(out, ToolDef{Type: "function", Function: map[string]interface{}{
			"name":        name,
			"description": strings.TrimSpace(fmt.Sprint(tool["description"])),
			"parameters":  firstNonNil(tool["parameters"], map[string]interface{}{}),
		}})
	}
	return out
}

func responsesToolChoiceToChat(choice interface{}) interface{} {
	if choice == nil {
		return nil
	}
	m, _ := choice.(map[string]interface{})
	if m == nil || !strings.EqualFold(strings.TrimSpace(fmt.Sprint(m["type"])), "function") {
		return choice
	}
	if _, ok := m["function"].(map[string]interface{}); ok {
		return choice
	}
	name := parseLooseStringAny(m["name"])
	if name == "" {
		return choice
	}
	return map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": name}}
}

func responsesReasoningEffort(reasoning map[string]interface{}) *string {
	if len(reasoning) == 0 {
		return nil
	}
	if effort := strings.ToLower(strings.TrimSpace(fmt.Sprint(reasoning["effort"]))); effort != "" && effort != "<nil>" {
		return &effort
	}
	return nil
}

func firstNonNil(values ...interface{}) interface{} {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func responsesObjectFromChat(model string, chat map[string]interface{}) map[string]interface{} {
	output := responsesOutputFromChat(chat)
	return map[string]interface{}{
		"id":                  "resp_" + randomHex(12),
		"object":              "response",
		"created_at":          time.Now().Unix(),
		"status":              "completed",
		"model":               firstNonEmpty(strings.TrimSpace(fmt.Sprint(chat["model"])), model),
		"output":              output,
		"parallel_tool_calls": true,
		"tool_choice":         "auto",
		"usage":               responsesUsageFromChat(chat["usage"]),
	}
}

func responsesOutputFromChat(chat map[string]interface{}) []interface{} {
	choices := interfaceSlice(chat["choices"])
	if len(choices) == 0 {
		return []interface{}{}
	}
	choice, _ := choices[0].(map[string]interface{})
	message, _ := choice["message"].(map[string]interface{})
	if message == nil {
		return []interface{}{}
	}
	out := make([]interface{}, 0, 2)
	if reasoning := strings.TrimSpace(fmt.Sprint(firstNonNil(message["reasoning_content"], message["reasoning"]))); reasoning != "" && reasoning != "<nil>" {
		item := map[string]interface{}{
			"id": "rs_" + randomHex(12), "type": "reasoning", "status": "completed",
			"summary": []interface{}{map[string]interface{}{"type": "summary_text", "text": reasoning}},
		}
		if encrypted := strings.TrimSpace(fmt.Sprint(message["reasoning_encrypted_content"])); encrypted != "" && encrypted != "<nil>" {
			item["encrypted_content"] = encrypted
		}
		out = append(out, item)
	} else if encrypted := strings.TrimSpace(fmt.Sprint(message["reasoning_encrypted_content"])); encrypted != "" && encrypted != "<nil>" {
		out = append(out, map[string]interface{}{
			"id": "rs_" + randomHex(12), "type": "reasoning", "status": "completed", "summary": []interface{}{}, "encrypted_content": encrypted,
		})
	}
	for _, raw := range interfaceSlice(message["tool_calls"]) {
		call, _ := raw.(map[string]interface{})
		if item := responseFunctionCallItem(call); item != nil {
			out = append(out, item)
		}
	}
	text := strings.TrimSpace(fmt.Sprint(message["content"]))
	if text != "" && text != "<nil>" {
		part := map[string]interface{}{
			"type":        "output_text",
			"text":        text,
			"annotations": firstNonNil(message["annotations"], []interface{}{}),
		}
		out = append(out, map[string]interface{}{
			"id":      "msg_" + randomHex(12),
			"type":    "message",
			"status":  "completed",
			"role":    "assistant",
			"content": []interface{}{part},
		})
	}
	return out
}

func responseFunctionCallItem(call map[string]interface{}) map[string]interface{} {
	if call == nil {
		return nil
	}
	fn, _ := call["function"].(map[string]interface{})
	name := parseLooseStringAny(fn["name"])
	if name == "" {
		return nil
	}
	args := parseLooseStringAny(fn["arguments"])
	if args == "" {
		args = "{}"
	}
	return map[string]interface{}{
		"id":        "fc_" + randomHex(12),
		"type":      "function_call",
		"call_id":   firstNonEmpty(strings.TrimSpace(fmt.Sprint(call["id"])), "call_"+randomHex(12)),
		"name":      name,
		"arguments": args,
		"status":    "completed",
	}
}

func responsesUsageFromChat(raw interface{}) map[string]interface{} {
	usage, _ := raw.(map[string]interface{})
	if usage == nil {
		return map[string]interface{}{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}
	}
	input := interfaceToInt(firstNonNil(usage["prompt_tokens"], usage["input_tokens"]))
	output := interfaceToInt(firstNonNil(usage["completion_tokens"], usage["output_tokens"]))
	total := interfaceToInt(usage["total_tokens"])
	if total == 0 {
		total = input + output
	}
	return map[string]interface{}{
		"input_tokens":  input,
		"output_tokens": output,
		"total_tokens":  total,
	}
}

func copyCapturedResponse(w http.ResponseWriter, rec *captureResponseWriter) {
	for k, values := range rec.Header() {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	code := rec.code
	if code == 0 {
		code = http.StatusOK
	}
	w.WriteHeader(code)
	_, _ = w.Write(rec.body.Bytes())
}

func writeResponsesStreamFromChat(w http.ResponseWriter, model, raw string) {
	writeResponsesStreamFromChatReader(w, model, strings.NewReader(raw))
}

type responseStreamToolState struct {
	itemID      string
	callID      string
	name        string
	outputIndex int
	arguments   strings.Builder
}

// writeResponsesStreamFromChatReader translates Chat SSE incrementally. It is
// deliberately reader-based so the first Responses event is emitted as soon
// as the upstream Chat event arrives instead of after the completion ends.
func writeResponsesStreamFromChatReader(w http.ResponseWriter, model string, reader io.Reader) {
	writeResponsesStreamFromChatReaderRequest(w, ResponsesCreateRequest{Model: model}, reader)
}

func writeResponsesStreamFromChatReaderRequest(w http.ResponseWriter, request ResponsesCreateRequest, reader io.Reader) {
	model := request.Model
	streamResponseHeaders(w)
	id := "resp_" + randomHex(12)
	messageID := "msg_" + randomHex(12)
	reasoningID := "rs_" + randomHex(12)
	startedMessage := false
	startedReasoning := false
	reasoningIndex := -1
	messageIndex := -1
	var text strings.Builder
	var reasoning strings.Builder
	var encryptedReasoning string
	var output []interface{}
	var usage map[string]interface{}
	toolStates := make(map[int]*responseStreamToolState)
	toolOrder := make([]int, 0)

	writeSSEJSON := func(event string, payload map[string]interface{}) {
		raw, err := json.Marshal(payload)
		if err != nil {
			raw = []byte(`{}`)
		}
		writeSSEBytes(w, event, raw)
	}
	createdResponse := map[string]interface{}{
		"id": id, "object": "response", "created_at": time.Now().Unix(), "status": "in_progress", "model": model, "output": []interface{}{},
	}
	if len(request.Metadata) > 0 {
		createdResponse["metadata"] = request.Metadata
	}
	writeSSEJSON("response.created", map[string]interface{}{
		"type":     "response.created",
		"response": createdResponse,
	})
	closeReasoning := func() {
		if !startedReasoning {
			return
		}
		value := reasoning.String()
		writeSSEJSON("response.reasoning_summary_text.done", map[string]interface{}{
			"type": "response.reasoning_summary_text.done", "item_id": reasoningID, "output_index": reasoningIndex, "summary_index": 0, "text": value,
		})
		part := map[string]interface{}{"type": "summary_text", "text": value}
		writeSSEJSON("response.reasoning_summary_part.done", map[string]interface{}{
			"type": "response.reasoning_summary_part.done", "item_id": reasoningID, "output_index": reasoningIndex, "summary_index": 0, "part": part,
		})
		item := map[string]interface{}{"id": reasoningID, "type": "reasoning", "status": "completed", "summary": []interface{}{part}}
		if encryptedReasoning != "" {
			item["encrypted_content"] = encryptedReasoning
		}
		output[reasoningIndex] = item
		writeSSEJSON("response.output_item.done", map[string]interface{}{
			"type": "response.output_item.done", "output_index": reasoningIndex, "item": item,
		})
		startedReasoning = false
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		line := scanner.Text()
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
		if u := responsesUsageFromChat(chunk["usage"]); interfaceToInt(u["total_tokens"]) > 0 {
			usage = u
		}
		choices := interfaceSlice(chunk["choices"])
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]interface{})
		delta, _ := choice["delta"].(map[string]interface{})
		if encrypted := strings.TrimSpace(fmt.Sprint(delta["reasoning_encrypted_content"])); encrypted != "" && encrypted != "<nil>" {
			if !startedReasoning {
				startedReasoning = true
				reasoningIndex = len(output)
				output = append(output, nil)
				writeSSEJSON("response.output_item.added", map[string]interface{}{
					"type": "response.output_item.added", "output_index": reasoningIndex,
					"item": map[string]interface{}{"id": reasoningID, "type": "reasoning", "status": "in_progress", "summary": []interface{}{}},
				})
				writeSSEJSON("response.reasoning_summary_part.added", map[string]interface{}{
					"type": "response.reasoning_summary_part.added", "item_id": reasoningID, "output_index": reasoningIndex, "summary_index": 0,
					"part": map[string]interface{}{"type": "summary_text", "text": ""},
				})
			}
			encryptedReasoning = encrypted
		}
		if value := strings.TrimSpace(fmt.Sprint(firstNonNil(delta["reasoning_content"], delta["reasoning"]))); value != "" && value != "<nil>" {
			if !startedReasoning {
				startedReasoning = true
				reasoningIndex = len(output)
				output = append(output, nil)
				writeSSEJSON("response.output_item.added", map[string]interface{}{
					"type": "response.output_item.added", "output_index": reasoningIndex,
					"item": map[string]interface{}{"id": reasoningID, "type": "reasoning", "status": "in_progress", "summary": []interface{}{}},
				})
				writeSSEJSON("response.reasoning_summary_part.added", map[string]interface{}{
					"type": "response.reasoning_summary_part.added", "item_id": reasoningID, "output_index": reasoningIndex, "summary_index": 0,
					"part": map[string]interface{}{"type": "summary_text", "text": ""},
				})
			}
			reasoning.WriteString(value)
			writeSSEJSON("response.reasoning_summary_text.delta", map[string]interface{}{
				"type": "response.reasoning_summary_text.delta", "item_id": reasoningID, "output_index": reasoningIndex, "summary_index": 0, "delta": value,
			})
		}
		if content, ok := delta["content"].(string); ok && content != "" {
			closeReasoning()
			if !startedMessage {
				startedMessage = true
				messageIndex = len(output)
				output = append(output, nil)
				writeSSEJSON("response.output_item.added", map[string]interface{}{
					"type": "response.output_item.added", "output_index": messageIndex,
					"item": map[string]interface{}{"id": messageID, "type": "message", "role": "assistant", "content": []interface{}{}, "status": "in_progress"},
				})
				writeSSEJSON("response.content_part.added", map[string]interface{}{
					"type": "response.content_part.added", "item_id": messageID, "output_index": messageIndex, "content_index": 0,
					"part": map[string]interface{}{"type": "output_text", "text": "", "annotations": []interface{}{}},
				})
			}
			text.WriteString(content)
			writeSSEJSON("response.output_text.delta", map[string]interface{}{
				"type": "response.output_text.delta", "item_id": messageID, "output_index": messageIndex, "content_index": 0, "delta": content,
			})
		}
		for _, rawCall := range interfaceSlice(delta["tool_calls"]) {
			closeReasoning()
			call, _ := rawCall.(map[string]interface{})
			if call == nil {
				continue
			}
			callIndex := interfaceToInt(call["index"])
			fn, _ := call["function"].(map[string]interface{})
			state := toolStates[callIndex]
			if state == nil {
				callID := firstNonEmpty(parseLooseStringAny(call["id"]), "call_"+randomHex(12))
				state = &responseStreamToolState{
					itemID: "fc_" + randomHex(12), callID: callID,
					name: firstNonEmpty(parseLooseStringAny(fn["name"]), "tool"), outputIndex: len(output),
				}
				toolStates[callIndex] = state
				toolOrder = append(toolOrder, callIndex)
				output = append(output, nil)
				writeSSEJSON("response.output_item.added", map[string]interface{}{
					"type": "response.output_item.added", "output_index": state.outputIndex,
					"item": map[string]interface{}{"id": state.itemID, "type": "function_call", "call_id": state.callID, "name": state.name, "arguments": "", "status": "in_progress"},
				})
			}
			if id := parseLooseStringAny(call["id"]); id != "" {
				state.callID = id
			}
			if name := parseLooseStringAny(fn["name"]); name != "" {
				state.name = name
			}
			if fragment, ok := fn["arguments"].(string); ok && fragment != "" {
				state.arguments.WriteString(fragment)
				writeSSEJSON("response.function_call_arguments.delta", map[string]interface{}{
					"type": "response.function_call_arguments.delta", "item_id": state.itemID, "output_index": state.outputIndex, "delta": fragment,
				})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		writeSSEJSON("response.failed", map[string]interface{}{
			"type":     "response.failed",
			"response": map[string]interface{}{"id": id, "object": "response", "status": "failed", "model": model, "error": map[string]interface{}{"code": "stream_read_error", "message": "chat stream read error"}},
		})
		writeSSEBytes(w, "", []byte("[DONE]"))
		return
	}
	closeReasoning()
	for _, callIndex := range toolOrder {
		state := toolStates[callIndex]
		if state == nil || strings.TrimSpace(state.name) == "" {
			continue
		}
		arguments := state.arguments.String()
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		item := map[string]interface{}{
			"id": state.itemID, "type": "function_call", "call_id": state.callID,
			"name": state.name, "arguments": arguments, "status": "completed",
		}
		output[state.outputIndex] = item
		writeSSEJSON("response.function_call_arguments.done", map[string]interface{}{
			"type": "response.function_call_arguments.done", "item_id": state.itemID, "output_index": state.outputIndex, "arguments": arguments,
		})
		writeSSEJSON("response.output_item.done", map[string]interface{}{
			"type": "response.output_item.done", "output_index": state.outputIndex, "item": item,
		})
	}
	if startedMessage {
		fullText := text.String()
		part := map[string]interface{}{"type": "output_text", "text": fullText, "annotations": []interface{}{}}
		msg := map[string]interface{}{"id": messageID, "type": "message", "status": "completed", "role": "assistant", "content": []interface{}{part}}
		output[messageIndex] = msg
		writeSSEJSON("response.output_text.done", map[string]interface{}{
			"type": "response.output_text.done", "item_id": messageID, "output_index": messageIndex, "content_index": 0, "text": fullText,
		})
		writeSSEJSON("response.content_part.done", map[string]interface{}{
			"type": "response.content_part.done", "item_id": messageID, "output_index": messageIndex, "content_index": 0, "part": part,
		})
		writeSSEJSON("response.output_item.done", map[string]interface{}{
			"type": "response.output_item.done", "output_index": messageIndex, "item": msg,
		})
	}
	if usage == nil {
		usage = map[string]interface{}{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}
	}
	completedResponse := map[string]interface{}{
		"id": id, "object": "response", "created_at": time.Now().Unix(), "status": "completed", "model": model, "output": output, "usage": usage,
	}
	if len(request.Metadata) > 0 {
		completedResponse["metadata"] = request.Metadata
	}
	if strings.TrimSpace(request.Truncation) != "" {
		completedResponse["truncation"] = request.Truncation
	}
	writeSSEJSON("response.completed", map[string]interface{}{
		"type":     "response.completed",
		"response": completedResponse,
	})
	writeSSEBytes(w, "", []byte("[DONE]"))
}
