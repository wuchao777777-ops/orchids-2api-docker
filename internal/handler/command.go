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

		// Define constants for fully static messages
		const (
			sseContentBlockStart = `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
			sseContentBlockStop  = `{"type":"content_block_stop","index":0}`
			sseMessageStop       = `{"type":"message_stop"}`
		)

		startData, _ := json.Marshal(struct {
			Type    string `json:"type"`
			Message struct {
				ID      string        `json:"id"`
				Type    string        `json:"type"`
				Role    string        `json:"role"`
				Content []interface{} `json:"content"`
				Model   string        `json:"model"`
				Usage   struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}{
			Type: "message_start",
			Message: struct {
				ID      string        `json:"id"`
				Type    string        `json:"type"`
				Role    string        `json:"role"`
				Content []interface{} `json:"content"`
				Model   string        `json:"model"`
				Usage   struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}{
				ID:      msgID,
				Type:    "message",
				Role:    "assistant",
				Content: []interface{}{},
				Model:   req.Model,
				Usage: struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				}{InputTokens: inputTokens, OutputTokens: 0},
			},
		})
		write("message_start", string(startData))
		write("content_block_start", sseContentBlockStart)

		blockDelta, _ := json.Marshal(struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}{
			Type:  "content_block_delta",
			Index: 0,
			Delta: struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{Type: "text_delta", Text: prefix},
		})
		write("content_block_delta", string(blockDelta))
		write("content_block_stop", sseContentBlockStop)

		msgDelta, _ := json.Marshal(struct {
			Type  string `json:"type"`
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}{
			Type: "message_delta",
			Delta: struct {
				StopReason string `json:"stop_reason"`
			}{StopReason: "end_turn"},
			Usage: struct {
				OutputTokens int `json:"output_tokens"`
			}{OutputTokens: outputTokens},
		})
		write("message_delta", string(msgDelta))
		write("message_stop", sseMessageStop)
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

		write := func(event string, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			flusher.Flush()
			if logger != nil {
				logger.LogOutputSSE(event, data)
			}
		}

		// Pre-defined static portions of the SSE stream
		const (
			sseContentBlockStart = `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
			sseContentBlockStop  = `{"type":"content_block_stop","index":0}`
			sseMessageStop       = `{"type":"message_stop"}`
		)

		startData, _ := json.Marshal(struct {
			Type    string `json:"type"`
			Message struct {
				ID      string        `json:"id"`
				Type    string        `json:"type"`
				Role    string        `json:"role"`
				Content []interface{} `json:"content"`
				Model   string        `json:"model"`
				Usage   struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}{
			Type: "message_start",
			Message: struct {
				ID      string        `json:"id"`
				Type    string        `json:"type"`
				Role    string        `json:"role"`
				Content []interface{} `json:"content"`
				Model   string        `json:"model"`
				Usage   struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}{
				ID:      msgID,
				Type:    "message",
				Role:    "assistant",
				Content: []interface{}{},
				Model:   req.Model,
				Usage: struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				}{InputTokens: inputTokens, OutputTokens: 0},
			},
		})
		write("message_start", string(startData))
		write("content_block_start", sseContentBlockStart)

		blockDelta, _ := json.Marshal(struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}{
			Type:  "content_block_delta",
			Index: 0,
			Delta: struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{Type: "text_delta", Text: text},
		})
		write("content_block_delta", string(blockDelta))
		write("content_block_stop", sseContentBlockStop)

		msgDelta, _ := json.Marshal(struct {
			Type  string `json:"type"`
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}{
			Type: "message_delta",
			Delta: struct {
				StopReason string `json:"stop_reason"`
			}{StopReason: "end_turn"},
			Usage: struct {
				OutputTokens int `json:"output_tokens"`
			}{OutputTokens: outputTokens},
		})
		write("message_delta", string(msgDelta))
		write("message_stop", sseMessageStop)
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
