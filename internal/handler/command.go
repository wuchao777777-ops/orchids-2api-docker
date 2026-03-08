package handler

import (
	"fmt"
	"github.com/goccy/go-json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"orchids-api/internal/debug"
	"orchids-api/internal/tiktoken"

	"github.com/kballard/go-shellquote"
)

func isCommandPrefixRequest(req ClaudeRequest) (bool, string) {
	userText := extractUserText(req.Messages)
	if userText == "" {
		return false, ""
	}
	lower := strings.ToLower(userText)
	if !strings.Contains(lower, "<policy_spec>") && !strings.Contains(lower, "command prefix") {
		return false, ""
	}
	command := extractCommandFromPolicy(userText)
	if command == "" {
		return false, ""
	}
	return true, command
}

func extractCommandFromPolicy(text string) string {
	re := regexp.MustCompile(`(?m)^Command:\s*(.+)$`)
	if match := re.FindStringSubmatch(text); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	if idx := strings.Index(text, "Command:"); idx >= 0 {
		return strings.TrimSpace(text[idx+len("Command:"):])
	}
	return ""
}

func writeCommandPrefixResponse(w http.ResponseWriter, req ClaudeRequest, prefix string, startTime time.Time, logger *debug.Logger) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "none"
	}
	inputTokens := tiktoken.EstimateTextTokens(extractUserText(req.Messages))
	outputTokens := tiktoken.EstimateTextTokens(prefix)
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixMilli())

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		write := func(event string, data []byte) {
			_ = writeSSEFrameBytes(w, event, data)
			flusher.Flush()
			if logger != nil {
				logger.LogOutputSSE(event, string(data))
			}
		}

		// Define constants for fully static messages
		startData, _ := marshalSSEMessageStartBytes(msgID, req.Model, inputTokens, 0)
		blockStart, _ := marshalSSEContentBlockStartTextBytes(0)
		blockDelta, _ := marshalSSEContentBlockDeltaTextBytes(0, prefix)
		blockStop, _ := marshalSSEContentBlockStopBytes(0)
		msgDelta, _ := marshalSSEMessageDeltaBytes("end_turn", outputTokens)
		write("message_start", startData)
		write("content_block_start", blockStart)
		write("content_block_delta", blockDelta)
		write("content_block_stop", blockStop)
		write("message_delta", msgDelta)
		write("message_stop", sseMessageStopBytes)
		if logger != nil {
			logger.LogSummary(inputTokens, outputTokens, time.Since(startTime), "end_turn")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	response := struct {
		ID           string              `json:"id"`
		Type         string              `json:"type"`
		Role         string              `json:"role"`
		Content      []map[string]string `json:"content"`
		Model        string              `json:"model"`
		StopReason   string              `json:"stop_reason"`
		StopSequence interface{}         `json:"stop_sequence"`
		Usage        struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}{
		ID:           msgID,
		Type:         "message",
		Role:         "assistant",
		Content:      []map[string]string{{"type": "text", "text": prefix}},
		Model:        req.Model,
		StopReason:   "end_turn",
		StopSequence: nil,
		Usage: struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		}{InputTokens: inputTokens, OutputTokens: outputTokens},
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		if logger != nil {
			logger.LogOutputSSE("error", fmt.Sprintf("failed to encode response: %v", err))
		}
	}
	if logger != nil {
		logger.LogSummary(inputTokens, outputTokens, time.Since(startTime), "end_turn")
	}
}

