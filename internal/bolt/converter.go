package bolt

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/goccy/go-json"

	"orchids-api/internal/debug"
	"orchids-api/internal/upstream"
)

var boltStructuredNestedKeys = []string{"parts", "content", "delta", "data", "messages"}
var boltStructuredStringKeys = []string{"text", "message", "content", "delta", "output", "response"}
var boltToolCallCollectionKeys = []string{"tool_calls", "toolCalls", "calls", "tool_call", "toolCall"}

type outboundConverter struct {
	model                   string
	inCodeBlock             bool
	codeBuffer              strings.Builder
	codeFenceHeader         strings.Builder
	awaitingCodeFenceHeader bool
	inputTokens             int
	outputTokens            int
	emittedToolUse          bool
	seenToolCalls           map[string]struct{}
	suppressText            bool
	finalText               strings.Builder
	finishReason            string
	firstToolName           string
	firstToolPath           string
	firstToolInput          string
}

func newOutboundConverter(model string, inputTokens int) *outboundConverter {
	return &outboundConverter{
		model:         model,
		inputTokens:   inputTokens,
		seenToolCalls: make(map[string]struct{}),
	}
}

func (c *outboundConverter) ProcessStream(reader io.Reader, logger *debug.Logger, writer func(upstream.SSEMessage) error) error {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var textBuffer strings.Builder
	var pendingEndEvent *EndEvent
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
		if logger != nil {
			logger.LogUpstreamSSE(eventType, eventData)
		}

		switch eventType {
		case "0", "9", "a":
			allowRaw := eventType == "0"
			if err := c.processChunkData(eventData, &textBuffer, writer, allowRaw); err != nil {
				return err
			}
			if !c.inCodeBlock && !looksLikeBoltPendingLead(textBuffer.String()) && !looksLikeBoltPendingJSON(textBuffer.String()) {
				if err := c.flushTextBuffer(&textBuffer, writer); err != nil {
					return err
				}
			}
			if c.emittedToolUse {
				return c.finishImmediatelyAfterToolUse(writer)
			}
		case "8":
			continue
		case "e":
			endEvent, err := c.parseEndEvent(eventData)
			if err != nil {
				continue
			}
			pendingEndEvent = endEvent
			continue
		case "d":
			endEvent, err := c.parseEndEvent(eventData)
			if err != nil {
				endEvent = pendingEndEvent
			}
			if endEvent == nil {
				endEvent = pendingEndEvent
			}
			if endEvent == nil {
				continue
			}
			return c.flushAndFinish(endEvent, &textBuffer, writer)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	if pendingEndEvent != nil {
		return c.flushAndFinish(pendingEndEvent, &textBuffer, writer)
	}
	return nil
}

func (c *outboundConverter) parseEndEvent(data string) (*EndEvent, error) {
	var event EndEvent
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil, err
	}
	return &event, nil
}

func (c *outboundConverter) processChunkData(data string, textBuffer *strings.Builder, writer func(upstream.SSEMessage) error, allowRawFallback bool) error {
	switch firstNonSpaceByte(data) {
	case '"':
		var text string
		if err := json.Unmarshal([]byte(data), &text); err != nil {
			if allowRawFallback {
				return c.processTextContent(strings.TrimSpace(data), textBuffer, writer)
			}
			return nil
		}
		text = strings.ReplaceAll(text, "626f6c742d63632d6167656e74", "")
		return c.processTextContent(text, textBuffer, writer)
	case '{', '[':
		var value interface{}
		if err := json.Unmarshal([]byte(data), &value); err != nil {
			if allowRawFallback {
				return c.processTextContent(strings.TrimSpace(data), textBuffer, writer)
			}
			return nil
		}
		return c.processStructuredValue(value, textBuffer, writer, allowRawFallback)
	default:
		if allowRawFallback {
			return c.processTextContent(strings.TrimSpace(data), textBuffer, writer)
		}
		return nil
	}
}

