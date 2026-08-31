package grok

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/debug"
)

func (h *Handler) consoleURL(path string) string {
	base := "https://console.x.ai/v1"
	if h != nil && h.cfg != nil {
		base = h.cfg.GrokConsoleBaseURLOrDefault()
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

type consoleContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type consoleMessageItem struct {
	Role    string                `json:"role"`
	Content []consoleContentBlock `json:"content"`
}

func chatMessageContentText(content interface{}) string {
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	case []interface{}:
		var b strings.Builder
		for _, part := range v {
			m, ok := part.(map[string]interface{})
			if !ok {
				continue
			}
			t := strings.ToLower(strings.TrimSpace(fmt.Sprint(m["type"])))
			switch t {
			case "text", "input_text":
				if s := strings.TrimSpace(fmt.Sprint(m["text"])); s != "" {
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
					b.WriteString(s)
				}
			}
		}
		return b.String()
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func (c *Client) consoleHeaders(token string) http.Header {
	h := c.headers(token)
	h.Set("Origin", "https://console.x.ai")
	h.Set("Referer", "https://console.x.ai/")
	h.Set("Accept", "*/*")
	return h
}

func (h *Handler) consolePayload(spec ModelSpec, req *ChatCompletionsRequest) (map[string]interface{}, error) {
	return h.responsesPayloadFromChat(spec, req, false)
}

func consoleInputHasEncryptedReasoning(input []interface{}) bool {
	for _, raw := range input {
		item, _ := raw.(map[string]interface{})
		if item == nil || !strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["type"])), "reasoning") {
			continue
		}
		if value := strings.TrimSpace(fmt.Sprint(item["encrypted_content"])); value != "" && value != "<nil>" {
			return true
		}
	}
	return false
}

func insertConsoleReplayBeforeLastUser(input []interface{}, replay map[string]interface{}) []interface{} {
	insertAt := len(input)
	for index := len(input) - 1; index >= 0; index-- {
		switch item := input[index].(type) {
		case consoleMessageItem:
			if strings.EqualFold(strings.TrimSpace(item.Role), "user") {
				insertAt = index
				index = -1
			}
		case map[string]interface{}:
			if strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["role"])), "user") {
				insertAt = index
				index = -1
			}
		}
	}
	out := make([]interface{}, 0, len(input)+1)
	out = append(out, input[:insertAt]...)
	out = append(out, replay)
	out = append(out, input[insertAt:]...)
	return out
}

func consoleToolsFromOpenAI(tools []ToolDef) []map[string]interface{} {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		if !strings.EqualFold(strings.TrimSpace(tool.Type), "function") {
			continue
		}
		name := strings.TrimSpace(fmt.Sprint(tool.Function["name"]))
		if name == "" {
			continue
		}
		switch consoleBuiltinToolName(name) {
		case "web_search":
			out = append(out, map[string]interface{}{"type": "web_search"})
			continue
		case "x_search":
			out = append(out, map[string]interface{}{"type": "x_search"})
			continue
		}
		item := map[string]interface{}{
			"type":        "function",
			"name":        name,
			"description": strings.TrimSpace(fmt.Sprint(tool.Function["description"])),
			"parameters":  map[string]interface{}{},
		}
		if params, ok := tool.Function["parameters"]; ok && params != nil {
			item["parameters"] = params
		}
		if strict, ok := tool.Function["strict"].(bool); ok {
			item["strict"] = strict
		}
		out = append(out, item)
	}
	return out
}

func consoleToolChoiceFromOpenAI(choice interface{}) interface{} {
	switch v := choice.(type) {
	case nil:
		return nil
	case string:
		c := strings.ToLower(strings.TrimSpace(v))
		if c == "" {
			return nil
		}
		return c
	case map[string]interface{}:
		if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(v["type"])), "function") {
			return v
		}
		fn, _ := v["function"].(map[string]interface{})
		name := strings.TrimSpace(fmt.Sprint(fn["name"]))
		if name == "" {
			return v
		}
		return map[string]interface{}{
			"type": "function",
			"name": name,
		}
	default:
		return v
	}
}

