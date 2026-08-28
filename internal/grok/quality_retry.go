package grok

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/store"
)

const qualityGateMaxBytes = 8 << 20
const missingThinkingCooldown = 10 * time.Minute

func responseRequiresThinking(spec ModelSpec, req *ChatCompletionsRequest) bool {
	if req != nil {
		if !req.Stream {
			return false
		}
		if req.ReasoningEffort != nil {
			effort := strings.ToLower(strings.TrimSpace(*req.ReasoningEffort))
			if effort == "none" || effort == "disabled" || effort == "off" {
				return false
			}
		}
		if req.Thinking != nil {
			value := strings.ToLower(strings.TrimSpace(*req.Thinking))
			if value == "none" || value == "disabled" || value == "false" || value == "off" {
				return false
			}
		}
	}
	model := strings.ToLower(firstNonEmpty(spec.ConsoleModel, spec.UpstreamModel, spec.ID))
	if strings.Contains(model, "non-reasoning") || strings.Contains(model, "build-0.1") {
		return false
	}
	return spec.Upstream == UpstreamCLI || spec.Upstream == UpstreamConsole || strings.TrimSpace(spec.ConsoleModel) != ""
}

// retryMissingThinking holds only the response prelude. Once reasoning
// evidence appears, the buffered bytes and the live body are joined and normal
// realtime streaming resumes. A fully completed response without evidence is
// retried once on another account; if no replacement exists, it fails open.
func (h *Handler) retryMissingThinking(
	ctx context.Context,
	sess *chatAccountSession,
	resp *http.Response,
	provider string,
	openNext func([]int64) (*chatAccountSession, error),
	doNext func() (*http.Response, error),
) (*http.Response, error) {
	if resp == nil || sess == nil || sess.acc == nil {
		return resp, nil
	}
	missing, err := gateResponseForThinking(resp)
	if err != nil || !missing {
		return resp, err
	}
	first := resp
	firstID := sess.acc.ID
	h.markMissingThinking(ctx, sess.acc)
	next, openErr := openNext([]int64{firstID})
	if openErr != nil || next == nil {
		return first, nil
	}
	_ = first.Body.Close()
	sess.Close()
	sess.acc, sess.token, sess.poolCandidates, sess.release = next.acc, next.token, next.poolCandidates, next.release
	h.bindAffinity(ctx, provider, next.acc.ID)
	retry, retryErr := doNext()
	if retryErr != nil {
		return nil, retryErr
	}
	missingAgain, gateErr := gateResponseForThinking(retry)
	if gateErr != nil {
		_ = retry.Body.Close()
		return nil, gateErr
	}
	if missingAgain {
		h.markMissingThinking(ctx, sess.acc)
	}
	return retry, nil
}

func (h *Handler) markMissingThinking(ctx context.Context, acc *store.Account) {
	if h == nil || h.lb == nil || acc == nil {
		return
	}
	acc.QuotaResetAt = time.Now().Add(missingThinkingCooldown)
	h.lb.MarkAccountStatus(ctx, acc, "429")
}

func gateResponseForThinking(resp *http.Response) (bool, error) {
	if resp == nil || resp.Body == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, nil
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "text/event-stream") {
		data, err := io.ReadAll(io.LimitReader(resp.Body, qualityGateMaxBytes+1))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(data))
		if err != nil {
			return false, err
		}
		if len(data) > qualityGateMaxBytes {
			return false, nil
		}
		return !payloadHasThinkingEvidence(data) && qualityPayloadNeedsRetry(data), nil
	}

	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	var captured bytes.Buffer
	for captured.Len() <= qualityGateMaxBytes {
		line, err := reader.ReadBytes('\n')
		captured.Write(line)
		if payloadHasThinkingEvidence(sseData(line)) {
			resp.Body = &joinedReadCloser{Reader: io.MultiReader(bytes.NewReader(captured.Bytes()), reader), closer: resp.Body}
			return false, nil
		}
		if err == io.EOF {
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(captured.Bytes()))
			return qualityPayloadNeedsRetry(captured.Bytes()), nil
		}
		if err != nil {
			return false, err
		}
	}
	resp.Body = &joinedReadCloser{Reader: io.MultiReader(bytes.NewReader(captured.Bytes()), reader), closer: resp.Body}
	return false, nil
}

func qualityPayloadNeedsRetry(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	visible := 0
	for _, line := range strings.Split(string(data), "\n") {
		payload := []byte(strings.TrimSpace(line))
		if strings.HasPrefix(string(payload), "data:") {
			payload = sseData(payload)
		}
		if len(payload) == 0 {
			continue
		}
		var value interface{}
		if json.Unmarshal(payload, &value) == nil {
			visible += visibleTextLength(value, false)
		}
	}
	// Empty terminal streams are retriable. Very short answers are allowed
	// because acknowledgements such as "ok" legitimately contain no thinking.
	return visible == 0 || visible >= 32
}

func visibleTextLength(value interface{}, reasoningScope bool) int {
	switch item := value.(type) {
	case map[string]interface{}:
		typeName := strings.ToLower(strings.TrimSpace(fmt.Sprint(item["type"])))
		scope := reasoningScope || strings.Contains(typeName, "reasoning") || strings.Contains(typeName, "thinking")
		total := 0
		if !scope {
			for _, key := range []string{"content", "text", "delta", "output_text"} {
				if text, ok := item[key].(string); ok {
					total += len([]rune(text))
				}
			}
		}
		for _, child := range item {
			total += visibleTextLength(child, scope)
		}
		return total
	case []interface{}:
		total := 0
		for _, child := range item {
			total += visibleTextLength(child, reasoningScope)
		}
		return total
	}
	return 0
}

type joinedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *joinedReadCloser) Close() error { return r.closer.Close() }

func sseData(line []byte) []byte {
	text := strings.TrimSpace(string(line))
	if !strings.HasPrefix(text, "data:") {
		return nil
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, "data:"))
	if text == "" || text == "[DONE]" {
		return nil
	}
	return []byte(text)
}

func payloadHasThinkingEvidence(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	var value interface{}
	if json.Unmarshal(data, &value) != nil {
		return false
	}
	return valueHasThinkingEvidence(value, false)
}

func valueHasThinkingEvidence(value interface{}, reasoningScope bool) bool {
	switch item := value.(type) {
	case map[string]interface{}:
		typeName := strings.ToLower(strings.TrimSpace(fmt.Sprint(item["type"])))
		eventName := strings.ToLower(strings.TrimSpace(fmt.Sprint(item["event"])))
		scope := reasoningScope || strings.Contains(typeName, "reasoning") || strings.Contains(eventName, "reasoning")
		if scope {
			for _, key := range []string{"delta", "text", "encrypted_content", "thinking", "reasoning_content"} {
				if value := strings.TrimSpace(fmt.Sprint(item[key])); value != "" && value != "<nil>" {
					return true
				}
			}
		}
		if value := strings.TrimSpace(fmt.Sprint(item["reasoning_content"])); value != "" && value != "<nil>" {
			return true
		}
		if usage, _ := item["usage"].(map[string]interface{}); usage != nil {
			if interfaceToInt(usage["reasoning_tokens"]) > 0 {
				return true
			}
			if details, _ := usage["output_tokens_details"].(map[string]interface{}); details != nil && interfaceToInt(details["reasoning_tokens"]) > 0 {
				return true
			}
		}
		for _, child := range item {
			if valueHasThinkingEvidence(child, scope) {
				return true
			}
		}
	case []interface{}:
		for _, child := range item {
			if valueHasThinkingEvidence(child, reasoningScope) {
				return true
			}
		}
	}
	return false
}