func (c *outboundConverter) processStructuredValue(value interface{}, textBuffer *strings.Builder, writer func(upstream.SSEMessage) error, allowRawFallback bool) error {
	if toolCalls := extractToolCallsFromValue(value); len(toolCalls) > 0 {
		return c.flushTextAndSendToolCalls(toolCalls, textBuffer, writer)
	}

	switch v := value.(type) {
	case string:
		return c.processTextContent(v, textBuffer, writer)
	case []interface{}:
		for _, item := range v {
			if err := c.processStructuredValue(item, textBuffer, writer, allowRawFallback); err != nil {
				return err
			}
		}
		return nil
	case map[string]interface{}:
		for _, key := range boltStructuredNestedKeys {
			if nested, ok := v[key]; ok {
				switch nested.(type) {
				case []interface{}, map[string]interface{}:
					if err := c.processStructuredValue(nested, textBuffer, writer, false); err != nil {
						return err
					}
				}
			}
		}
		for _, key := range boltStructuredStringKeys {
			if raw, ok := v[key].(string); ok && strings.TrimSpace(raw) != "" {
				return c.processTextContent(raw, textBuffer, writer)
			}
		}
	}

	if !allowRawFallback {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return c.processTextContent(string(raw), textBuffer, writer)
}

func (c *outboundConverter) processTextContent(text string, textBuffer *strings.Builder, writer func(upstream.SSEMessage) error) error {
	if c.suppressText {
		return nil
	}
	trimmed := strings.TrimSpace(text)
	if leadingJSON, _, ok := extractLeadingJSONValue(trimmed); ok {
		if toolCalls := extractToolCallsFromJSON([]byte(leadingJSON)); len(toolCalls) > 0 {
			return c.flushTextAndSendToolCalls(toolCalls, textBuffer, writer)
		}
	}

	if !c.inCodeBlock {
		idx := strings.Index(text, "```")
		if idx >= 0 {
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
			c.awaitingCodeFenceHeader = true
			c.codeFenceHeader.Reset()
			c.codeBuffer.Reset()
			text = afterMarker
		}
	}

	if c.inCodeBlock {
		if idx := strings.Index(text, "```"); idx >= 0 {
			beforeEnd := text[:idx]
			afterEnd := text[idx+3:]
			c.appendCodeBlockContent(beforeEnd)
			codeContent := strings.Trim(c.codeBuffer.String(), "\r\n")
			fenceHeader := strings.TrimSpace(c.codeFenceHeader.String())
			c.codeBuffer.Reset()
			c.codeFenceHeader.Reset()
			c.inCodeBlock = false
			c.awaitingCodeFenceHeader = false
			if err := c.processCodeBlock(fenceHeader, codeContent, textBuffer, writer); err != nil {
				return err
			}
			if afterEnd != "" {
				textBuffer.WriteString(afterEnd)
			}
		} else {
			c.appendCodeBlockContent(text)
		}
		return nil
	}

	textBuffer.WriteString(text)
	return nil
}

func (c *outboundConverter) appendCodeBlockContent(text string) {
	if !c.awaitingCodeFenceHeader {
		c.codeBuffer.WriteString(text)
		return
	}
	if text == "" {
		return
	}

	if idx := strings.IndexAny(text, "\r\n"); idx >= 0 {
		c.codeFenceHeader.WriteString(text[:idx])
		c.awaitingCodeFenceHeader = false
		next := idx + 1
		if text[idx] == '\r' && next < len(text) && text[next] == '\n' {
			next++
		}
		if next < len(text) {
			c.codeBuffer.WriteString(text[next:])
		}
		return
	}

	c.codeFenceHeader.WriteString(text)
}

func (c *outboundConverter) processCodeBlock(fenceHeader string, content string, textBuffer *strings.Builder, writer func(upstream.SSEMessage) error) error {
	if toolCalls := extractToolCallsFromJSON([]byte(content)); len(toolCalls) > 0 {
		return c.flushTextAndSendToolCalls(toolCalls, textBuffer, writer)
	}

	textBuffer.WriteString("```")
	if fenceHeader != "" {
		textBuffer.WriteString(fenceHeader)
	}
	textBuffer.WriteByte('\n')
	textBuffer.WriteString(content)
	textBuffer.WriteString("\n```")
	return nil
}

func (c *outboundConverter) sendTextDelta(text string, writer func(upstream.SSEMessage) error) error {
	text = sanitizeBoltAssistantText(text)
	if text == "" {
		return nil
	}
	c.finalText.WriteString(text)
	return writeBoltStreamMessage(writer, "model.text-delta", map[string]interface{}{
		"delta": text,
	})
}

func (c *outboundConverter) FinalText() string {
	if c == nil {
		return ""
	}
	return c.finalText.String()
}

func (c *outboundConverter) FinishReason() string {
	if c == nil {
		return ""
	}
	return c.finishReason
}

func (c *outboundConverter) FirstToolName() string {
	if c == nil {
		return ""
	}
	return c.firstToolName
}

func (c *outboundConverter) FirstToolPath() string {
	if c == nil {
		return ""
	}
	return c.firstToolPath
}

func (c *outboundConverter) FirstToolInput() string {
	if c == nil {
		return ""
	}
	return c.firstToolInput
}

func (c *outboundConverter) sendToolUse(toolCall *ToolCall, writer func(upstream.SSEMessage) error) error {
	if toolCall == nil || strings.TrimSpace(toolCall.Function) == "" {
		return nil
	}
	params := normalizeToolCallParameters(toolCall.Parameters)
	toolName := strings.TrimSpace(toolCall.Function)
	params = sanitizeBoltEditLineNumbers(toolName, params)
	key := toolCall.Function + "\x00" + string(params)
	if _, exists := c.seenToolCalls[key]; exists {
		return nil
	}
	c.seenToolCalls[key] = struct{}{}
	c.emittedToolUse = true
	c.suppressText = true
	if c.firstToolName == "" {
		c.firstToolName = toolName
		c.firstToolPath = extractBoltToolPath(toolCall.Parameters)
		c.firstToolInput = string(params)
	}
	return writeBoltStreamMessage(writer, "model.tool-call", map[string]interface{}{
		"toolCallId": "toolu_" + generateRandomID(20),
		"toolName":   toolName,
		"input":      string(params),
	})
}

func (c *outboundConverter) flushTextAndSendToolCalls(toolCalls []ToolCall, textBuffer *strings.Builder, writer func(upstream.SSEMessage) error) error {
	if textBuffer != nil {
		textBuffer.Reset()
	}
	for i := range toolCalls {
		if err := c.sendToolUse(&toolCalls[i], writer); err != nil {
			return err
		}
	}
	return nil
}

func (c *outboundConverter) flushTextBuffer(textBuffer *strings.Builder, writer func(upstream.SSEMessage) error) error {
	if textBuffer == nil || textBuffer.Len() == 0 {
		return nil
	}
	text := textBuffer.String()
	textBuffer.Reset()

	if idx := findEmbeddedToolCallStart(text); idx >= 0 {
		jsonPart := strings.TrimSpace(text[idx:])
		if leadingJSON, _, ok := extractLeadingJSONValue(jsonPart); ok {
			if toolCalls := extractToolCallsFromJSON([]byte(leadingJSON)); len(toolCalls) > 0 {
				preamble := strings.TrimSpace(text[:idx])
				if preamble != "" {
					if err := c.sendTextDelta(preamble, writer); err != nil {
						return err
					}
				}
				for i := range toolCalls {
					if err := c.sendToolUse(&toolCalls[i], writer); err != nil {
						return err
					}
				}
				return nil
			}
		}
	}

	return c.sendTextDelta(text, writer)
}

func (c *outboundConverter) sendEndEvents(endEvent *EndEvent, writer func(upstream.SSEMessage) error) error {
	if endEvent != nil {
		c.outputTokens = endEvent.Usage.CompletionTokens
	}
	finishReason := "end_turn"
	if c.emittedToolUse || (endEvent != nil && strings.EqualFold(strings.TrimSpace(endEvent.FinishReason), "tool_use")) {
		finishReason = "tool_use"
	}
	c.finishReason = finishReason

	return writeBoltStreamMessage(writer, "model.finish", map[string]interface{}{
		"finishReason": finishReason,
		"usage": map[string]interface{}{
			"inputTokens":   c.inputTokens,
			"outputTokens":  c.outputTokens,
			"input_tokens":  c.inputTokens,
			"output_tokens": c.outputTokens,
		},
	})
}

func (c *outboundConverter) finishImmediatelyAfterToolUse(writer func(upstream.SSEMessage) error) error {
	return c.sendEndEvents(nil, writer)
}

func (c *outboundConverter) flushAndFinish(endEvent *EndEvent, textBuffer *strings.Builder, writer func(upstream.SSEMessage) error) error {
	if c.inCodeBlock && c.codeBuffer.Len() > 0 {
		codeContent := strings.Trim(c.codeBuffer.String(), "\r\n")
		fenceHeader := strings.TrimSpace(c.codeFenceHeader.String())
		c.codeBuffer.Reset()
		c.codeFenceHeader.Reset()
		c.inCodeBlock = false
		c.awaitingCodeFenceHeader = false
		if err := c.processCodeBlock(fenceHeader, codeContent, textBuffer, writer); err != nil {
			return err
		}
	}
	if err := c.flushTextBuffer(textBuffer, writer); err != nil {
		return err
	}
	return c.sendEndEvents(endEvent, writer)
}

func looksLikeBoltPendingLead(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	return strings.HasSuffix(trimmed, ":") || strings.HasSuffix(trimmed, "：")
}

func looksLikeBoltPendingJSON(text string) bool {
	if idx := findEmbeddedToolCallStart(text); idx >= 0 {
		candidate := strings.TrimSpace(text[idx:])
		if candidate == "" {
			return false
		}
		if _, _, ok := extractLeadingJSONValue(candidate); ok {
			return false
		}
		return true
	}
	open := 0
	for _, ch := range text {
		switch ch {
		case '{':
			open++
		case '}':
			open--
		}
	}
	return open > 0
}

func findEmbeddedToolCallStart(text string) int {
	for idx := 0; idx < len(text); idx++ {
		if text[idx] != '{' {
			continue
		}
		j := idx + 1
		for j < len(text) {
			switch text[j] {
			case ' ', '\t', '\r', '\n':
				j++
			default:
				goto check
			}
		}
		continue
	check:
		if strings.HasPrefix(text[j:], `"tool"`) || strings.HasPrefix(text[j:], `"tool_calls"`) {
			return idx
		}
	}
	return -1
}

func extractToolCallsFromJSON(data []byte) []ToolCall {
	var value interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		return nil
	}
	return extractToolCallsFromValue(value)
}