func injectConsoleSearchTools(tools []map[string]interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools)+2)
	hasWebSearch := false
	hasXSearch := false
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		copied := make(map[string]interface{}, len(tool))
		for k, v := range tool {
			copied[k] = v
		}
		switch consoleToolName(copied) {
		case "web_search":
			hasWebSearch = true
		case "x_search":
			hasXSearch = true
		}
		out = append(out, copied)
	}
	if !hasWebSearch {
		out = append(out, map[string]interface{}{"type": "web_search"})
	}
	if !hasXSearch {
		out = append(out, map[string]interface{}{"type": "x_search"})
	}
	return out
}

func consoleToolName(tool map[string]interface{}) string {
	if tool == nil {
		return ""
	}
	toolType := strings.ToLower(strings.TrimSpace(fmt.Sprint(tool["type"])))
	if toolType == "function" {
		return consoleBuiltinToolName(fmt.Sprint(tool["name"]))
	}
	return consoleBuiltinToolName(toolType)
}

func consoleBuiltinToolName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "web_search":
		return "web_search"
	case "x_search":
		return "x_search"
	default:
		return ""
	}
}

func (h *Handler) doConsole(ctx context.Context, token string, payload map[string]interface{}) (*http.Response, error) {
	if err := consoleRateLimitEndpoint(ctx); err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	resp, err := h.client.doConsoleDPoPRequest(ctx, token, http.MethodPost, h.consoleURL("responses"), body)
	if err != nil {
		noteConsoleRateLimitError(err)
		return nil, err
	}
	return resp, nil
}

func shouldServeConsoleChat(spec ModelSpec, attachments []AttachmentInput) bool {
	return strings.TrimSpace(spec.ConsoleModel) != "" && len(attachments) == 0
}

func requiresConsoleResponses(spec ModelSpec) bool {
	return strings.TrimSpace(spec.ConsoleModel) != ""
}

func consoleExtractText(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case map[string]interface{}:
		if t := strings.TrimSpace(fmt.Sprint(x["type"])); t == "output_text" || t == "text" || t == "message" {
			if raw := x["text"]; raw != nil {
				if s := strings.TrimSpace(fmt.Sprint(raw)); s != "" && s != "<nil>" {
					return s
				}
			}
			if raw := x["content"]; raw != nil {
				if s := strings.TrimSpace(consoleExtractText(raw)); s != "" {
					return s
				}
			}
			if raw := x["summary"]; raw != nil {
				if s := strings.TrimSpace(consoleExtractText(raw)); s != "" {
					return s
				}
			}
		}
		if t := strings.TrimSpace(fmt.Sprint(x["type"])); t == "message" || t == "response.output_message" {
			if raw := x["content"]; raw != nil {
				if s := strings.TrimSpace(consoleExtractText(raw)); s != "" {
					return s
				}
			}
		}
		for _, key := range []string{"output_text", "content", "output", "text", "message"} {
			if raw := x[key]; raw != nil {
				if s := consoleExtractText(raw); strings.TrimSpace(s) != "" {
					return s
				}
			}
		}
	case []interface{}:
		var b strings.Builder
		for _, item := range x {
			if s := strings.TrimSpace(consoleExtractText(item)); s != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(s)
			}
		}
		return b.String()
	}
	return ""
}

func consoleExtractMessageText(v interface{}) string {
	switch x := v.(type) {
	case map[string]interface{}:
		if output, ok := x["output"].([]interface{}); ok {
			for _, item := range output {
				m, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				t := strings.ToLower(strings.TrimSpace(fmt.Sprint(m["type"])))
				if t != "message" && t != "response.output_message" {
					continue
				}
				if s := strings.TrimSpace(consoleExtractText(m["content"])); s != "" {
					return s
				}
			}
		}
	}
	return strings.TrimSpace(consoleExtractText(v))
}