func writeTopicClassifierResponse(w http.ResponseWriter, req ClaudeRequest, startTime time.Time, logger *debug.Logger) {
	isNewTopic, title := classifyTopicRequest(req)
	payload := map[string]interface{}{
		"isNewTopic": isNewTopic,
		"title":      nil,
	}
	if isNewTopic {
		payload["title"] = title
	}
	raw, _ := json.Marshal(payload)
	text := string(raw)

	inputTokens := tiktoken.EstimateTextTokens(extractUserText(req.Messages))
	outputTokens := tiktoken.EstimateTextTokens(text)
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixMilli())

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		write := func(event string, data []byte) {
			_ = writeSSEFrameBytes(w, event, data)
			flusher.Flush()
			if logger != nil {
				logger.LogOutputSSE(event, string(data))
			}
		}

		// Pre-defined static portions of the SSE stream
		startData, _ := marshalSSEMessageStartBytes(msgID, req.Model, inputTokens, 0)
		blockStart, _ := marshalSSEContentBlockStartTextBytes(0)
		blockDelta, _ := marshalSSEContentBlockDeltaTextBytes(0, text)
		blockStop, _ := marshalSSEContentBlockStopBytes(0)
		msgDelta, _ := marshalSSEMessageDeltaBytes("end_turn", outputTokens)
		write("message_start", startData)
		write("content_block_start", blockStart)
		write("content_block_delta", blockDelta)
		write("content_block_stop", blockStop)
		write("message_delta", msgDelta)
		write("message_stop", sseMessageStopBytes)
		if logger != nil {
			logger.LogSummary(inputTokens, outputTokens, time.Since(startTime), "end_turn")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	response := struct {
		ID           string              `json:"id"`
		Type         string              `json:"type"`
		Role         string              `json:"role"`
		Content      []map[string]string `json:"content"`
		Model        string              `json:"model"`
		StopReason   string              `json:"stop_reason"`
		StopSequence interface{}         `json:"stop_sequence"`
		Usage        struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}{
		ID:           msgID,
		Type:         "message",
		Role:         "assistant",
		Content:      []map[string]string{{"type": "text", "text": text}},
		Model:        req.Model,
		StopReason:   "end_turn",
		StopSequence: nil,
		Usage: struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		}{InputTokens: inputTokens, OutputTokens: outputTokens},
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		if logger != nil {
			logger.LogOutputSSE("error", fmt.Sprintf("failed to encode response: %v", err))
		}
	}
	if logger != nil {
		logger.LogSummary(inputTokens, outputTokens, time.Since(startTime), "end_turn")
	}
}

func writeSuggestionModeResponse(w http.ResponseWriter, req ClaudeRequest, startTime time.Time, logger *debug.Logger) {
	suggestion := buildLocalSuggestion(req.Messages)
	inputTokens := tiktoken.EstimateTextTokens(extractUserText(req.Messages))
	outputTokens := tiktoken.EstimateTextTokens(suggestion)
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixMilli())

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		write := func(event string, data []byte) {
			_ = writeSSEFrameBytes(w, event, data)
			flusher.Flush()
			if logger != nil {
				logger.LogOutputSSE(event, string(data))
			}
		}

		startData, _ := marshalSSEMessageStartBytes(msgID, req.Model, inputTokens, 0)
		msgDelta, _ := marshalSSEMessageDeltaBytes("end_turn", outputTokens)
		write("message_start", startData)
		if suggestion != "" {
			blockStart, _ := marshalSSEContentBlockStartTextBytes(0)
			blockDelta, _ := marshalSSEContentBlockDeltaTextBytes(0, suggestion)
			blockStop, _ := marshalSSEContentBlockStopBytes(0)
			write("content_block_start", blockStart)
			write("content_block_delta", blockDelta)
			write("content_block_stop", blockStop)
		}
		write("message_delta", msgDelta)
		write("message_stop", sseMessageStopBytes)
		if logger != nil {
			logger.LogSummary(inputTokens, outputTokens, time.Since(startTime), "end_turn")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	content := []map[string]string{}
	if suggestion != "" {
		content = []map[string]string{{"type": "text", "text": suggestion}}
	}
	response := struct {
		ID           string              `json:"id"`
		Type         string              `json:"type"`
		Role         string              `json:"role"`
		Content      []map[string]string `json:"content"`
		Model        string              `json:"model"`
		StopReason   string              `json:"stop_reason"`
		StopSequence interface{}         `json:"stop_sequence"`
		Usage        struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}{
		ID:           msgID,
		Type:         "message",
		Role:         "assistant",
		Content:      content,
		Model:        req.Model,
		StopReason:   "end_turn",
		StopSequence: nil,
		Usage: struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		}{InputTokens: inputTokens, OutputTokens: outputTokens},
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		if logger != nil {
			logger.LogOutputSSE("error", fmt.Sprintf("failed to encode response: %v", err))
		}
	}
	if logger != nil {
		logger.LogSummary(inputTokens, outputTokens, time.Since(startTime), "end_turn")
	}
}