func extractToolCallsFromValue(value interface{}) []ToolCall {
	var calls []ToolCall
	appendToolCallsFromValue(&calls, value)
	return calls
}

func appendToolCallsFromValue(calls *[]ToolCall, value interface{}) {
	switch v := value.(type) {
	case []interface{}:
		for _, item := range v {
			appendToolCallsFromValue(calls, item)
		}
	case map[string]interface{}:
		for _, key := range boltToolCallCollectionKeys {
			if nested, ok := v[key]; ok {
				appendToolCallsFromValue(calls, nested)
				if len(*calls) > 0 {
					return
				}
			}
		}
		if call, ok := parseToolCallValue(v); ok {
			*calls = append(*calls, call)
		}
	}
}

func parseToolCallValue(v map[string]interface{}) (ToolCall, bool) {
	typeHint := strings.ToLower(strings.TrimSpace(stringValue(v["type"])))

	if toolName := strings.TrimSpace(stringValue(v["tool"])); toolName != "" {
		return ToolCall{Function: toolName, Parameters: normalizeToolCallParameters(firstNonNil(v["parameters"], v["params"], v["arguments"], v["args"], v["input"]))}, true
	}

	if functionValue, ok := v["function"].(map[string]interface{}); ok {
		if toolName := strings.TrimSpace(stringValue(functionValue["name"])); toolName != "" {
			args := firstNonNil(functionValue["parameters"], functionValue["arguments"], v["parameters"], v["arguments"], v["args"], v["input"])
			return ToolCall{Function: toolName, Parameters: normalizeToolCallParameters(args)}, true
		}
	}

	name := strings.TrimSpace(stringValue(v["name"]))
	if name == "" {
		name = strings.TrimSpace(stringValue(v["function"]))
	}
	if name == "" {
		name = strings.TrimSpace(stringValue(v["toolName"]))
	}
	if name == "" {
		return ToolCall{}, false
	}

	args := firstNonNil(v["parameters"], v["params"], v["arguments"], v["args"], v["input"])
	if args == nil && typeHint != "tool_use" && typeHint != "tool_call" && typeHint != "function" {
		return ToolCall{}, false
	}

	return ToolCall{Function: name, Parameters: normalizeToolCallParameters(args)}, true
}