func consoleFlatAnnotations(v interface{}) []map[string]interface{} {
	seen := map[string]struct{}{}
	out := make([]map[string]interface{}, 0)
	add := func(url, title string, start, end int) {
		url = strings.TrimSpace(url)
		if url == "" {
			return
		}
		key := url + "\x00" + title
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, map[string]interface{}{
			"url":         url,
			"title":       strings.TrimSpace(title),
			"start_index": start,
			"end_index":   end,
		})
	}
	var walk func(interface{})
	walk = func(raw interface{}) {
		switch x := raw.(type) {
		case map[string]interface{}:
			t := strings.ToLower(strings.TrimSpace(fmt.Sprint(x["type"])))
			if t == "url_citation" || (x["url"] != nil && (x["title"] != nil || x["start_index"] != nil || x["end_index"] != nil)) {
				add(fmt.Sprint(x["url"]), fmt.Sprint(x["title"]), interfaceToInt(x["start_index"]), interfaceToInt(x["end_index"]))
			}
			if t == "web_search_call" {
				if action, _ := x["action"].(map[string]interface{}); action != nil {
					for _, src := range interfaceSlice(action["sources"]) {
						if m, _ := src.(map[string]interface{}); m != nil {
							add(fmt.Sprint(m["url"]), fmt.Sprint(m["title"]), 0, 0)
						}
					}
					if strings.EqualFold(strings.TrimSpace(fmt.Sprint(action["type"])), "open_page") {
						add(fmt.Sprint(action["url"]), "", 0, 0)
					}
				}
			}
			for _, key := range []string{"annotation", "annotations", "content", "output", "item"} {
				if child, ok := x[key]; ok {
					walk(child)
				}
			}
		case []interface{}:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(v)
	return out
}

func consoleChatAnnotations(flat []map[string]interface{}) []interface{} {
	if len(flat) == 0 {
		return []interface{}{}
	}
	out := make([]interface{}, 0, len(flat))
	for _, ann := range flat {
		out = append(out, map[string]interface{}{
			"type": "url_citation",
			"url_citation": map[string]interface{}{
				"url":         ann["url"],
				"title":       ann["title"],
				"start_index": ann["start_index"],
				"end_index":   ann["end_index"],
			},
		})
	}
	return out
}

func appendUniqueConsoleAnnotations(dst []map[string]interface{}, src []map[string]interface{}) []map[string]interface{} {
	if len(src) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst)+len(src))
	for _, ann := range dst {
		seen[fmt.Sprint(ann["url"])+"\x00"+fmt.Sprint(ann["title"])] = struct{}{}
	}
	for _, ann := range src {
		key := fmt.Sprint(ann["url"]) + "\x00" + fmt.Sprint(ann["title"])
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, ann)
	}
	return dst
}

func consoleUsage(v map[string]interface{}) map[string]interface{} {
	raw, ok := v["usage"].(map[string]interface{})
	if !ok {
		return nil
	}
	prompt := interfaceToInt(raw["input_tokens"])
	completion := interfaceToInt(raw["output_tokens"])
	if prompt == 0 {
		prompt = interfaceToInt(raw["prompt_tokens"])
	}
	if completion == 0 {
		completion = interfaceToInt(raw["completion_tokens"])
	}
	total := interfaceToInt(raw["total_tokens"])
	if total == 0 {
		total = prompt + completion
	}
	reasoning := 0
	if details, _ := raw["output_tokens_details"].(map[string]interface{}); details != nil {
		reasoning = interfaceToInt(details["reasoning_tokens"])
	}
	if reasoning == 0 {
		reasoning = interfaceToInt(raw["reasoning_tokens"])
	}
	return map[string]interface{}{
		"prompt_tokens":     prompt,
		"completion_tokens": completion,
		"total_tokens":      total,
		"prompt_tokens_details": map[string]interface{}{
			"cached_tokens": 0,
			"text_tokens":   prompt,
			"audio_tokens":  0,
			"image_tokens":  0,
		},
		"completion_tokens_details": map[string]interface{}{
			"text_tokens":      max(completion-reasoning, 0),
			"audio_tokens":     0,
			"reasoning_tokens": reasoning,
		},
	}
}