func detectCommandPrefix(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return "none"
	}
	if looksLikeCommandInjection(trimmed) {
		return "command_injection_detected"
	}
	tokens, err := shellquote.Split(trimmed)
	if err != nil || len(tokens) == 0 {
		return "command_injection_detected"
	}

	var prefix []string
	i := 0
	for i < len(tokens) && isEnvAssignment(tokens[i]) {
		prefix = append(prefix, tokens[i])
		i++
	}
	if i >= len(tokens) {
		return "none"
	}

	cmdIndex := i
	cmd := tokens[i]
	i++
	lowerCmd := strings.ToLower(cmd)

	switch lowerCmd {
	case "git":
		subIdx := findGitSubcommandIndex(tokens, i)
		if subIdx == -1 {
			return "none"
		}
		sub := strings.ToLower(tokens[subIdx])
		if sub == "push" && subIdx == len(tokens)-1 {
			return "none"
		}
		prefix = append(prefix, tokens[cmdIndex:subIdx+1]...)
		return strings.Join(prefix, " ")
	case "go", "gg", "potion", "pig":
		if i >= len(tokens) {
			return "none"
		}
		prefix = append(prefix, tokens[cmdIndex:i+1]...)
		return strings.Join(prefix, " ")
	case "npm":
		if i >= len(tokens) {
			return "none"
		}
		sub := strings.ToLower(tokens[i])
		switch sub {
		case "run":
			if i+1 >= len(tokens) {
				return "none"
			}
			if i+2 >= len(tokens) && len(prefix) == 0 {
				return "none"
			}
			prefix = append(prefix, tokens[cmdIndex:i+2]...)
			return strings.Join(prefix, " ")
		case "test":
			if i+1 >= len(tokens) {
				return "none"
			}
			prefix = append(prefix, tokens[cmdIndex:i+1]...)
			return strings.Join(prefix, " ")
		default:
			prefix = append(prefix, tokens[cmdIndex])
			return strings.Join(prefix, " ")
		}
	default:
		prefix = append(prefix, tokens[cmdIndex])
		return strings.Join(prefix, " ")
	}
}

func isEnvAssignment(token string) bool {
	if token == "" {
		return false
	}
	if !envAssignPattern.MatchString(token) {
		return false
	}
	return true
}

var envAssignPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

func looksLikeCommandInjection(command string) bool {
	if strings.Contains(command, "\n") || strings.Contains(command, "\r") {
		return true
	}
	if strings.Contains(command, "`") || strings.Contains(command, "$(") {
		return true
	}
	if strings.Contains(command, ";") || strings.Contains(command, "||") || strings.Contains(command, "&&") {
		return true
	}
	if strings.Contains(command, "|") {
		return true
	}
	return false
}

func findGitSubcommandIndex(tokens []string, start int) int {
	for i := start; i < len(tokens); i++ {
		if gitSubcommands[strings.ToLower(tokens[i])] {
			return i
		}
	}
	return -1
}

var gitSubcommands = map[string]bool{
	"add":      true,
	"branch":   true,
	"checkout": true,
	"clone":    true,
	"commit":   true,
	"diff":     true,
	"fetch":    true,
	"log":      true,
	"merge":    true,
	"pull":     true,
	"push":     true,
	"rebase":   true,
	"reset":    true,
	"show":     true,
	"stash":    true,
	"status":   true,
	"tag":      true,
	"remote":   true,
}