func firstNonNil(values ...interface{}) interface{} {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func stringValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return ""
	}
}

func normalizeToolCallParameters(value interface{}) json.RawMessage {
	switch v := value.(type) {
	case nil:
		return json.RawMessage("{}")
	case json.RawMessage:
		if len(v) == 0 {
			return json.RawMessage("{}")
		}
		return v
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return json.RawMessage("{}")
		}
		if json.Valid([]byte(trimmed)) {
			return json.RawMessage(trimmed)
		}
	}

	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || string(data) == "null" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(data)
}

func writeBoltStreamMessage(writer func(upstream.SSEMessage) error, eventType string, eventMap map[string]interface{}) error {
	if writer == nil {
		return nil
	}
	raw, err := json.Marshal(eventMap)
	if err != nil {
		return err
	}
	return writer(upstream.SSEMessage{
		Type:    eventType,
		Event:   eventMap,
		RawJSON: raw,
	})
}

func firstNonSpaceByte(text string) byte {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return text[i]
		}
	}
	return 0
}

func extractLeadingJSONValue(text string) (string, string, bool) {
	text = strings.TrimLeft(text, " \t\r\n")
	if text == "" {
		return "", "", false
	}

	var stack []byte
	inString := false
	escaped := false

	switch text[0] {
	case '{', '[':
		stack = append(stack, text[0])
	default:
		return "", "", false
	}

	for i := 1; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, ch)
		case '}', ']':
			if len(stack) == 0 || !jsonDelimitersMatch(stack[len(stack)-1], ch) {
				return "", "", false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return text[:i+1], text[i+1:], true
			}
		}
	}

	return "", "", false
}

func jsonDelimitersMatch(open, close byte) bool {
	return (open == '{' && close == '}') || (open == '[' && close == ']')
}

func sanitizeBoltEditLineNumbers(toolName string, params json.RawMessage) json.RawMessage {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "edit", "write":
	default:
		return params
	}

	var m map[string]interface{}
	if err := json.Unmarshal(params, &m); err != nil {
		return params
	}

	changed := false
	for _, key := range []string{"old_string", "new_string", "content"} {
		raw, ok := m[key]
		if !ok {
			continue
		}
		s, ok := raw.(string)
		if !ok || s == "" {
			continue
		}
		stripped := stripBoltReadLineNumbers(s)
		if stripped != s {
			m[key] = stripped
			changed = true
		}
	}

	if !changed {
		return params
	}
	out, err := json.Marshal(m)
	if err != nil {
		return params
	}
	return json.RawMessage(out)
}