// finishUpstreamChat completes a chat response after an upstream call: error
// reporting, quota sync, then streaming or collection. Shared by the console
// and CLI chat paths. url is used for both error and request logging; headers
// is evaluated lazily so it is only built on the success path.
func (h *Handler) finishUpstreamChat(ctx context.Context, w http.ResponseWriter, req *ChatCompletionsRequest, sess *chatAccountSession, logger *debug.Logger, name, url string, headers func() http.Header, payload map[string]interface{}, resp *http.Response, err error) {
	if err != nil {
		h.auditRequest(ctx, sess.acc, ProviderForAccount(sess.acc), req.Model, fmt.Sprint(upstreamHTTPResponseStatus(err)), nil)
		slog.Error(name+" chat upstream failed", "url", url, "status", parseUpstreamStatus(err), "error", err)
		if logger != nil {
			logger.LogUpstreamHTTPError(url, parseUpstreamStatus(err), "", err)
		}
		if markAllGrokAccountStatuses(err) {
			h.markAccountStatus(ctx, sess.acc, err)
		}
		http.Error(w, err.Error(), upstreamHTTPResponseStatus(err))
		return
	}
	defer resp.Body.Close()
	h.auditRequest(ctx, sess.acc, ProviderForAccount(sess.acc), req.Model, fmt.Sprint(resp.StatusCode), nil)
	if logger != nil {
		logger.LogUpstreamRequest(url, debugHeaderMap(headers()), payload)
	}
	h.syncGrokQuota(sess.acc, resp.Header)
	if req.Stream {
		h.streamConsoleChat(w, req, resp.Body)
		return
	}
	h.collectConsoleChat(w, req, resp.Body)
}

