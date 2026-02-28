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

		write := func(event string, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			flusher.Flush()
			if logger != nil {
				logger.LogOutputSSE(event, data)
			}
		}

		startData, _ := json.Marshal(map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":      msgID,
				"type":    "message",
				"role":    "assistant",
				"content": []interface{}{},
				"model":   req.Model,
				"usage":   map[string]int{"input_tokens": inputTokens, "output_tokens": 0},
			},
		})
		write("message_start", string(startData))

		blockStart, _ := json.Marshal(map[string]interface{}{
			"type":          "content_block_start",
			"index":         0,
			"content_block": map[string]string{"type": "text", "text": ""},
		})
		write("content_block_start", string(blockStart))

		blockDelta, _ := json.Marshal(map[string]interface{}{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]string{"type": "text_delta", "text": prefix},
		})
		write("content_block_delta", string(blockDelta))

		blockStop, _ := json.Marshal(map[string]interface{}{
			"type":  "content_block_stop",
			"index": 0,
		})
		write("content_block_stop", string(blockStop))

		msgDelta, _ := json.Marshal(map[string]interface{}{
			"type":  "message_delta",
			"delta": map[string]string{"stop_reason": "end_turn"},
			"usage": map[string]int{"output_tokens": outputTokens},
		})
		write("message_delta", string(msgDelta))

		msgStop, _ := json.Marshal(map[string]string{"type": "message_stop"})
		write("message_stop", string(msgStop))
		if logger != nil {
			logger.LogSummary(inputTokens, outputTokens, time.Since(startTime), "end_turn")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"id":            msgID,
		"type":          "message",
		"role":          "assistant",
		"content":       []map[string]string{{"type": "text", "text": prefix}},
		"model":         req.Model,
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]int{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
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

		write := func(event string, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			flusher.Flush()
			if logger != nil {
				logger.LogOutputSSE(event, data)
			}
		}

		startData, _ := json.Marshal(map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":      msgID,
				"type":    "message",
				"role":    "assistant",
				"content": []interface{}{},
				"model":   req.Model,
				"usage":   map[string]int{"input_tokens": inputTokens, "output_tokens": 0},
			},
		})
		write("message_start", string(startData))

		blockStart, _ := json.Marshal(map[string]interface{}{
			"type":          "content_block_start",
			"index":         0,
			"content_block": map[string]string{"type": "text", "text": ""},
		})
		write("content_block_start", string(blockStart))

		blockDelta, _ := json.Marshal(map[string]interface{}{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]string{"type": "text_delta", "text": text},
		})
		write("content_block_delta", string(blockDelta))

		blockStop, _ := json.Marshal(map[string]interface{}{
			"type":  "content_block_stop",
			"index": 0,
		})
		write("content_block_stop", string(blockStop))

		msgDelta, _ := json.Marshal(map[string]interface{}{
			"type":  "message_delta",
			"delta": map[string]string{"stop_reason": "end_turn"},
			"usage": map[string]int{"output_tokens": outputTokens},
		})
		write("message_delta", string(msgDelta))

		msgStop, _ := json.Marshal(map[string]string{"type": "message_stop"})
		write("message_stop", string(msgStop))
		if logger != nil {
			logger.LogSummary(inputTokens, outputTokens, time.Since(startTime), "end_turn")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"id":            msgID,
		"type":          "message",
		"role":          "assistant",
		"content":       []map[string]string{{"type": "text", "text": text}},
		"model":         req.Model,
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]int{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
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
