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
		// Retrying a tool-capable turn can repeat a hosted search or another
		// side effect. Quality retry is therefore limited to plain generation.
		if len(req.Tools) > 0 || len(req.ResponsesTools) > 0 || req.ToolChoice != nil {
			return false
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
// retried across distinct accounts up to the configured bounded attempt count;
// if no replacement exists, the configured exhausted policy is applied.
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
	current := resp
	used := make([]int64, 0, h.qualityMaxAttempts())
	for attempt := 1; attempt <= h.qualityMaxAttempts(); attempt++ {
		missing, err := gateResponseForThinkingWithOptions(ctx, current, h.qualityHoldDuration(), h.qualityMinVisibleChars())
		if err != nil {
			_ = current.Body.Close()
			return nil, err
		}
		h.auditQualityAttempt(ctx, sess.acc, provider, attempt, missing)
		if !missing {
			h.clearMissingThinkingStrike(ctx, sess.acc)
			return current, nil
		}
		h.markMissingThinking(ctx, sess.acc)
		used = append(used, sess.acc.ID)
		if attempt >= h.qualityMaxAttempts() {
			break
		}
		next, openErr := openNext(used)
		if openErr != nil || next == nil {
			break
		}
		_ = current.Body.Close()
		sess.Close()
		sess.acc, sess.token, sess.poolCandidates, sess.release = next.acc, next.token, next.poolCandidates, next.release
		h.bindAffinity(ctx, provider, next.acc.ID)
		current, err = doNext()
		if err != nil {
			return nil, err
		}
	}
	if h.qualityExhaustedMode() == "error" {
		_ = current.Body.Close()
		return nil, newUpstreamError(http.StatusServiceUnavailable, nil, []byte(`{"error":{"code":"quality_degraded","message":"reasoning evidence was missing after all quality attempts"}}`), "")
	}
	return current, nil
}

func (h *Handler) markMissingThinking(ctx context.Context, acc *store.Account) {
	if h == nil || h.lb == nil || h.lb.Store == nil || acc == nil {
		return
	}
	now := time.Now()
	cooldown := h.missingThinkingCooldown()
	if acc.MissingThinkingStrikes > 0 && !acc.MissingThinkingLastAt.IsZero() && !now.Before(acc.MissingThinkingLastAt.Add(cooldown)) {
		acc.MissingThinkingStrikes++
		acc.MissingThinkingLastAt = now
		acc.Enabled = false
		acc.StatusCode = "missing_thinking_disabled"
		acc.LastAttempt = now
		_ = h.lb.Store.UpdateAccount(ctx, acc)
		return
	}
	if acc.MissingThinkingStrikes == 0 {
		acc.MissingThinkingStrikes = 1
	}
	acc.MissingThinkingLastAt = now
	acc.QuotaResetAt = now.Add(cooldown)
	h.lb.MarkAccountStatus(ctx, acc, "429")
}

func (h *Handler) clearMissingThinkingStrike(ctx context.Context, acc *store.Account) {
	if h == nil || h.lb == nil || h.lb.Store == nil || acc == nil || acc.MissingThinkingStrikes == 0 {
		return
	}
	acc.MissingThinkingStrikes = 0
	acc.MissingThinkingLastAt = time.Time{}
	_ = h.lb.Store.UpdateAccount(ctx, acc)
}

func (h *Handler) qualityHoldDuration() time.Duration {
	if h == nil || h.cfg == nil {
		return 30 * time.Second
	}
	return h.cfg.GrokQualityHoldDuration()
}

func (h *Handler) qualityMinVisibleChars() int {
	if h == nil || h.cfg == nil {
		return 32
	}
	return h.cfg.GrokQualityMinVisibleChars()
}

func (h *Handler) qualityExhaustedMode() string {
	if h == nil || h.cfg == nil {
		return "fail_open"
	}
	return h.cfg.GrokQualityExhaustedMode()
}

func (h *Handler) qualityMaxAttempts() int {
	if h == nil || h.cfg == nil {
		return 6
	}
	return h.cfg.GrokQualityAttempts()
}

func (h *Handler) missingThinkingCooldown() time.Duration {
	if h == nil || h.cfg == nil {
		return 10 * time.Minute
	}
	return h.cfg.GrokMissingThinkingCooldown()
}

func gateResponseForThinking(resp *http.Response) (bool, error) {
	return gateResponseForThinkingWithOptions(context.Background(), resp, 30*time.Second, 32)
}

type thinkingGateRead struct {
	data []byte
	err  error
}

func gateResponseForThinkingWithOptions(ctx context.Context, resp *http.Response, hold time.Duration, minVisible int) (bool, error) {
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
		return !payloadHasThinkingEvidence(data) && qualityPayloadNeedsRetryWithMin(data, minVisible), nil
	}
	if hold <= 0 {
		hold = 30 * time.Second
	}
	original := resp.Body
	reads := make(chan thinkingGateRead, 8)
	go func() {
		defer close(reads)
		reader := bufio.NewReaderSize(original, 64*1024)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 || err != nil {
				select {
				case reads <- thinkingGateRead{data: line, err: err}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	timer := time.NewTimer(hold)
	defer timer.Stop()
	var captured bytes.Buffer
	for captured.Len() <= qualityGateMaxBytes {
		select {
		case <-ctx.Done():
			_ = original.Close()
			return false, ctx.Err()
		case <-timer.C:
			resp.Body = &joinedReadCloser{Reader: io.MultiReader(bytes.NewReader(captured.Bytes()), &thinkingGateChannelReader{reads: reads}), closer: original}
			return false, nil
		case result, ok := <-reads:
			if !ok {
				_ = original.Close()
				resp.Body = io.NopCloser(bytes.NewReader(captured.Bytes()))
				return qualityPayloadNeedsRetryWithMin(captured.Bytes(), minVisible), nil
			}
			captured.Write(result.data)
			if payloadHasThinkingEvidence(sseData(result.data)) {
				resp.Body = &joinedReadCloser{Reader: io.MultiReader(bytes.NewReader(captured.Bytes()), &thinkingGateChannelReader{reads: reads}), closer: original}
				return false, nil
			}
			if result.err == io.EOF {
				_ = original.Close()
				resp.Body = io.NopCloser(bytes.NewReader(captured.Bytes()))
				return qualityPayloadNeedsRetryWithMin(captured.Bytes(), minVisible), nil
			}
			if result.err != nil {
				_ = original.Close()
				return false, result.err
			}
		}
	}
	resp.Body = &joinedReadCloser{Reader: io.MultiReader(bytes.NewReader(captured.Bytes()), &thinkingGateChannelReader{reads: reads}), closer: original}
	return false, nil
}

type thinkingGateChannelReader struct {
	reads   <-chan thinkingGateRead
	pending []byte
	err     error
}

func (r *thinkingGateChannelReader) Read(target []byte) (int, error) {
	for len(r.pending) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		result, ok := <-r.reads
		if !ok {
			return 0, io.EOF
		}
		r.pending = result.data
		r.err = result.err
		if len(r.pending) == 0 && r.err != nil {
			return 0, r.err
		}
	}
	n := copy(target, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func qualityPayloadNeedsRetry(data []byte) bool {
	return qualityPayloadNeedsRetryWithMin(data, 32)
}

func qualityPayloadNeedsRetryWithMin(data []byte, minVisible int) bool {
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
	if minVisible <= 0 {
		minVisible = 32
	}
	return visible == 0 || visible >= minVisible
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