func (h *Handler) serveConsoleChat(ctx context.Context, w http.ResponseWriter, req *ChatCompletionsRequest, spec ModelSpec, sess *chatAccountSession, logger *debug.Logger) {
	payload, err := h.consolePayload(spec, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := h.doConsoleWithAutoSwitch(ctx, sess, payload, req.Model)
	if err == nil && responseRequiresThinking(spec, req) {
		resp, err = h.retryMissingThinking(ctx, sess, resp, ProviderConsole,
			func(exclude []int64) (*chatAccountSession, error) {
				return h.openConsoleAccountSession(ctx, exclude, req.Model)
			},
			func() (*http.Response, error) { return h.doConsole(ctx, sess.token, payload) })
	}
	h.finishUpstreamChat(ctx, w, req, sess, logger, "console", h.consoleURL("responses"),
		func() http.Header { return h.client.consoleHeaders(sess.token) }, payload, resp, err)
}

// retryWithAccountSwitch runs a request in a time-budgeted loop, switching to
// the next account whenever shouldSwitchGrokAccount fires. doRequest issues the
// request against the current session; openNext returns its replacement.
// onSwitch runs after each successful account swap (e.g. to rebuild the request
// payload for the new account).
func (h *Handler) retryWithAccountSwitch(ctx context.Context, sess *chatAccountSession, switchPace time.Duration, doRequest func() (*http.Response, error), openNext func(used []int64) (*chatAccountSession, error), onSwitch func() error) (*http.Response, error) {
	switchDeadline := time.Now().Add(10 * time.Second)
	if h != nil && h.cfg != nil && h.cfg.AccountSwitchCount > 0 {
		switchDeadline = time.Now().Add(time.Duration(h.cfg.AccountSwitchCount) * time.Second)
	}

	used := make([]int64, 0)
	attempt := 0
	for {
		attempt++
		if sess.acc != nil && sess.acc.ID != 0 {
			used = append(used, sess.acc.ID)
		}
		started := time.Now()
		resp, err := doRequest()
		provider := "web"
		if sess.acc != nil {
			provider = ProviderForAccount(sess.acc)
		}
		h.auditAttempt(ctx, sess.acc, provider, attempt, started, err)
		if err == nil {
			return resp, nil
		}
		if markAllGrokAccountStatuses(err) {
			h.markAccountStatus(ctx, sess.acc, err)
		}
		if !shouldSwitchGrokAccount(err) || time.Now().After(switchDeadline) {
			return nil, err
		}

		sess.Close()
		if !sleepWithContext(ctx, switchPace) {
			return nil, ctx.Err()
		}
		next, switchErr := openNext(used)
		if switchErr != nil {
			return nil, err
		}
		sess.acc = next.acc
		sess.token = next.token
		sess.poolCandidates = next.poolCandidates
		sess.release = next.release
		if onSwitch != nil {
			if err := onSwitch(); err != nil {
				return nil, err
			}
		}
	}
}

func (h *Handler) doConsoleWithAutoSwitch(ctx context.Context, sess *chatAccountSession, payload map[string]interface{}, modelIDs ...string) (*http.Response, error) {
	if sess == nil || strings.TrimSpace(sess.token) == "" {
		return nil, fmt.Errorf("empty chat session")
	}
	return h.retryWithAccountSwitch(ctx, sess, 1500*time.Millisecond,
		func() (*http.Response, error) { return h.doConsole(ctx, sess.token, payload) },
		func(used []int64) (*chatAccountSession, error) {
			return h.openConsoleAccountSession(ctx, used, modelIDs...)
		}, nil)
}

func (h *Handler) collectConsoleChat(w http.ResponseWriter, req *ChatCompletionsRequest, body io.Reader) {
	var raw map[string]interface{}
	if err := json.NewDecoder(body).Decode(&raw); err != nil {
		http.Error(w, "console response parse error: "+err.Error(), http.StatusBadGateway)
		return
	}
	text := consoleExtractMessageText(raw)
	reasoning := consoleExtractReasoningText(raw)
	encryptedReasoning := consoleExtractEncryptedReasoning(raw)
	annotations := consoleChatAnnotations(consoleFlatAnnotations(raw))
	toolCalls := consoleToolCallsFromOutput(raw)
	message := map[string]interface{}{
		"role":        "assistant",
		"content":     text,
		"refusal":     nil,
		"annotations": annotations,
	}
	if strings.TrimSpace(reasoning) != "" {
		message["reasoning_content"] = reasoning
	}
	if encryptedReasoning != "" {
		message["reasoning_encrypted_content"] = encryptedReasoning
		if req.ReasoningReplay {
			h.storeReasoningReplay(req.Model, req.PromptCacheKey, encryptedReasoning)
		}
	}
	finishReason := "stop"
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
		finishReason = "tool_calls"
		if strings.TrimSpace(text) == "" {
			message["content"] = nil
		}
	}
	resp := map[string]interface{}{
		"id":                 firstNonEmpty(fmt.Sprint(raw["id"]), "chatcmpl_"+randomHex(8)),
		"object":             "chat.completion",
		"created":            time.Now().Unix(),
		"model":              req.Model,
		"service_tier":       nil,
		"system_fingerprint": "",
		"choices": []map[string]interface{}{{
			"index":         0,
			"message":       message,
			"finish_reason": finishReason,
		}},
		"usage": firstUsage(consoleUsage(raw), addReasoningUsage(buildChatUsagePayload(req, text, toolCalls), reasoning)),
	}
	writeJSON(w, resp)
}

func consoleExtractReasoningText(raw map[string]interface{}) string {
	if raw == nil {
		return ""
	}
	var rawText strings.Builder
	var summaryText strings.Builder
	for _, value := range interfaceSlice(raw["output"]) {
		item, _ := value.(map[string]interface{})
		if item == nil || !strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["type"])), "reasoning") {
			continue
		}
		for _, value := range interfaceSlice(item["content"]) {
			part, _ := value.(map[string]interface{})
			if part == nil || !strings.EqualFold(strings.TrimSpace(fmt.Sprint(part["type"])), "reasoning_text") {
				continue
			}
			if text := fmt.Sprint(part["text"]); strings.TrimSpace(text) != "" && text != "<nil>" {
				rawText.WriteString(text)
			}
		}
		for _, value := range interfaceSlice(item["summary"]) {
			part, _ := value.(map[string]interface{})
			if part == nil {
				continue
			}
			if text := fmt.Sprint(part["text"]); strings.TrimSpace(text) != "" && text != "<nil>" {
				summaryText.WriteString(text)
			}
		}
	}
	if rawText.Len() > 0 {
		return rawText.String()
	}
	return summaryText.String()
}

func consoleExtractEncryptedReasoning(raw map[string]interface{}) string {
	if raw == nil {
		return ""
	}
	for _, value := range interfaceSlice(raw["output"]) {
		item, _ := value.(map[string]interface{})
		if item == nil || !strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["type"])), "reasoning") {
			continue
		}
		if encrypted := strings.TrimSpace(fmt.Sprint(item["encrypted_content"])); encrypted != "" && encrypted != "<nil>" {
			return encrypted
		}
	}
	return ""
}

func consoleToolCallsFromOutput(raw map[string]interface{}) []map[string]interface{} {
	if raw == nil {
		return nil
	}
	var out []map[string]interface{}
	for _, item := range interfaceSlice(raw["output"]) {
		if tc := consoleToolCallFromItem(item); tc != nil {
			out = append(out, tc)
		}
	}
	return out
}

func consoleToolCallFromItem(raw interface{}) map[string]interface{} {
	item, _ := raw.(map[string]interface{})
	if item == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["type"])), "function_call") {
		return nil
	}
	name := strings.TrimSpace(fmt.Sprint(item["name"]))
	if name == "" || name == "<nil>" {
		return nil
	}
	callID := strings.TrimSpace(fmt.Sprint(item["call_id"]))
	if callID == "" || callID == "<nil>" {
		callID = strings.TrimSpace(fmt.Sprint(item["id"]))
	}
	if callID == "" || callID == "<nil>" {
		callID = "call_" + randomHex(12)
	}
	arguments := "{}"
	if rawArgs, ok := item["arguments"]; ok && rawArgs != nil {
		switch v := rawArgs.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				arguments = strings.TrimSpace(v)
			}
		default:
			if buf, err := json.Marshal(v); err == nil {
				arguments = string(buf)
			}
		}
	}
	return map[string]interface{}{
		"id":   callID,
		"type": "function",
		"function": map[string]interface{}{
			"name":      name,
			"arguments": arguments,
		},
	}
}

type consoleStreamToolCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

func (tc *consoleStreamToolCall) openAIToolCall(index int) map[string]interface{} {
	id := strings.TrimSpace(tc.ID)
	if id == "" {
		id = "call_" + randomHex(12)
	}
	args := strings.TrimSpace(tc.Arguments.String())
	if args == "" {
		args = "{}"
	}
	return map[string]interface{}{
		"index": index,
		"id":    id,
		"type":  "function",
		"function": map[string]interface{}{
			"name":      strings.TrimSpace(tc.Name),
			"arguments": args,
		},
	}
}

func firstUsage(a, b map[string]interface{}) map[string]interface{} {
	if len(a) > 0 {
		return a
	}
	return b
}

func consoleUsageFromStreamEvent(ev map[string]interface{}) map[string]interface{} {
	if ev == nil {
		return nil
	}
	if resp, _ := ev["response"].(map[string]interface{}); resp != nil {
		if usage := consoleUsage(resp); len(usage) > 0 {
			return usage
		}
	}
	return consoleUsage(ev)
}

func appendConsoleFinalChunk(dst []byte, id string, created int64, model, fingerprint, finish string, annotations []interface{}, usage map[string]interface{}) []byte {
	delta := map[string]interface{}{}
	if len(annotations) > 0 {
		delta["annotations"] = annotations
	}
	chunk := map[string]interface{}{
		"id":                 id,
		"object":             "chat.completion.chunk",
		"created":            created,
		"model":              model,
		"service_tier":       nil,
		"system_fingerprint": fingerprint,
		"choices": []map[string]interface{}{{
			"index":         0,
			"delta":         delta,
			"logprobs":      nil,
			"finish_reason": finish,
		}},
		"usage": usage,
	}
	raw, err := json.Marshal(chunk)
	if err != nil {
		return appendChatCompletionChunkWithUsage(dst, id, created, model, fingerprint, "", "", finish, true, usage)
	}
	return append(dst, raw...)
}

func (h *Handler) streamConsoleChat(w http.ResponseWriter, req *ChatCompletionsRequest, body io.Reader) {
	flusher := streamResponseHeaders(w)
	id := "chatcmpl_" + randomHex(8)
	fingerprint := ""
	raw := appendChatCompletionChunk(nil, id, time.Now().Unix(), req.Model, fingerprint, "assistant", "", "", false)
	writeSSE(w, flusher, "", raw)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	var event string
	var final strings.Builder
	var reasoning strings.Builder
	var reasoningSummary strings.Builder
	var encryptedReasoning string
	rawReasoningSeen := false
	var annotations []map[string]interface{}
	var finalUsage map[string]interface{}
	var toolCalls []*consoleStreamToolCall
	var activeToolCall *consoleStreamToolCall
	var contentLoopGuard, reasoningLoopGuard streamLoopGuard
	doomLoop := false
	emitReasoning := func(value string) {
		if value == "" {
			return
		}
		if reasoningLoopGuard.Add(value) {
			doomLoop = true
			writeSSEStreamError(w, flusher, nil, "upstream reasoning repetition loop detected")
			return
		}
		reasoning.WriteString(value)
		raw := appendChatCompletionReasoningChunk(nil, id, time.Now().Unix(), req.Model, fingerprint, value)
		writeSSE(w, flusher, "", raw)
	}
	flushReasoningSummary := func() {
		if rawReasoningSeen || reasoningSummary.Len() == 0 {
			return
		}
		emitReasoning(reasoningSummary.String())
		reasoningSummary.Reset()
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			writeSSEStreamError(w, flusher, nil, "console stream parse error: "+err.Error())
			return
		}
		annotations = appendUniqueConsoleAnnotations(annotations, consoleFlatAnnotations(ev))
		if usage := consoleUsageFromStreamEvent(ev); len(usage) > 0 {
			finalUsage = usage
		}
		eventLower := strings.ToLower(strings.TrimSpace(event))
		if eventLower == "" {
			eventLower = strings.ToLower(strings.TrimSpace(fmt.Sprint(ev["type"])))
		}
		if reasoningDelta := consoleReasoningDelta(eventLower, ev); reasoningDelta != "" {
			if strings.Contains(eventLower, "reasoning_text") && !strings.Contains(eventLower, "summary") {
				if !rawReasoningSeen {
					rawReasoningSeen = true
					reasoningSummary.Reset()
				}
				emitReasoning(reasoningDelta)
			} else {
				reasoningSummary.WriteString(reasoningDelta)
			}
			if doomLoop {
				return
			}
			continue
		}
		if item, _ := ev["item"].(map[string]interface{}); item != nil && strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["type"])), "function_call") {
			name := strings.TrimSpace(fmt.Sprint(item["name"]))
			if name != "" && name != "<nil>" {
				tc := &consoleStreamToolCall{
					ID:   strings.TrimSpace(fmt.Sprint(item["call_id"])),
					Name: name,
				}
				if tc.ID == "" || tc.ID == "<nil>" {
					tc.ID = strings.TrimSpace(fmt.Sprint(item["id"]))
				}
				if args, ok := item["arguments"]; ok && args != nil {
					switch v := args.(type) {
					case string:
						tc.Arguments.WriteString(v)
					default:
						if buf, err := json.Marshal(v); err == nil {
							tc.Arguments.Write(buf)
						}
					}
				}
				toolCalls = append(toolCalls, tc)
				activeToolCall = tc
			}
			continue
		}
		if item, _ := ev["item"].(map[string]interface{}); item != nil && strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["type"])), "reasoning") {
			if encrypted := strings.TrimSpace(fmt.Sprint(item["encrypted_content"])); encrypted != "" && encrypted != "<nil>" {
				encryptedReasoning = encrypted
				chunk := map[string]interface{}{
					"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": req.Model,
					"choices": []map[string]interface{}{{"index": 0, "delta": map[string]interface{}{"reasoning_encrypted_content": encrypted}, "finish_reason": nil}},
				}
				if encoded, err := json.Marshal(chunk); err == nil {
					writeSSE(w, flusher, "", encoded)
				}
			}
		}
		if strings.Contains(eventLower, "function_call_arguments") {
			if activeToolCall == nil && len(toolCalls) > 0 {
				activeToolCall = toolCalls[len(toolCalls)-1]
			}
			if activeToolCall != nil {
				if strings.Contains(eventLower, ".delta") {
					if delta := strings.TrimSpace(fmt.Sprint(ev["delta"])); delta != "" && delta != "<nil>" {
						activeToolCall.Arguments.WriteString(delta)
					}
				}
				if strings.Contains(eventLower, ".done") {
					if args := strings.TrimSpace(fmt.Sprint(ev["arguments"])); args != "" && args != "<nil>" {
						activeToolCall.Arguments.Reset()
						activeToolCall.Arguments.WriteString(args)
					}
				}
			}
			continue
		}
		if strings.Contains(eventLower, "output_text") {
			flushReasoningSummary()
		}
		content := consoleDeltaText(eventLower, ev)
		if content == "" {
			continue
		}
		if contentLoopGuard.Add(content) {
			writeSSEStreamError(w, flusher, nil, "upstream content repetition loop detected")
			return
		}
		final.WriteString(content)
		raw = appendChatCompletionChunk(nil, id, time.Now().Unix(), req.Model, fingerprint, "", content, "", false)
		writeSSE(w, flusher, "", raw)
	}
	if err := scanner.Err(); err != nil {
		writeSSEStreamError(w, flusher, nil, "console stream read error: "+err.Error())
		return
	}
	flushReasoningSummary()
	if doomLoop {
		return
	}
	if req.ReasoningReplay && encryptedReasoning != "" {
		h.storeReasoningReplay(req.Model, req.PromptCacheKey, encryptedReasoning)
	}
	indexedToolCalls := make([]map[string]interface{}, 0, len(toolCalls))
	for _, tc := range toolCalls {
		if tc == nil || strings.TrimSpace(tc.Name) == "" {
			continue
		}
		indexedToolCalls = append(indexedToolCalls, tc.openAIToolCall(len(indexedToolCalls)))
	}
	usage := finalUsage
	if len(usage) == 0 {
		usage = addReasoningUsage(buildChatUsagePayload(req, final.String(), indexedToolCalls), reasoning.String())
	}
	if len(indexedToolCalls) > 0 {
		raw = appendChatCompletionToolCallsChunkWithUsage(nil, id, time.Now().Unix(), req.Model, fingerprint, indexedToolCalls, "tool_calls", true, usage)
		writeSSE(w, flusher, "", raw)
		writeSSE(w, flusher, "", []byte("[DONE]"))
		return
	}
	raw = appendConsoleFinalChunk(nil, id, time.Now().Unix(), req.Model, fingerprint, "stop", consoleChatAnnotations(annotations), usage)
	writeSSE(w, flusher, "", raw)
	writeSSE(w, flusher, "", []byte("[DONE]"))
}

func consoleDeltaText(event string, ev map[string]interface{}) string {
	event = strings.ToLower(strings.TrimSpace(event))
	if !strings.Contains(event, "delta") || strings.Contains(event, "reasoning") {
		return ""
	}
	for _, key := range []string{"delta", "text"} {
		raw, ok := ev[key]
		if !ok || raw == nil {
			continue
		}
		s, ok := raw.(string)
		if !ok {
			s = fmt.Sprint(raw)
		}
		if s != "" && s != "<nil>" {
			return s
		}
	}
	if strings.Contains(event, "output_text") {
		return consoleExtractText(ev)
	}
	return ""
}

func consoleReasoningDelta(event string, ev map[string]interface{}) string {
	event = strings.ToLower(strings.TrimSpace(event))
	if !strings.Contains(event, "reasoning") || !strings.Contains(event, "delta") {
		return ""
	}
	for _, key := range []string{"delta", "text"} {
		if value, ok := ev[key]; ok && value != nil {
			if text, ok := value.(string); ok && text != "" {
				return text
			}
		}
	}
	return ""
}
