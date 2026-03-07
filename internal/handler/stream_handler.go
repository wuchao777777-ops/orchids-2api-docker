package handler

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/goccy/go-json"

	"orchids-api/internal/adapter"
	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/orchids"
	"orchids-api/internal/perf"
	"orchids-api/internal/prompt"
	"orchids-api/internal/tiktoken"
	"orchids-api/internal/upstream"
)

const (
	fnv64Offset = uint64(14695981039346656037)
	fnv64Prime  = uint64(1099511628211)
)

const (
	sseEventPrefix                 = "event: "
	sseDataPrefix                  = "data: "
	sseLineBreak                   = "\n\n"
	sseDataJoin                    = "\ndata: "
	sseDoneLine                    = "data: [DONE]\n\n"
	sseKeepAlive                   = ": keep-alive\n\n"
	sseDeferredFlushFrameThreshold = 4
	sseDeferredFlushByteThreshold  = 2048
	sseBufferedWriteMax            = 4096
	jsonHexDigits                  = "0123456789abcdef"
)

var (
	rawJSONEmptyObject  = json.RawMessage("{}")
	sseMessageStopBytes = []byte(`{"type":"message_stop"}`)
	sseTextDeltaMarker  = []byte(`"type":"text_delta"`)
	sseDoneLineBytes    = []byte(sseDoneLine)
	sseKeepAliveBytes   = []byte(sseKeepAlive)
	sseEventPrefixBytes = []byte(sseEventPrefix)
	sseDataPrefixBytes  = []byte(sseDataPrefix)
	sseLineBreakBytes   = []byte(sseLineBreak)
	sseDataJoinBytes    = []byte(sseDataJoin)
	sseEventBytesByName = map[string][]byte{
		"message_start":       []byte("message_start"),
		"message_delta":       []byte("message_delta"),
		"message_stop":        []byte("message_stop"),
		"content_block_start": []byte("content_block_start"),
		"content_block_delta": []byte("content_block_delta"),
		"content_block_stop":  []byte("content_block_stop"),
		"fs_operation":        []byte("fs_operation"),
	}
)

func mapKeys(m map[string]interface{}) []string {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Order isn't critical; keep lightweight (avoid importing sort).
	return keys
}

type sseToolUseContentBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type sseTextContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type sseThinkingContentBlock struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

type sseContentBlockStartToolUse struct {
	Type         string                 `json:"type"`
	Index        int                    `json:"index"`
	ContentBlock sseToolUseContentBlock `json:"content_block"`
}

type sseContentBlockStartText struct {
	Type         string              `json:"type"`
	Index        int                 `json:"index"`
	ContentBlock sseTextContentBlock `json:"content_block"`
}

type sseContentBlockStartThinking struct {
	Type         string                  `json:"type"`
	Index        int                     `json:"index"`
	ContentBlock sseThinkingContentBlock `json:"content_block"`
}

type sseInputJSONDelta struct {
	Type        string `json:"type"`
	PartialJSON string `json:"partial_json"`
}

type sseTextDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type sseThinkingDelta struct {
	Type     string `json:"type"`
	Thinking string `json:"thinking"`
}

type sseContentBlockDeltaInputJSON struct {
	Type  string            `json:"type"`
	Index int               `json:"index"`
	Delta sseInputJSONDelta `json:"delta"`
}

type sseContentBlockDeltaText struct {
	Type  string       `json:"type"`
	Index int          `json:"index"`
	Delta sseTextDelta `json:"delta"`
}

type sseContentBlockDeltaThinking struct {
	Type  string           `json:"type"`
	Index int              `json:"index"`
	Delta sseThinkingDelta `json:"delta"`
}

type sseContentBlockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type sseMessageDeltaDetail struct {
	StopReason string `json:"stop_reason"`
}

type sseMessageUsage struct {
	OutputTokens int `json:"output_tokens"`
}

type sseMessageDelta struct {
	Type  string                `json:"type"`
	Delta sseMessageDeltaDetail `json:"delta"`
	Usage sseMessageUsage       `json:"usage"`
}

type sseMessageStop struct {
	Type string `json:"type"`
}

func marshalJSONString(v interface{}) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalEventPayloadBytes(msg upstream.SSEMessage) ([]byte, error) {
	if len(msg.RawJSON) > 0 {
		return msg.RawJSON, nil
	}
	return json.Marshal(msg.Event)
}

func marshalEventPayload(msg upstream.SSEMessage) (string, error) {
	raw, err := marshalEventPayloadBytes(msg)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func writeSSEFrame(w io.Writer, event, data string) error {
	if _, err := io.WriteString(w, sseEventPrefix); err != nil {
		return err
	}
	if _, err := io.WriteString(w, event); err != nil {
		return err
	}
	if _, err := io.WriteString(w, sseDataJoin); err != nil {
		return err
	}
	if _, err := io.WriteString(w, data); err != nil {
		return err
	}
	_, err := io.WriteString(w, sseLineBreak)
	return err
}

func writeOpenAIFrame(w io.Writer, payload []byte) error {
	if _, err := w.Write(sseDataPrefixBytes); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err := w.Write(sseLineBreakBytes)
	return err
}

func writeSSEEventName(w io.Writer, event string) error {
	if raw, ok := sseEventBytesByName[event]; ok {
		_, err := w.Write(raw)
		return err
	}
	if sw, ok := w.(io.StringWriter); ok {
		_, err := sw.WriteString(event)
		return err
	}
	_, err := w.Write([]byte(event))
	return err
}

func writeSSEFrameBytes(w io.Writer, event string, data []byte) error {
	if _, err := w.Write(sseEventPrefixBytes); err != nil {
		return err
	}
	if err := writeSSEEventName(w, event); err != nil {
		return err
	}
	if _, err := w.Write(sseDataJoinBytes); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err := w.Write(sseLineBreakBytes)
	return err
}

func shouldFlushSSEImmediately(event, data string) bool {
	switch event {
	case "message_start", "message_delta", "message_stop", "content_block_start", "content_block_stop":
		return true
	case "content_block_delta":
		return strings.Contains(data, `"type":"text_delta"`)
	case "fs_operation":
		return false
	}
	return !strings.HasPrefix(event, "coding_agent.")
}

func (h *streamHandler) flushSSEWithLenLocked(event string, dataLen int, immediate bool, force bool) {
	if h.flusher == nil {
		return
	}
	if force || immediate {
		h.deferredFlushFrames = 0
		h.deferredFlushBytes = 0
		h.flusher.Flush()
		return
	}
	h.deferredFlushFrames++
	h.deferredFlushBytes += len(event) + dataLen + len(sseEventPrefix) + len(sseDataJoin) + len(sseLineBreak)
	if h.deferredFlushFrames >= sseDeferredFlushFrameThreshold || h.deferredFlushBytes >= sseDeferredFlushByteThreshold {
		h.deferredFlushFrames = 0
		h.deferredFlushBytes = 0
		h.flusher.Flush()
	}
}

func (h *streamHandler) flushSSELocked(event, data string, force bool) {
	h.flushSSEWithLenLocked(event, len(data), shouldFlushSSEImmediately(event, data), force)
}

func shouldFlushSSEImmediatelyBytes(event string, data []byte) bool {
	switch event {
	case "message_start", "message_delta", "message_stop", "content_block_start", "content_block_stop":
		return true
	case "content_block_delta":
		return bytes.Contains(data, sseTextDeltaMarker)
	case "fs_operation":
		return false
	}
	return !strings.HasPrefix(event, "coding_agent.")
}

func (h *streamHandler) flushSSEBytesLocked(event string, data []byte, force bool) {
	h.flushSSEWithLenLocked(event, len(data), shouldFlushSSEImmediatelyBytes(event, data), force)
}

func (h *streamHandler) flushSSEBytesLockedWithHint(event string, dataLen int, immediate bool, force bool) {
	h.flushSSEWithLenLocked(event, dataLen, immediate, force)
}

func canAppendJSONRawString(value string) bool {
	for i := 0; i < len(value); {
		b := value[i]
		if b < utf8.RuneSelf {
			if b < 0x20 || b == '\\' || b == '"' || b == '<' || b == '>' || b == '&' {
				return false
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 1 {
			return false
		}
		if r == '\u2028' || r == '\u2029' {
			return false
		}
		i += size
	}
	return true
}

func appendJSONString(builder *strings.Builder, value string) error {
	if canAppendJSONRawString(value) {
		builder.Grow(len(value) + 2)
		builder.WriteByte('"')
		builder.WriteString(value)
		builder.WriteByte('"')
		return nil
	}
	if !utf8.ValidString(value) {
		quoted, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, _ = builder.Write(quoted)
		return nil
	}

	builder.Grow(len(value) + 2)
	builder.WriteByte('"')
	start := 0
	for i := 0; i < len(value); {
		b := value[i]
		if b < utf8.RuneSelf {
			if b >= 0x20 && b != '\\' && b != '"' && b != '<' && b != '>' && b != '&' {
				i++
				continue
			}
			if start < i {
				builder.WriteString(value[start:i])
			}
			switch b {
			case '\\', '"':
				builder.WriteByte('\\')
				builder.WriteByte(b)
			case '\b':
				builder.WriteByte('\\')
				builder.WriteByte('b')
			case '\f':
				builder.WriteByte('\\')
				builder.WriteByte('f')
			case '\n':
				builder.WriteByte('\\')
				builder.WriteByte('n')
			case '\r':
				builder.WriteByte('\\')
				builder.WriteByte('r')
			case '\t':
				builder.WriteByte('\\')
				builder.WriteByte('t')
			default:
				builder.WriteString("\\u00")
				builder.WriteByte(jsonHexDigits[b>>4])
				builder.WriteByte(jsonHexDigits[b&0x0f])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == '\u2028' || r == '\u2029' {
			if start < i {
				builder.WriteString(value[start:i])
			}
			if r == '\u2028' {
				builder.WriteString("\\u2028")
			} else {
				builder.WriteString("\\u2029")
			}
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(value) {
		builder.WriteString(value[start:])
	}
	builder.WriteByte('"')
	return nil
}

func appendJSONBytes(dst []byte, value string) ([]byte, error) {
	if canAppendJSONRawString(value) {
		dst = append(dst, '"')
		dst = append(dst, value...)
		dst = append(dst, '"')
		return dst, nil
	}

	originLen := len(dst)
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(value); {
		b := value[i]
		if b < utf8.RuneSelf {
			if b >= 0x20 && b != '\\' && b != '"' && b != '<' && b != '>' && b != '&' {
				i++
				continue
			}
			if start < i {
				dst = append(dst, value[start:i]...)
			}
			switch b {
			case '\\', '"':
				dst = append(dst, '\\', b)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = append(dst, '\\', 'u', '0', '0')
				dst = append(dst, jsonHexDigits[b>>4], jsonHexDigits[b&0x0f])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 1 {
			dst = dst[:originLen]
			quoted, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			return append(dst, quoted...), nil
		}
		if r == '\u2028' || r == '\u2029' {
			if start < i {
				dst = append(dst, value[start:i]...)
			}
			if r == '\u2028' {
				dst = append(dst, '\\', 'u', '2', '0', '2', '8')
			} else {
				dst = append(dst, '\\', 'u', '2', '0', '2', '9')
			}
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(value) {
		dst = append(dst, value[start:]...)
	}
	dst = append(dst, '"')
	return dst, nil
}

func appendSSEContentBlockStartToolUse(dst []byte, index int, id, name string) ([]byte, error) {
	dst = append(dst, `{"type":"content_block_start","index":`...)
	dst = strconv.AppendInt(dst, int64(index), 10)
	dst = append(dst, `,"content_block":{"type":"tool_use","id":`...)
	var err error
	dst, err = appendJSONBytes(dst, id)
	if err != nil {
		return nil, err
	}
	dst = append(dst, `,"name":`...)
	dst, err = appendJSONBytes(dst, name)
	if err != nil {
		return nil, err
	}
	dst = append(dst, `,"input":{}}}`...)
	return dst, nil
}

func appendSSEMessageStart(dst []byte, msgID, model string, inputTokens, outputTokens int) ([]byte, error) {
	dst = append(dst, `{"type":"message_start","message":{"id":`...)
	var err error
	dst, err = appendJSONBytes(dst, msgID)
	if err != nil {
		return nil, err
	}
	dst = append(dst, `,"type":"message","role":"assistant","content":[],"model":`...)
	dst, err = appendJSONBytes(dst, model)
	if err != nil {
		return nil, err
	}
	dst = append(dst, `,"usage":{"input_tokens":`...)
	dst = strconv.AppendInt(dst, int64(inputTokens), 10)
	dst = append(dst, `,"output_tokens":`...)
	dst = strconv.AppendInt(dst, int64(outputTokens), 10)
	dst = append(dst, `}}}`...)
	return dst, nil
}

func appendSSEMessageStartNoUsage(dst []byte, msgID, model string) ([]byte, error) {
	dst = append(dst, `{"type":"message_start","message":{"id":`...)
	var err error
	dst, err = appendJSONBytes(dst, msgID)
	if err != nil {
		return nil, err
	}
	dst = append(dst, `,"type":"message","role":"assistant","content":[],"model":`...)
	dst, err = appendJSONBytes(dst, model)
	if err != nil {
		return nil, err
	}
	dst = append(dst, `}}`...)
	return dst, nil
}

func appendSSEContentBlockStartText(dst []byte, index int) ([]byte, error) {
	dst = append(dst, `{"type":"content_block_start","index":`...)
	dst = strconv.AppendInt(dst, int64(index), 10)
	dst = append(dst, `,"content_block":{"type":"text","text":""}}`...)
	return dst, nil
}

func appendSSEContentBlockStartThinking(dst []byte, index int, signature string) ([]byte, error) {
	dst = append(dst, `{"type":"content_block_start","index":`...)
	dst = strconv.AppendInt(dst, int64(index), 10)
	dst = append(dst, `,"content_block":{"type":"thinking","thinking":"","signature":`...)
	var err error
	dst, err = appendJSONBytes(dst, signature)
	if err != nil {
		return nil, err
	}
	dst = append(dst, `}}`...)
	return dst, nil
}

func appendSSEContentBlockDeltaInputJSON(dst []byte, index int, partialJSON string) ([]byte, error) {
	dst = append(dst, `{"type":"content_block_delta","index":`...)
	dst = strconv.AppendInt(dst, int64(index), 10)
	dst = append(dst, `,"delta":{"type":"input_json_delta","partial_json":`...)
	var err error
	dst, err = appendJSONBytes(dst, partialJSON)
	if err != nil {
		return nil, err
	}
	dst = append(dst, `}}`...)
	return dst, nil
}

func appendSSEContentBlockDeltaText(dst []byte, index int, text string) ([]byte, error) {
	dst = append(dst, `{"type":"content_block_delta","index":`...)
	dst = strconv.AppendInt(dst, int64(index), 10)
	dst = append(dst, `,"delta":{"type":"text_delta","text":`...)
	var err error
	dst, err = appendJSONBytes(dst, text)
	if err != nil {
		return nil, err
	}
	dst = append(dst, `}}`...)
	return dst, nil
}

func appendSSEContentBlockDeltaThinking(dst []byte, index int, thinking string) ([]byte, error) {
	dst = append(dst, `{"type":"content_block_delta","index":`...)
	dst = strconv.AppendInt(dst, int64(index), 10)
	dst = append(dst, `,"delta":{"type":"thinking_delta","thinking":`...)
	var err error
	dst, err = appendJSONBytes(dst, thinking)
	if err != nil {
		return nil, err
	}
	dst = append(dst, `}}`...)
	return dst, nil
}

func appendSSEContentBlockStop(dst []byte, index int) ([]byte, error) {
	dst = append(dst, `{"type":"content_block_stop","index":`...)
	dst = strconv.AppendInt(dst, int64(index), 10)
	dst = append(dst, '}')
	return dst, nil
}

func appendSSEMessageDelta(dst []byte, stopReason string, outputTokens int) ([]byte, error) {
	dst = append(dst, `{"type":"message_delta","delta":{"stop_reason":`...)
	var err error
	dst, err = appendJSONBytes(dst, stopReason)
	if err != nil {
		return nil, err
	}
	dst = append(dst, `},"usage":{"output_tokens":`...)
	dst = strconv.AppendInt(dst, int64(outputTokens), 10)
	dst = append(dst, `}}`...)
	return dst, nil
}

func marshalSSEContentBlockStartToolUseBytes(index int, id, name string) ([]byte, error) {
	return appendSSEContentBlockStartToolUse(make([]byte, 0, 128+len(id)+len(name)), index, id, name)
}

func marshalSSEMessageStartBytes(msgID, model string, inputTokens, outputTokens int) ([]byte, error) {
	return appendSSEMessageStart(make([]byte, 0, 192+len(msgID)+len(model)), msgID, model, inputTokens, outputTokens)
}

func marshalSSEMessageStartNoUsageBytes(msgID, model string) ([]byte, error) {
	return appendSSEMessageStartNoUsage(make([]byte, 0, 128+len(msgID)+len(model)), msgID, model)
}

func marshalSSEContentBlockStartToolUse(index int, id, name string) (string, error) {
	raw, err := marshalSSEContentBlockStartToolUseBytes(index, id, name)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalSSEContentBlockStartTextBytes(index int) ([]byte, error) {
	return appendSSEContentBlockStartText(make([]byte, 0, 96), index)
}

func marshalSSEContentBlockStartText(index int) (string, error) {
	raw, err := marshalSSEContentBlockStartTextBytes(index)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalSSEContentBlockStartThinkingBytes(index int, signature string) ([]byte, error) {
	return appendSSEContentBlockStartThinking(make([]byte, 0, 112+len(signature)), index, signature)
}

func marshalSSEContentBlockStartThinking(index int, signature string) (string, error) {
	raw, err := marshalSSEContentBlockStartThinkingBytes(index, signature)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalSSEContentBlockDeltaInputJSONBytes(index int, partialJSON string) ([]byte, error) {
	return appendSSEContentBlockDeltaInputJSON(make([]byte, 0, 96+len(partialJSON)*2), index, partialJSON)
}

func marshalSSEContentBlockDeltaInputJSON(index int, partialJSON string) (string, error) {
	raw, err := marshalSSEContentBlockDeltaInputJSONBytes(index, partialJSON)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalSSEContentBlockDeltaTextBytes(index int, text string) ([]byte, error) {
	return appendSSEContentBlockDeltaText(make([]byte, 0, 80+len(text)), index, text)
}

func marshalSSEContentBlockDeltaText(index int, text string) (string, error) {
	raw, err := marshalSSEContentBlockDeltaTextBytes(index, text)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalSSEContentBlockDeltaThinkingBytes(index int, thinking string) ([]byte, error) {
	return appendSSEContentBlockDeltaThinking(make([]byte, 0, 88+len(thinking)), index, thinking)
}

func marshalSSEContentBlockDeltaThinking(index int, thinking string) (string, error) {
	raw, err := marshalSSEContentBlockDeltaThinkingBytes(index, thinking)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalSSEContentBlockStopBytes(index int) ([]byte, error) {
	return appendSSEContentBlockStop(make([]byte, 0, 48), index)
}

func marshalSSEContentBlockStop(index int) (string, error) {
	raw, err := marshalSSEContentBlockStopBytes(index)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalSSEMessageDeltaBytes(stopReason string, outputTokens int) ([]byte, error) {
	return appendSSEMessageDelta(make([]byte, 0, 88+len(stopReason)), stopReason, outputTokens)
}

func marshalSSEMessageDelta(stopReason string, outputTokens int) (string, error) {
	raw, err := marshalSSEMessageDeltaBytes(stopReason, outputTokens)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalSSEMessageStopBytes() ([]byte, error) {
	return sseMessageStopBytes, nil
}

func marshalSSEMessageStop() (string, error) {
	return string(sseMessageStopBytes), nil
}

type streamHandler struct {
	// Configuration
	config           *config.Config
	workdir          string
	isStream         bool
	suppressThinking bool
	useUpstreamUsage bool
	outputTokenMode  string
	responseFormat   adapter.ResponseFormat

	// HTTP Response
	w       http.ResponseWriter
	flusher http.Flusher

	// State
	mu                       sync.Mutex
	outputMu                 sync.Mutex
	blockIndex               int
	msgID                    string
	startTime                time.Time
	hasReturn                bool
	finalStopReason          string
	outputTokens             int
	inputTokens              int
	activeThinkingBlockIndex int
	activeThinkingSSEIndex   int
	activeTextBlockIndex     int
	activeTextSSEIndex       int
	activeBlockType          string // "thinking", "text", "tool_use"

	// Buffers and Builders
	responseText          *strings.Builder
	outputEstimator       tiktoken.Estimator
	writeChunkBuffer      *strings.Builder
	textBlockBuilders     map[int]*strings.Builder
	thinkingBlockBuilders map[int]*strings.Builder
	thinkingBlockSigs     map[int]string
	contentBlocks         []map[string]interface{}
	currentTextIndex      int
	pendingThinkingSig    string
	hasTextOutput         bool
	lastTextDelta         string
	lastTextDeltaSource   string
	lastTextDeltaAt       time.Time
	deferredFlushFrames   int
	deferredFlushBytes    int
	openAIChunkScratch    []byte
	ssePayloadScratch     []byte

	// Tool Handling (proxy mode only)
	toolBlocks         map[string]int
	pendingToolCalls   []toolCall
	toolInputNames     map[string]string
	toolInputBuffers   map[string]*strings.Builder
	toolInputHadDelta  map[string]bool
	toolCallHandled    map[string]bool
	toolCallEmitted    map[string]struct{}
	currentToolInputID string
	toolCallCount      int
	bashCallDedup      map[string]struct{}
	seedToolDedup      map[string]struct{}
	toolDedupCount     int
	toolDedupKeys      map[string]int
	introDedup         map[string]struct{}

	// Throttling
	lastScanTime time.Time

	// Callbacks
	onConversationID func(string) // 濠电姷鏁搁崑鐐哄垂閸洖绠伴柟闂寸劍閺呮繈鏌曟径鍡樻珕闁稿顦甸弻銈囩矙鐠恒劋绮垫繛瀛樺殠閸婃繈寮婚敓鐘茬＜婵炴垶锕╅崵瀣磽娴ｆ彃浜鹃梺?conversationID 闂傚倸鍊风粈渚€骞栭锕€鐤柛鎰ゴ閺嬫牗绻涢幋鐐╂（婵炲樊浜滈崘鈧銈嗗姧缁蹭粙顢?
	// Logger
	logger *debug.Logger
}

func newStreamHandler(
	cfg *config.Config,
	w http.ResponseWriter,
	logger *debug.Logger,
	suppressThinking bool,
	isStream bool,
	responseFormat adapter.ResponseFormat,
	workdir string,
) *streamHandler {
	var flusher http.Flusher
	if isStream {
		if f, ok := w.(http.Flusher); ok {
			flusher = f
		}
	}

	outputTokenMode := strings.ToLower(strings.TrimSpace(cfg.OutputTokenMode))
	if outputTokenMode == "" {
		outputTokenMode = "final"
	}

	h := &streamHandler{
		config:           cfg,
		workdir:          workdir,
		w:                w,
		flusher:          flusher,
		isStream:         isStream,
		logger:           logger,
		suppressThinking: suppressThinking,
		outputTokenMode:  outputTokenMode,
		responseFormat:   responseFormat,

		blockIndex:               -1,
		toolBlocks:               make(map[string]int),
		responseText:             perf.AcquireStringBuilder(),
		writeChunkBuffer:         perf.AcquireStringBuilder(),
		textBlockBuilders:        make(map[int]*strings.Builder),
		thinkingBlockBuilders:    make(map[int]*strings.Builder),
		thinkingBlockSigs:        make(map[int]string),
		toolInputNames:           make(map[string]string),
		toolInputBuffers:         make(map[string]*strings.Builder),
		toolInputHadDelta:        make(map[string]bool),
		toolCallHandled:          make(map[string]bool),
		toolCallEmitted:          make(map[string]struct{}),
		bashCallDedup:            make(map[string]struct{}),
		seedToolDedup:            make(map[string]struct{}),
		toolDedupKeys:            make(map[string]int),
		introDedup:               make(map[string]struct{}),
		msgID:                    fmt.Sprintf("msg_%d", time.Now().UnixMilli()),
		startTime:                time.Now(),
		currentTextIndex:         -1,
		activeThinkingBlockIndex: -1,
		activeThinkingSSEIndex:   -1,
		activeTextBlockIndex:     -1,
		activeTextSSEIndex:       -1,
		activeBlockType:          "",
		openAIChunkScratch:       make([]byte, 0, 512),
		ssePayloadScratch:        make([]byte, 0, 512),
	}
	return h
}

func (h *streamHandler) release() {
	perf.ReleaseStringBuilder(h.responseText)
	perf.ReleaseStringBuilder(h.writeChunkBuffer)
	for _, sb := range h.textBlockBuilders {
		perf.ReleaseStringBuilder(sb)
	}
	for _, sb := range h.thinkingBlockBuilders {
		perf.ReleaseStringBuilder(sb)
	}
	for _, sb := range h.toolInputBuffers {
		perf.ReleaseStringBuilder(sb)
	}
}

func (h *streamHandler) writeSSE(event, data string) {
	if !h.isStream {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hasReturn {
		return
	}
	if h.responseFormat == adapter.FormatOpenAI {
		written, err := h.writeOpenAISSE(event, data)
		if err != nil {
			h.markWriteErrorLocked(event, err)
			return
		}
		if written {
			h.flushSSELocked(event, data, false)
		}
		return
	}

	if err := writeSSEFrame(h.w, event, data); err != nil {
		h.markWriteErrorLocked(event, err)
		return
	}
	h.flushSSELocked(event, data, false)

	if h.config != nil && h.config.DebugEnabled && h.config.DebugLogSSE {
		h.logger.LogOutputSSE(event, data)
	}
}

func (h *streamHandler) writeSSEBytes(event string, data []byte) {
	if !h.isStream {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hasReturn {
		return
	}
	if h.responseFormat == adapter.FormatOpenAI {
		written, err := h.writeOpenAISSEBytes(event, data)
		if err != nil {
			h.markWriteErrorLocked(event, err)
			return
		}
		if written {
			h.flushSSEBytesLocked(event, data, false)
		}
		return
	}

	if err := writeSSEFrameBytes(h.w, event, data); err != nil {
		h.markWriteErrorLocked(event, err)
		return
	}
	h.flushSSEBytesLocked(event, data, false)
	if h.config != nil && h.config.DebugEnabled && h.config.DebugLogSSE {
		h.logger.LogOutputSSE(event, string(data))
	}
}

func (h *streamHandler) writeOpenAISSE(event, data string) (bool, error) {
	return h.writeOpenAISSEBytes(event, []byte(data))
}
func (h *streamHandler) writeOpenAISSEBytes(event string, data []byte) (bool, error) {
	raw, ok := adapter.AppendOpenAIChunk(h.openAIChunkScratch[:0], h.msgID, h.startTime.Unix(), event, data)
	if !ok {
		return false, nil
	}
	h.openAIChunkScratch = raw[:0]
	if err := writeOpenAIFrame(h.w, raw); err != nil {
		return false, err
	}
	return true, nil
}

func (h *streamHandler) writeFinalSSE(event, data string) {
	if !h.isStream {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.responseFormat == adapter.FormatOpenAI {
		written, err := h.writeOpenAISSE(event, data)
		if err != nil {
			h.markWriteErrorLocked(event, err)
			return
		}
		if written {
			h.flushSSELocked(event, data, true)
		}
		// Send [DONE] at the very end
		if event == "message_stop" {
			if _, err := h.w.Write(sseDoneLineBytes); err != nil {
				h.markWriteErrorLocked(event, err)
				return
			}
			h.flushSSELocked(event, sseDoneLine, true)
		}
		return
	}

	if err := writeSSEFrame(h.w, event, data); err != nil {
		h.markWriteErrorLocked(event, err)
		return
	}
	h.flushSSELocked(event, data, true)

	if h.config != nil && h.config.DebugEnabled && h.config.DebugLogSSE {
		h.logger.LogOutputSSE(event, data)
	}
}

func (h *streamHandler) writeFinalSSEBytes(event string, data []byte) {
	if !h.isStream {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writeFinalSSEBytesLocked(event, data)
}

func (h *streamHandler) writeFinalSSEBytesLocked(event string, data []byte) {
	h.writeFinalSSEBytesLockedWithHint(event, data, false)
}

func (h *streamHandler) writeFinalSSEBytesLockedWithHint(event string, data []byte, immediate bool) {
	if !h.isStream {
		return
	}

	if h.responseFormat == adapter.FormatOpenAI {
		written, err := h.writeOpenAISSEBytes(event, data)
		if err != nil {
			h.markWriteErrorLocked(event, err)
			return
		}
		if written {
			h.flushSSEBytesLockedWithHint(event, len(data), immediate, true)
		}
		if event == "message_stop" {
			if _, err := h.w.Write(sseDoneLineBytes); err != nil {
				h.markWriteErrorLocked(event, err)
				return
			}
			h.flushSSELocked(event, sseDoneLine, true)
		}
		return
	}

	if err := writeSSEFrameBytes(h.w, event, data); err != nil {
		h.markWriteErrorLocked(event, err)
		return
	}
	h.flushSSEBytesLockedWithHint(event, len(data), immediate, true)
	if h.config != nil && h.config.DebugEnabled && h.config.DebugLogSSE {
		h.logger.LogOutputSSE(event, string(data))
	}
}

func (h *streamHandler) writeSSEBytesLockedWithHint(event string, data []byte, immediate bool) {
	if !h.isStream {
		return
	}
	if h.hasReturn {
		return
	}
	if h.responseFormat == adapter.FormatOpenAI {
		written, err := h.writeOpenAISSEBytes(event, data)
		if err != nil {
			h.markWriteErrorLocked(event, err)
			return
		}
		if written {
			h.flushSSEBytesLockedWithHint(event, len(data), immediate, false)
		}
		return
	}
	if err := writeSSEFrameBytes(h.w, event, data); err != nil {
		h.markWriteErrorLocked(event, err)
		return
	}
	h.flushSSEBytesLockedWithHint(event, len(data), immediate, false)
	if h.config != nil && h.config.DebugEnabled && h.config.DebugLogSSE {
		h.logger.LogOutputSSE(event, string(data))
	}
	if h.config != nil && h.config.DebugEnabled {
		slog.Debug("SSE Out", "event", event, "data_len", len(data))
	}
}

func (h *streamHandler) writeSSEContentBlockStartToolUseLocked(index int, id, name string, final bool) {
	raw, err := appendSSEContentBlockStartToolUse(h.ssePayloadScratch[:0], index, id, name)
	if err != nil {
		h.markWriteErrorLocked("content_block_start", err)
		return
	}
	h.ssePayloadScratch = raw[:0]
	if final {
		h.writeFinalSSEBytesLockedWithHint("content_block_start", raw, true)
		return
	}
	h.writeSSEBytesLockedWithHint("content_block_start", raw, true)
}

func (h *streamHandler) writeSSEContentBlockStartToolUse(index int, id, name string, final bool) {
	if !h.isStream {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writeSSEContentBlockStartToolUseLocked(index, id, name, final)
}

func (h *streamHandler) writeSSEContentBlockStartTextLocked(index int, final bool) {
	raw, err := appendSSEContentBlockStartText(h.ssePayloadScratch[:0], index)
	if err != nil {
		h.markWriteErrorLocked("content_block_start", err)
		return
	}
	h.ssePayloadScratch = raw[:0]
	if final {
		h.writeFinalSSEBytesLockedWithHint("content_block_start", raw, true)
		return
	}
	h.writeSSEBytesLockedWithHint("content_block_start", raw, true)
}

func (h *streamHandler) writeSSEContentBlockStartText(index int, final bool) {
	if !h.isStream {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writeSSEContentBlockStartTextLocked(index, final)
}

func (h *streamHandler) writeSSEContentBlockDeltaInputJSONLocked(index int, partialJSON string, final bool) {
	raw, err := appendSSEContentBlockDeltaInputJSON(h.ssePayloadScratch[:0], index, partialJSON)
	if err != nil {
		h.markWriteErrorLocked("content_block_delta", err)
		return
	}
	h.ssePayloadScratch = raw[:0]
	if final {
		h.writeFinalSSEBytesLockedWithHint("content_block_delta", raw, false)
		return
	}
	h.writeSSEBytesLockedWithHint("content_block_delta", raw, false)
}

func (h *streamHandler) writeSSEContentBlockDeltaInputJSON(index int, partialJSON string, final bool) {
	if !h.isStream {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writeSSEContentBlockDeltaInputJSONLocked(index, partialJSON, final)
}

func (h *streamHandler) writeSSEContentBlockDeltaTextLocked(index int, text string, final bool) {
	raw, err := appendSSEContentBlockDeltaText(h.ssePayloadScratch[:0], index, text)
	if err != nil {
		h.markWriteErrorLocked("content_block_delta", err)
		return
	}
	h.ssePayloadScratch = raw[:0]
	if final {
		h.writeFinalSSEBytesLockedWithHint("content_block_delta", raw, true)
		return
	}
	h.writeSSEBytesLockedWithHint("content_block_delta", raw, true)
}

func (h *streamHandler) writeSSEContentBlockDeltaText(index int, text string, final bool) {
	if !h.isStream {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writeSSEContentBlockDeltaTextLocked(index, text, final)
}

func (h *streamHandler) writeSSEContentBlockDeltaThinkingLocked(index int, thinking string, final bool) {
	raw, err := appendSSEContentBlockDeltaThinking(h.ssePayloadScratch[:0], index, thinking)
	if err != nil {
		h.markWriteErrorLocked("content_block_delta", err)
		return
	}
	h.ssePayloadScratch = raw[:0]
	if final {
		h.writeFinalSSEBytesLockedWithHint("content_block_delta", raw, false)
		return
	}
	h.writeSSEBytesLockedWithHint("content_block_delta", raw, false)
}

func (h *streamHandler) writeSSEContentBlockDeltaThinking(index int, thinking string, final bool) {
	if !h.isStream {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writeSSEContentBlockDeltaThinkingLocked(index, thinking, final)
}

func (h *streamHandler) writeSSEContentBlockStopLocked(index int, final bool) {
	raw, err := appendSSEContentBlockStop(h.ssePayloadScratch[:0], index)
	if err != nil {
		h.markWriteErrorLocked("content_block_stop", err)
		return
	}
	h.ssePayloadScratch = raw[:0]
	if final {
		h.writeFinalSSEBytesLockedWithHint("content_block_stop", raw, true)
		return
	}
	h.writeSSEBytesLockedWithHint("content_block_stop", raw, true)
}

func (h *streamHandler) writeSSEContentBlockStop(index int, final bool) {
	if !h.isStream {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writeSSEContentBlockStopLocked(index, final)
}

func (h *streamHandler) writeSSEMessageDeltaLocked(stopReason string, outputTokens int, final bool) {
	raw, err := appendSSEMessageDelta(h.ssePayloadScratch[:0], stopReason, outputTokens)
	if err != nil {
		h.markWriteErrorLocked("message_delta", err)
		return
	}
	h.ssePayloadScratch = raw[:0]
	if final {
		h.writeFinalSSEBytesLockedWithHint("message_delta", raw, true)
		return
	}
	h.writeSSEBytesLockedWithHint("message_delta", raw, true)
}

func (h *streamHandler) writeSSEMessageDelta(stopReason string, outputTokens int, final bool) {
	if !h.isStream {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writeSSEMessageDeltaLocked(stopReason, outputTokens, final)
}

func (h *streamHandler) writeSSEMessageStart(model string, inputTokens, outputTokens int) {
	if !h.isStream {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	raw, err := appendSSEMessageStart(h.ssePayloadScratch[:0], h.msgID, model, inputTokens, outputTokens)
	if err != nil {
		h.markWriteErrorLocked("message_start", err)
		return
	}
	h.ssePayloadScratch = raw[:0]
	h.writeSSEBytesLockedWithHint("message_start", raw, true)
}

func (h *streamHandler) writeKeepAlive() {
	if !h.isStream {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hasReturn {
		return
	}
	if _, err := h.w.Write(sseKeepAliveBytes); err != nil {
		h.markWriteErrorLocked("keep-alive", err)
		return
	}
	h.flushSSELocked("keep-alive", sseKeepAlive, true)
}

func (h *streamHandler) addOutputTokens(text string) {
	if text == "" {
		return
	}
	h.outputMu.Lock()
	if !h.useUpstreamUsage {
		h.outputEstimator.Add(text)
	}
	h.outputMu.Unlock()
}

func (h *streamHandler) finalizeOutputTokens() {
	h.outputMu.Lock()
	defer h.outputMu.Unlock()

	if h.useUpstreamUsage {
		return
	}
	h.outputTokens = h.outputEstimator.Count()
}

func (h *streamHandler) setUsageTokens(input, output int) {
	h.outputMu.Lock()
	if input >= 0 {
		h.inputTokens = input
	}
	if output >= 0 {
		h.outputTokens = output
		h.useUpstreamUsage = true
	}
	h.outputMu.Unlock()
}

func (h *streamHandler) resetRoundState() {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Ensure any currently open block is closed before resetting state
	h.closeActiveBlockLocked()

	// 濠电姷鏁搁崑鐐哄垂閸洖绠伴柛婵勫劤閻捇鏌ｉ幋婵愭綗闁逞屽墮閹虫﹢寮崘顔肩＜婵﹩鍘肩粊鍫曟⒒娓氣偓濞佳団€﹂崼銉ョ閹艰揪绲挎稉宥囨喐閻楀牆绗氶柍閿嬪灴閺岀喓绱掑Ο铏诡儌闂佺粯甯楅幃鍌炲蓟濞戙垺鏅查柛娑卞枟閸犳劗绱?h.blockIndex闂?	// 濠电姷鏁搁崕鎴犲緤閽樺娲晜閻愵剙搴婇梺鍛婃处閸ㄦ澘效閺屻儲鐓冪憸婊堝礈濞戞碍顫曢柟鐑樻尵閻熷綊鏌涢…鎴濇灓濞寸姾鍋愮槐鎾存媴閻熼偊鏆㈤梺鍝勬噽婵炩偓鐎殿喖顭峰畷銊╁级閹寸媭鍞洪梻浣筋潐閹矂宕㈤挊澶樼唵闁哄啫鐗婇埛鎴︽煕濞戞﹫鍔熼柟铏姍閺屾盯濡搁妸銉у帿闁诲酣娼ч妶鎼佸春閿熺姴宸濇い鎾跺濡差垶鏌ｆ惔锛勭暛闁稿酣浜惰棟妞ゆ牗鍩冮弸宥夋煏韫囧鈧牠宕戦敐澶嬬厱闁靛绲芥俊鐣岀磼閳ь剟宕橀埡鈧换鍡涙煟閹邦厼绲婚柍褜鍓濋褍宓勯梺鍦濠㈡﹢锝為崨瀛樼厽婵☆垰鍚嬮弳鈺呮煃鐟欏嫮娲存慨濠冩そ楠炴牠鎮欓幓鎺懶戦梻浣侯焾椤戝洭宕伴幘璇茬闁圭儤顨忛弫鍐煥閺冨洤袚婵炲懏鐗犻弻锝堢疀閺囩偘鎴烽梺鐑╁墲濡啫鐣烽悽绋课у璺侯儑閸橀箖姊绘担鍝ヤ虎妞ゆ垵妫涚槐鐐哄箣閻愵亙绨婚梺瑙勫劤绾绢厾绮旈悜姗嗘闁绘劕妯婇崕鎰亜閿旀儳顣奸柟顖涙椤㈡瑩鎳￠妶鍥风闯闂傚倸鍊烽懗鍫曘€佹繝鍕濞村吋娼欑壕鍧楁煟閵忋埄鐒鹃柡?"Mismatched content block type"闂?
	h.activeThinkingBlockIndex = -1
	h.activeThinkingSSEIndex = -1
	h.activeTextBlockIndex = -1
	h.activeTextSSEIndex = -1
	h.activeBlockType = ""
	h.hasReturn = false

	clear(h.toolBlocks)
	h.responseText.Reset()
	h.contentBlocks = nil
	h.currentTextIndex = -1

	for _, sb := range h.textBlockBuilders {
		perf.ReleaseStringBuilder(sb)
	}
	clear(h.textBlockBuilders)

	for _, sb := range h.thinkingBlockBuilders {
		perf.ReleaseStringBuilder(sb)
	}
	clear(h.thinkingBlockBuilders)

	h.pendingToolCalls = nil
	clear(h.toolInputNames)

	for _, sb := range h.toolInputBuffers {
		perf.ReleaseStringBuilder(sb)
	}
	clear(h.toolInputBuffers)

	clear(h.toolInputHadDelta)
	clear(h.toolCallHandled)
	clear(h.toolCallEmitted)
	clear(h.bashCallDedup)
	for key := range h.seedToolDedup {
		h.bashCallDedup[key] = struct{}{}
	}
	h.toolDedupCount = 0
	clear(h.toolDedupKeys)
	h.currentToolInputID = ""
	h.toolCallCount = 0
	h.outputTokens = 0
	h.outputEstimator.Reset()
	h.writeChunkBuffer.Reset()
	h.useUpstreamUsage = false
	h.finalStopReason = ""
	h.hasTextOutput = false
	h.lastTextDelta = ""
	h.lastTextDeltaSource = ""
	h.lastTextDeltaAt = time.Time{}
	h.deferredFlushFrames = 0
	h.deferredFlushBytes = 0
}

func (h *streamHandler) shouldEmitToolCalls(stopReason string) bool {
	return true
}

// seedSideEffectDedupFromMessages pre-seeds dedup keys from prior assistant tool_use blocks.
func (h *streamHandler) seedSideEffectDedupFromMessages(messages []prompt.Message) {
	if len(messages) == 0 {
		return
	}
	lastUserTextIdx := -1
	for i, msg := range messages {
		if strings.ToLower(strings.TrimSpace(msg.Role)) != "user" {
			continue
		}
		if strings.TrimSpace(msg.ExtractText()) != "" {
			lastUserTextIdx = i
		}
	}
	if lastUserTextIdx < 0 {
		return
	}

	for i, msg := range messages {
		if i <= lastUserTextIdx || strings.ToLower(strings.TrimSpace(msg.Role)) != "assistant" {
			continue
		}
		for _, block := range msg.Content.GetBlocks() {
			if block.Type != "tool_use" {
				continue
			}
			nameKey := strings.ToLower(strings.TrimSpace(block.Name))
			if nameKey == "" {
				continue
			}
			input := strings.TrimSpace(stringifyToolInput(block.Input))
			if input == "" {
				input = "{}"
			}
			key := sideEffectToolDedupKey(nameKey, input)
			if key == "" {
				continue
			}
			h.seedToolDedup[key] = struct{}{}
			h.bashCallDedup[key] = struct{}{}
		}
	}
}

func (h *streamHandler) writeUpstreamEventSSE(msg upstream.SSEMessage) {
	if !h.isStream {
		return
	}
	payload, err := marshalEventPayloadBytes(msg)
	if err != nil {
		return
	}
	h.writeSSEBytes(msg.Type, payload)
}

func stringifyToolInput(input interface{}) string {
	switch v := input.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(raw)
	}
}

// sanitizeToolInput normalizes upstream tool input for Claude Code compatibility.
// It drops or maps fields known to cause local tool validation failures.
func sanitizeToolInput(name, input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return input
	}

	nameKey := strings.ToLower(strings.TrimSpace(name))
	switch nameKey {
	case "write", "edit", "read", "bash", "glob":
	default:
		return input
	}

	switch nameKey {
	case "write":
		if !strings.Contains(trimmed, `"path"`) && !strings.Contains(trimmed, `"overwrite"`) {
			return input
		}
	case "edit", "read":
		if !strings.Contains(trimmed, `"path"`) {
			return input
		}
	case "bash":
		if !strings.Contains(trimmed, `"cmd"`) {
			return input
		}
	case "glob":
		if !strings.Contains(trimmed, `"path"`) || strings.Contains(trimmed, `"pattern"`) {
			return input
		}
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return input
	}

	changed := false
	mapField := func(from, to string) {
		v, ok := payload[from]
		if !ok {
			return
		}
		if _, exists := payload[to]; !exists {
			payload[to] = v
			changed = true
		}
		delete(payload, from)
		changed = true
	}

	switch nameKey {
	case "write":
		// Claude Code Write tool rejects unknown field "overwrite".
		if _, ok := payload["overwrite"]; ok {
			delete(payload, "overwrite")
			changed = true
		}
		mapField("path", "file_path")
	case "edit":
		mapField("path", "file_path")
	case "read":
		mapField("path", "file_path")
	case "bash":
		mapField("cmd", "command")
	case "glob":
		if _, ok := payload["pattern"]; !ok {
			if path, ok := payload["path"].(string); ok && strings.TrimSpace(path) != "" {
				payload["pattern"] = "*"
				changed = true
			}
		}
	}

	if !changed {
		return input
	}

	normalized, err := json.Marshal(payload)
	if err != nil {
		return input
	}
	return string(normalized)
}

func normalizeUpstreamToolName(name string) string {
	mapped := orchids.NormalizeToolName(name)
	if strings.TrimSpace(mapped) == "" {
		return name
	}
	return mapped
}

func (h *streamHandler) emitToolCallNonStream(call toolCall) {
	h.addOutputTokens(call.name)
	h.addOutputTokens(call.input)
	inputJSON := strings.TrimSpace(call.input)
	if inputJSON == "" {
		inputJSON = "{}"
	}
	var inputValue interface{}
	if err := json.Unmarshal([]byte(inputJSON), &inputValue); err != nil {
		inputValue = map[string]interface{}{}
	}
	h.contentBlocks = append(h.contentBlocks, map[string]interface{}{
		"type":  "tool_use",
		"id":    call.id,
		"name":  call.name,
		"input": inputValue,
	})
}

func (h *streamHandler) emitToolCallStream(call toolCall, idx int, final bool) {
	if call.id == "" {
		return
	}

	h.addOutputTokens(call.name)
	h.addOutputTokens(call.input)
	inputJSON := strings.TrimSpace(call.input)
	if inputJSON == "" {
		inputJSON = "{}"
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if idx < 0 {
		h.blockIndex++
		idx = h.blockIndex
	}
	h.writeSSEContentBlockStartToolUseLocked(idx, call.id, call.name, final)
	h.writeSSEContentBlockDeltaInputJSONLocked(idx, inputJSON, final)
	h.writeSSEContentBlockStopLocked(idx, final)
}

// emitToolUseFromInput emits a single tool_use block once the full input is available.
func (h *streamHandler) emitToolUseFromInput(toolID, toolName, inputStr string) {
	if toolID == "" || toolName == "" {
		return
	}
	if _, ok := h.toolCallEmitted[toolID]; ok {
		return
	}
	h.toolCallEmitted[toolID] = struct{}{}

	h.addOutputTokens(toolName)
	inputJSON := strings.TrimSpace(inputStr)
	if inputJSON == "" {
		inputJSON = "{}"
	}

	h.mu.Lock()
	h.toolCallCount++
	h.blockIndex++
	idx := h.blockIndex
	h.writeSSEContentBlockStartToolUseLocked(idx, toolID, toolName, false)
	h.writeSSEContentBlockDeltaInputJSONLocked(idx, inputJSON, false)
	h.writeSSEContentBlockStopLocked(idx, false)
	h.mu.Unlock()
}

func (h *streamHandler) flushPendingToolCalls(stopReason string) {
	if !h.shouldEmitToolCalls(stopReason) {
		return
	}

	h.mu.Lock()
	calls := make([]toolCall, len(h.pendingToolCalls))
	copy(calls, h.pendingToolCalls)
	h.pendingToolCalls = nil
	h.mu.Unlock()

	for _, call := range calls {
		if h.isStream {
			h.emitToolCallStream(call, -1, true)
		} else {
			h.emitToolCallNonStream(call)
		}
	}
}

func (h *streamHandler) finishResponse(stopReason string) {
	if stopReason == "tool_use" {
		h.mu.Lock()
		hasToolCalls := h.toolCallCount > 0 ||
			len(h.pendingToolCalls) > 0 ||
			len(h.toolCallEmitted) > 0
		h.mu.Unlock()
		if !hasToolCalls {
			stopReason = "end_turn"
		}
	}
	h.mu.Lock()
	if h.hasReturn {
		h.mu.Unlock()
		return
	}
	h.hasReturn = true
	h.finalStopReason = stopReason
	h.mu.Unlock()

	if h.isStream {
		var blockStopData []byte
		h.mu.Lock()
		if stopData, ok := h.popActiveBlockStopDataLocked(); ok {
			blockStopData = stopData
		}
		h.mu.Unlock()
		if len(blockStopData) > 0 {
			h.writeFinalSSEBytes("content_block_stop", blockStopData)
		}
		if stopReason != "tool_use" {
			h.emitWriteChunkFallbackIfNeeded()
		}
		h.flushPendingToolCalls(stopReason)
		h.finalizeOutputTokens()
		h.mu.Lock()
		h.writeSSEMessageDeltaLocked(stopReason, h.outputTokens, true)
		h.mu.Unlock()

		stopData, err := marshalSSEMessageStopBytes()
		if err != nil {
			slog.Error("Failed to marshal message_stop", "error", err)
		} else {
			h.writeFinalSSEBytes("message_stop", stopData)
		}
	} else {
		if stopReason != "tool_use" {
			h.emitWriteChunkFallbackIfNeeded()
		}
		h.flushPendingToolCalls(stopReason)
		h.finalizeOutputTokens()
	}

	// 闂傚倷娴囧畷鍨叏閹惰姤鍊块柨鏇楀亾妞ゎ厼鐏濊灒闁兼祴鏅濋ˇ顖炴倵楠炲灝鍔氭い锔诲灣缁鎮滃Ο鍦畾濡炪倖鐗楁笟妤呭磿閵夛妇绠?
	h.mu.Lock()
	suppressedDedup := h.toolDedupCount
	dedupKeys := make(map[string]int, len(h.toolDedupKeys))
	for k, v := range h.toolDedupKeys {
		dedupKeys[k] = v
	}
	h.mu.Unlock()
	if suppressedDedup > 0 {
		slog.Info("tool call dedup summary", "suppressed_count", suppressedDedup, "dedup_keys", dedupKeys)
	}
	h.logger.LogSummary(h.inputTokens, h.outputTokens, time.Since(h.startTime), stopReason)
	slog.Debug("Request completed", "input_tokens", h.inputTokens, "output_tokens", h.outputTokens, "duration", time.Since(h.startTime))
}

func (h *streamHandler) ensureBlock(blockType string) int {
	if blockType == "thinking" && h.suppressThinking {
		return -1
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	// If already in a block of a different type, close it
	if h.activeBlockType != "" && h.activeBlockType != blockType {
		h.closeActiveBlockLocked()
	}

	// If already in the correct block type, return current index
	if h.activeBlockType == blockType {
		if blockType == "thinking" {
			return h.activeThinkingSSEIndex
		}
		if blockType == "text" {
			return h.activeTextSSEIndex
		}
	}

	// Start new block
	h.blockIndex++
	sseIdx := h.blockIndex
	h.activeBlockType = blockType

	switch blockType {
	case "thinking":
		signature := h.pendingThinkingSig
		h.pendingThinkingSig = ""
		h.contentBlocks = append(h.contentBlocks, map[string]interface{}{
			"type":      "thinking",
			"signature": signature,
		})
		internalIdx := len(h.contentBlocks) - 1
		h.activeThinkingBlockIndex = internalIdx
		h.activeThinkingSSEIndex = sseIdx
		h.thinkingBlockBuilders[internalIdx] = perf.AcquireStringBuilder()
		h.thinkingBlockSigs[internalIdx] = signature

		raw, err := appendSSEContentBlockStartThinking(h.ssePayloadScratch[:0], sseIdx, signature)
		if err != nil {
			h.markWriteErrorLocked("content_block_start", err)
			break
		}
		h.ssePayloadScratch = raw[:0]
		h.writeSSEBytesLockedWithHint("content_block_start", raw, true)
	case "text":
		h.contentBlocks = append(h.contentBlocks, map[string]interface{}{
			"type": "text",
		})
		internalIdx := len(h.contentBlocks) - 1
		h.activeTextBlockIndex = internalIdx
		h.activeTextSSEIndex = sseIdx
		h.textBlockBuilders[internalIdx] = perf.AcquireStringBuilder()

		h.writeSSEContentBlockStartTextLocked(sseIdx, false)
	}

	return sseIdx
}

func (h *streamHandler) closeActiveBlock() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeActiveBlockLocked()
}

func (h *streamHandler) popActiveBlockStopDataLocked() ([]byte, bool) {
	if h.activeBlockType == "" {
		return nil, false
	}

	var sseIdx int
	switch h.activeBlockType {
	case "thinking":
		sseIdx = h.activeThinkingSSEIndex
		h.activeThinkingBlockIndex = -1
		h.activeThinkingSSEIndex = -1
	case "text":
		sseIdx = h.activeTextSSEIndex
		h.activeTextBlockIndex = -1
		h.activeTextSSEIndex = -1
	default:
		// tool_use and others are usually handled as single-event blocks or managed separately
		h.activeBlockType = ""
		return nil, false
	}

	h.activeBlockType = ""

	stopData, err := marshalSSEContentBlockStopBytes(sseIdx)
	if err != nil {
		slog.Error("Failed to marshal content_block_stop", "error", err)
	}
	if err != nil {
		return nil, false
	}
	return stopData, true
}

func (h *streamHandler) closeActiveBlockLocked() {
	stopData, ok := h.popActiveBlockStopDataLocked()
	if !ok {
		return
	}
	h.writeSSEBytesLocked("content_block_stop", stopData)
}

func (h *streamHandler) writeSSELocked(event, data string) {
	if !h.isStream {
		return
	}
	if h.hasReturn {
		return
	}
	if h.responseFormat == adapter.FormatOpenAI {
		written, err := h.writeOpenAISSE(event, data)
		if err != nil {
			h.markWriteErrorLocked(event, err)
			return
		}
		if written {
			h.flushSSELocked(event, data, false)
		}
		return
	}
	if err := writeSSEFrame(h.w, event, data); err != nil {
		h.markWriteErrorLocked(event, err)
		return
	}
	h.flushSSELocked(event, data, false)
	if h.config != nil && h.config.DebugEnabled && h.config.DebugLogSSE {
		h.logger.LogOutputSSE(event, data)
	}
	// Log to slog only when debug enabled
	if h.config != nil && h.config.DebugEnabled {
		slog.Debug("SSE Out", "event", event, "data_len", len(data))
	}
}

func (h *streamHandler) writeSSEBytesLocked(event string, data []byte) {
	if !h.isStream {
		return
	}
	if h.hasReturn {
		return
	}
	if h.responseFormat == adapter.FormatOpenAI {
		written, err := h.writeOpenAISSEBytes(event, data)
		if err != nil {
			h.markWriteErrorLocked(event, err)
			return
		}
		if written {
			h.flushSSEBytesLocked(event, data, false)
		}
		return
	}
	if err := writeSSEFrameBytes(h.w, event, data); err != nil {
		h.markWriteErrorLocked(event, err)
		return
	}
	h.flushSSEBytesLocked(event, data, false)
	if h.config != nil && h.config.DebugEnabled && h.config.DebugLogSSE {
		h.logger.LogOutputSSE(event, string(data))
	}
	// Log to slog only when debug enabled
	if h.config != nil && h.config.DebugEnabled {
		slog.Debug("SSE Out", "event", event, "data_len", len(data))
	}
}

// Event Handlers

func (h *streamHandler) emitTextBlock(text string) {
	h.emitTextBlockWithMode(text, false)
}

func (h *streamHandler) emitTextBlockWithMode(text string, final bool) {
	if !h.isStream || text == "" {
		return
	}
	h.mu.Lock()
	h.hasTextOutput = true
	h.blockIndex++
	idx := h.blockIndex
	h.writeSSEContentBlockStartTextLocked(idx, final)
	h.writeSSEContentBlockDeltaTextLocked(idx, text, final)
	h.writeSSEContentBlockStopLocked(idx, final)
	h.mu.Unlock()
}

func (h *streamHandler) markTextOutput() {
	h.mu.Lock()
	h.hasTextOutput = true
	h.mu.Unlock()
}

func (h *streamHandler) emitWriteChunkFallbackIfNeeded() {
	if h.writeChunkBuffer == nil {
		return
	}

	h.mu.Lock()
	if h.hasTextOutput || h.writeChunkBuffer.Len() == 0 {
		h.mu.Unlock()
		return
	}
	text := h.writeChunkBuffer.String()
	h.hasTextOutput = true
	h.mu.Unlock()

	if h.isStream {
		h.emitTextBlockWithMode(text, true)
		return
	}

	h.mu.Lock()
	h.contentBlocks = append(h.contentBlocks, map[string]interface{}{
		"type": "text",
		"text": text,
	})
	h.mu.Unlock()
}

func (h *streamHandler) handleToolCallAfterChecks(call toolCall) {
	h.mu.Lock()
	h.pendingToolCalls = append(h.pendingToolCalls, call)
	h.toolCallCount++
	h.mu.Unlock()
}

func (h *streamHandler) shouldAcceptToolCall(call toolCall) bool {
	_, key, ok := evaluateToolCallInput(call.name, call.input)
	if !ok {
		if h.config != nil && h.config.DebugEnabled {
			slog.Debug("invalid tool call suppressed", "tool", call.name, "input", call.input)
		}
		return false
	}
	if key != "" {
		maskedKey := maskDedupKey(key)
		h.mu.Lock()
		if _, ok := h.bashCallDedup[key]; ok {
			h.toolDedupCount++
			h.toolDedupKeys[maskedKey]++
			suppressed := h.toolDedupCount
			h.mu.Unlock()
			if h.config != nil && h.config.DebugEnabled {
				slog.Debug("duplicate mutating tool call suppressed", "tool", call.name, "dedup_key", maskedKey, "suppressed_total", suppressed)
			}
			return false
		}
		h.bashCallDedup[key] = struct{}{}
		h.seedToolDedup[key] = struct{}{}
		h.mu.Unlock()
	}
	return true
}

func maskDedupKey(key string) string {
	tool := key
	if idx := strings.IndexByte(tool, ':'); idx > 0 {
		tool = tool[:idx]
	}
	sum := fnv1a64String(key)
	out := make([]byte, 0, len(tool)+1+16)
	out = append(out, tool...)
	out = append(out, '#')
	out = strconv.AppendUint(out, sum, 16)
	return string(out)
}

func sideEffectToolDedupKey(name, input string) string {
	nameKey := normalizeToolNameKey(name)
	if !isSideEffectToolName(nameKey) {
		return ""
	}
	fields, ok := decodeToolInputFields(input)
	if !ok {
		return ""
	}
	return sideEffectToolDedupKeyFromFields(nameKey, fields)
}

func fallbackToolCallID(toolName, input string) string {
	nameKey := strings.ToLower(strings.TrimSpace(toolName))
	if nameKey == "" {
		return ""
	}
	normalizedInput := strings.TrimSpace(input)
	if normalizedInput == "" {
		normalizedInput = "{}"
	}
	sum := fnv1a64Pair(nameKey, normalizedInput)
	out := make([]byte, 0, len("tool_anon_")+16)
	out = append(out, "tool_anon_"...)
	out = strconv.AppendUint(out, sum, 16)
	return string(out)
}

func hasRequiredToolInput(name, input string) bool {
	nameKey := normalizeToolNameKey(name)
	if nameKey == "" {
		return false
	}
	if !isStructuredToolName(nameKey) {
		return true
	}
	fields, ok := decodeToolInputFields(input)
	if !ok {
		return false
	}
	return hasRequiredToolInputFields(nameKey, fields)
}

func evaluateToolCallInput(name, input string) (nameKey string, dedupKey string, ok bool) {
	nameKey = normalizeToolNameKey(name)
	if nameKey == "" {
		return "", "", false
	}
	if !isStructuredToolName(nameKey) {
		return nameKey, "", true
	}
	fields, parsed := decodeToolInputFields(input)
	if !parsed {
		return nameKey, "", false
	}
	if !hasRequiredToolInputFields(nameKey, fields) {
		return nameKey, "", false
	}
	return nameKey, sideEffectToolDedupKeyFromFields(nameKey, fields), true
}

func normalizeToolNameKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func isStructuredToolName(nameKey string) bool {
	switch nameKey {
	case "edit", "write", "bash", "read", "glob", "grep":
		return true
	default:
		return false
	}
}

func isSideEffectToolName(nameKey string) bool {
	switch nameKey {
	case "bash", "write", "edit":
		return true
	default:
		return false
	}
}

type toolInputFields struct {
	Command  string          `json:"command"`
	Cmd      string          `json:"cmd"`
	FilePath string          `json:"file_path"`
	Path     string          `json:"path"`
	Content  json.RawMessage `json:"content"`
	Old      json.RawMessage `json:"old_string"`
	New      json.RawMessage `json:"new_string"`
}

func decodeToolInputFields(input string) (toolInputFields, bool) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		raw = "{}"
	}
	var fields toolInputFields
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return toolInputFields{}, false
	}
	return fields, true
}

func resolveToolPath(filePath, path string) string {
	if s := strings.TrimSpace(filePath); s != "" {
		return s
	}
	if s := strings.TrimSpace(path); s != "" {
		return s
	}
	return ""
}

func hasRequiredToolInputFields(nameKey string, fields toolInputFields) bool {
	switch nameKey {
	case "edit":
		path := resolveToolPath(fields.FilePath, fields.Path)
		return path != "" && len(fields.Old) > 0 && len(fields.New) > 0
	case "write":
		// Warp sometimes sends "path" instead of "file_path", or we might have mapped it.
		// Also strict checking might fail if "content" is empty string (though rare for meaningful write).
		path := resolveToolPath(fields.FilePath, fields.Path)
		return path != "" && len(fields.Content) > 0
	case "bash":
		return strings.TrimSpace(fields.Command) != "" || strings.TrimSpace(fields.Cmd) != ""
	case "read":
		return resolveToolPath(fields.FilePath, fields.Path) != ""
	default:
		return true
	}
}

func canonicalToolRawValue(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var decoded interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return trimmed
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return trimmed
	}
	return string(normalized)
}

func sideEffectToolDedupKeyFromFields(nameKey string, fields toolInputFields) string {
	if !isSideEffectToolName(nameKey) {
		return ""
	}
	switch nameKey {
	case "bash":
		command := strings.TrimSpace(fields.Command)
		if strings.TrimSpace(command) == "" {
			command = strings.TrimSpace(fields.Cmd)
		}
		command = strings.TrimSpace(command)
		if command == "" {
			return ""
		}
		return "bash:" + command
	case "write":
		path := resolveToolPath(fields.FilePath, fields.Path)
		if path == "" {
			return ""
		}
		if len(fields.Content) == 0 {
			return ""
		}
		return "write:" + path + "\x00" + canonicalToolRawValue(fields.Content)
	case "edit":
		path := resolveToolPath(fields.FilePath, fields.Path)
		if path == "" {
			return ""
		}
		if len(fields.Old) == 0 || len(fields.New) == 0 {
			return ""
		}
		return "edit:" + path + "\x00" + canonicalToolRawValue(fields.Old) + "\x00" + canonicalToolRawValue(fields.New)
	default:
		return ""
	}
}

func fnv1a64String(s string) uint64 {
	h := fnv64Offset
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnv64Prime
	}
	return h
}

func fnv1a64Pair(a, b string) uint64 {
	h := fnv64Offset
	for i := 0; i < len(a); i++ {
		h ^= uint64(a[i])
		h *= fnv64Prime
	}
	h ^= 0
	h *= fnv64Prime
	for i := 0; i < len(b); i++ {
		h ^= uint64(b[i])
		h *= fnv64Prime
	}
	return h
}

func (h *streamHandler) markWriteErrorLocked(event string, err error) {
	if err == nil {
		return
	}
	if h.hasReturn {
		return
	}
	h.hasReturn = true
	h.finalStopReason = "write_error"
	slog.Warn("SSE write failed", "event", event, "error", err)
}

func (h *streamHandler) forceFinishIfMissing() {
	h.mu.Lock()
	if h.hasReturn {
		h.mu.Unlock()
		return
	}
	hasToolCalls := h.toolCallCount > 0 ||
		len(h.pendingToolCalls) > 0 ||
		len(h.toolCallEmitted) > 0
	hasOutput := h.hasTextOutput || h.responseText.Len() > 0 || len(h.contentBlocks) > 0
	h.mu.Unlock()

	// Inject a fallback text block if upstream produced nothing.
	if !hasToolCalls && !hasOutput {
		slog.Warn("Upstream returned no output; injecting fallback text block")
		h.ensureBlock("text")
		h.mu.Lock()
		internalIdx := h.activeTextBlockIndex
		sseIdx := h.activeTextSSEIndex
		h.mu.Unlock()

		emptyMsg := "No response from upstream. The request may not be supported in this mode."
		if h.isStream {
			deltaData, _ := marshalSSEContentBlockDeltaTextBytes(sseIdx, emptyMsg)
			h.writeSSEBytes("content_block_delta", deltaData)
		} else {
			h.responseText.WriteString(emptyMsg)
			if builder, ok := h.textBlockBuilders[internalIdx]; ok {
				builder.WriteString(emptyMsg)
			}
		}
	}

	stopReason := "end_turn"
	if hasToolCalls {
		stopReason = "tool_use"
	}
	slog.Warn("Upstream stream ended without explicit stop marker; forcing response finish", "stop_reason", stopReason)
	h.finishResponse(stopReason)
}

func (h *streamHandler) hasAnyOutput() bool {
	h.mu.Lock()
	has := h.hasTextOutput ||
		h.toolCallCount > 0 ||
		len(h.pendingToolCalls) > 0 ||
		len(h.toolCallEmitted) > 0 ||
		len(h.contentBlocks) > 0 ||
		h.responseText.Len() > 0
	h.mu.Unlock()
	if has {
		return true
	}

	h.outputMu.Lock()
	has = h.outputEstimator.HasText() || h.outputTokens > 0
	h.outputMu.Unlock()
	return has
}

func (h *streamHandler) shouldSkipIntroDelta(delta string) bool {
	key := normalizeIntroKey(delta)
	if key == "" {
		return false
	}
	h.mu.Lock()
	_, exists := h.introDedup[key]
	if !exists {
		h.introDedup[key] = struct{}{}
	}
	h.mu.Unlock()
	return exists
}

func (h *streamHandler) shouldSkipCrossChannelDuplicateDelta(source, delta string) bool {
	if strings.TrimSpace(delta) == "" || source == "" {
		return false
	}
	now := time.Now()

	h.mu.Lock()
	defer h.mu.Unlock()

	skip := h.lastTextDelta == delta &&
		h.lastTextDeltaSource != "" &&
		h.lastTextDeltaSource != source &&
		now.Sub(h.lastTextDeltaAt) <= 2*time.Second

	h.lastTextDelta = delta
	h.lastTextDeltaSource = source
	h.lastTextDeltaAt = now
	return skip
}

func normalizeIntroKey(delta string) string {
	text := strings.TrimSpace(delta)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	compactLower := strings.Join(strings.Fields(strings.ReplaceAll(lower, "\U0001F44B", "")), " ")
	switch lower {
	case "hi! how can i help you today?",
		"hello! how can i help you today?",
		"hi! how can i help you today!",
		"hello! how can i help you today!":
		return "intro:en:greet"
	}
	switch compactLower {
	case "hi! what's up? how can i help today?",
		"hello! what's up? how can i help today?",
		"hi! how can i help today?",
		"hello! how can i help today?",
		"hi! how can i help you today?",
		"hello! how can i help you today?",
		"hi! how can i help you today!",
		"hello! how can i help you today!":
		return "intro:en:greet"
	}
	if (strings.HasPrefix(compactLower, "hi!") || strings.HasPrefix(compactLower, "hello!") || strings.HasPrefix(compactLower, "hey!")) &&
		(strings.Contains(compactLower, "how can i help today") || strings.Contains(compactLower, "how can i help you today")) {
		return "intro:en:greet"
	}
	if strings.HasPrefix(text, "\u4f60\u597d") || strings.HasPrefix(text, "\u60a8\u597d") || strings.Contains(text, "\u6211\u80fd\u5e2e\u4f60") {
		return "intro:zh:greet"
	}
	if strings.Contains(lower, "warp") && (strings.HasPrefix(text, "\u6211\u662f") || strings.Contains(text, "agent mode")) {
		return "intro:zh:warp"
	}
	if strings.Contains(lower, "claude") && (strings.HasPrefix(text, "\u6211\u662f") || strings.Contains(lower, "claude 4")) {
		return "intro:zh:claude"
	}
	return ""
}

func collapseDuplicatedIntroDelta(delta string) string {
	text := strings.TrimSpace(delta)
	if text == "" || len(text)%2 != 0 {
		return delta
	}
	half := len(text) / 2
	first := strings.TrimSpace(text[:half])
	second := strings.TrimSpace(text[half:])
	if first == "" || second == "" || first != second {
		return delta
	}
	if normalizeIntroKey(first) == "" {
		return delta
	}
	return first
}

// extractThinkingSignature extracts a signature from event or event.data.
func extractThinkingSignature(event map[string]interface{}) string {
	if event == nil {
		return ""
	}
	if sig, ok := event["signature"].(string); ok {
		return strings.TrimSpace(sig)
	}
	if data, ok := event["data"].(map[string]interface{}); ok {
		if sig, ok := data["signature"].(string); ok {
			return strings.TrimSpace(sig)
		}
	}
	return ""
}

func extractEventMessage(event map[string]interface{}, fallback string) string {
	if event == nil {
		return fallback
	}
	if data, ok := event["data"].(map[string]interface{}); ok {
		if msg, ok := data["message"].(string); ok && strings.TrimSpace(msg) != "" {
			return strings.TrimSpace(msg)
		}
	}
	if msg, ok := event["message"].(string); ok && strings.TrimSpace(msg) != "" {
		return strings.TrimSpace(msg)
	}
	return fallback
}

func (h *streamHandler) handleMessage(msg upstream.SSEMessage) {
	if h.config.DebugEnabled && msg.Type != "content_block_delta" {
		fields := []any{"type", msg.Type}
		if msg.Event != nil {
			// Avoid leaking secrets in logs: only log high-level shape.
			evtType, _ := msg.Event["type"].(string)
			fields = append(fields, "event_type", evtType)
			if delta, ok := msg.Event["delta"]; ok {
				fields = append(fields, "has_delta", delta != nil)
			}
			if data, ok := msg.Event["data"].(map[string]interface{}); ok {
				fields = append(fields, "data_keys", mapKeys(data))
				if msgStr, ok := data["message"].(string); ok {
					fields = append(fields, "data_message_len", len(msgStr))
				}
			}
			fields = append(fields, "event_keys", mapKeys(msg.Event))
		}
		slog.Debug("Incoming SSE", fields...)
	}
	h.mu.Lock()
	done := h.hasReturn
	h.mu.Unlock()
	if done {
		return
	}

	eventKey := msg.Type
	if msg.Type == "model" && msg.Event != nil {
		if evtType, ok := msg.Event["type"].(string); ok {
			eventKey = "model." + evtType
		}
	}

	// Instrument: Log detailed error info
	if strings.HasSuffix(eventKey, ".error") || strings.Contains(eventKey, "error") {
		if msg.Event != nil {
			if data, ok := msg.Event["data"]; ok {
				slog.Warn("SSE Error Payload", "type", eventKey, "data", data)
			}
		}
	}
	if h.suppressThinking {
		if strings.HasPrefix(eventKey, "model.reasoning-") ||
			strings.HasPrefix(eventKey, "coding_agent.reasoning") ||
			eventKey == "coding_agent.start" ||
			eventKey == "coding_agent.initializing" {
			return
		}
	}

	getUsageInt := func(usage map[string]interface{}, key string) (int, bool) {
		if usage == nil {
			return 0, false
		}
		if raw, ok := usage[key]; ok {
			switch v := raw.(type) {
			case float64:
				return int(v), true
			case int:
				return v, true
			case json.Number:
				if n, err := v.Int64(); err == nil {
					return int(n), true
				}
			}
		}
		return 0, false
	}

	switch eventKey {
	case "model.conversation_id":
		if msg.Event != nil {
			if id, ok := msg.Event["id"].(string); ok && id != "" && h.onConversationID != nil {
				h.onConversationID(id)
			}
		}

	case "model.reasoning-start":
		h.pendingThinkingSig = ""
		if sig := extractThinkingSignature(msg.Event); sig != "" {
			h.pendingThinkingSig = sig
			h.ensureBlock("thinking")
		}

	case "model.reasoning-delta", "coding_agent.reasoning.chunk":
		sig := ""
		if h.pendingThinkingSig == "" {
			sig = extractThinkingSignature(msg.Event)
			if sig != "" {
				h.pendingThinkingSig = sig
			}
		} else {
			sig = h.pendingThinkingSig
		}
		delta := ""
		if msg.Type == "model" {
			delta, _ = msg.Event["delta"].(string)
		} else {
			// coding_agent.reasoning.chunk
			if data, ok := msg.Event["data"].(map[string]interface{}); ok {
				delta, _ = data["text"].(string)
			}
		}
		if delta == "" {
			if sig != "" {
				h.ensureBlock("thinking")
				h.mu.Lock()
				internalIdx := h.activeThinkingBlockIndex
				if internalIdx >= 0 && internalIdx < len(h.contentBlocks) {
					if existing, ok := h.thinkingBlockSigs[internalIdx]; ok && existing == "" {
						h.thinkingBlockSigs[internalIdx] = sig
						h.contentBlocks[internalIdx]["signature"] = sig
					}
				}
				h.mu.Unlock()
			}
			return
		}

		h.mu.Lock()
		sseIdx := h.activeThinkingSSEIndex
		internalIdx := h.activeThinkingBlockIndex
		if sig != "" && internalIdx >= 0 && internalIdx < len(h.contentBlocks) {
			if existing, ok := h.thinkingBlockSigs[internalIdx]; ok && existing == "" {
				h.thinkingBlockSigs[internalIdx] = sig
				h.contentBlocks[internalIdx]["signature"] = sig
			}
		}
		h.mu.Unlock()
		if sseIdx < 0 {
			// If we get delta but no thinking block is active, try to ensure one
			sseIdx = h.ensureBlock("thinking")
			h.mu.Lock()
			internalIdx = h.activeThinkingBlockIndex
			h.mu.Unlock()
		}
		if h.isStream {
			h.addOutputTokens(delta)
		}
		// Always update internal state for history
		h.mu.Lock()
		if internalIdx >= 0 && internalIdx < len(h.contentBlocks) {
			builder, ok := h.thinkingBlockBuilders[internalIdx]
			if !ok {
				builder = perf.AcquireStringBuilder()
				h.thinkingBlockBuilders[internalIdx] = builder
			}
			builder.WriteString(delta)
		}
		h.mu.Unlock()
		h.writeSSEContentBlockDeltaThinking(sseIdx, delta, false)

	case "model.reasoning-end":
		h.closeActiveBlock()

	case "model.text-start":
		h.ensureBlock("text")

	case "model.text-delta", "coding_agent.output_text.delta":
		delta := ""
		source := eventKey
		if msg.Type == "model" {
			delta, _ = msg.Event["delta"].(string)
		} else {
			// coding_agent.output_text.delta
			delta, _ = msg.Event["delta"].(string)
		}
		if delta == "" {
			return
		}
		delta = collapseDuplicatedIntroDelta(delta)
		if h.shouldSkipIntroDelta(delta) {
			return
		}
		if h.shouldSkipCrossChannelDuplicateDelta(source, delta) {
			if h.config != nil && h.config.DebugEnabled {
				slog.Debug("skip cross-channel duplicate delta", "source", source, "delta_len", len(delta))
			}
			return
		}
		h.markTextOutput()

		h.mu.Lock()
		sseIdx := h.activeTextSSEIndex
		internalIdx := h.activeTextBlockIndex
		h.mu.Unlock()
		if sseIdx < 0 {
			// If we get delta but no text block is active, try to ensure one
			sseIdx = h.ensureBlock("text")
			h.mu.Lock()
			internalIdx = h.activeTextBlockIndex
			h.mu.Unlock()
		}
		h.addOutputTokens(delta)
		if !h.isStream {
			h.responseText.WriteString(delta)
		}
		// Always update internal state for history
		h.mu.Lock()
		if internalIdx >= 0 && internalIdx < len(h.contentBlocks) {
			builder, ok := h.textBlockBuilders[internalIdx]
			if !ok {
				builder = perf.AcquireStringBuilder()
				h.textBlockBuilders[internalIdx] = builder
			}
			builder.WriteString(delta)
		}
		h.mu.Unlock()
		h.writeSSEContentBlockDeltaText(sseIdx, delta, false)

	case "model.text-end":
		h.closeActiveBlock()

	case "coding_agent.start", "coding_agent.initializing", "init":
		// Ensure a thinking block is open for these status updates when we already have signature or block
		h.mu.Lock()
		hasThinkingBlock := h.activeThinkingSSEIndex >= 0
		h.mu.Unlock()
		if hasThinkingBlock || h.pendingThinkingSig != "" {
			h.ensureBlock("thinking")
		}
		h.writeUpstreamEventSSE(msg)
		return

	case "coding_agent.credits_exhausted":
		errorMsg := extractEventMessage(msg.Event, "You have run out of credits. Please upgrade your plan to continue.")
		h.closeActiveBlock()
		h.InjectErrorText("Injecting credits exhausted message to client", errorMsg)
		h.finishResponse("end_turn")
		return

	case "coding_agent.Write.started", "coding_agent.Edit.edit.started":
		if h.isStream {
			data, _ := msg.Event["data"].(map[string]interface{})
			path, _ := data["file_path"].(string)
			if !h.suppressThinking {
				op := "Writing"
				if strings.Contains(msg.Type, "Edit") {
					op = "Editing"
				}
				h.ensureBlock("thinking")
				h.emitThinkingDelta(fmt.Sprintf("\n[%s %s...]\n", op, path))

				h.writeUpstreamEventSSE(msg)
			}
		}
		return

	case "coding_agent.Write.content.chunk", "coding_agent.Edit.edit.chunk":
		if h.isStream {
			data, _ := msg.Event["data"].(map[string]interface{})
			text, _ := data["text"].(string)
			if text != "" {
				h.mu.Lock()
				if h.writeChunkBuffer != nil {
					h.writeChunkBuffer.WriteString(text)
				}
				h.mu.Unlock()
				// In no-thinking mode, surface Orchids write chunks as normal text deltas
				// so clients still see visible output instead of only internal events.
				if h.suppressThinking {
					h.emitTextDelta(text)
				} else {
					// Map Orchids code chunks to thinking blocks for standard UIs.
					h.emitThinkingDelta(text)
				}
			}
			if !h.suppressThinking {
				h.writeUpstreamEventSSE(msg)
			}
		}
		return

	case "coding_agent.Write.content.completed", "coding_agent.Edit.edit.completed", "coding_agent.edit_file.completed":
		if h.isStream {
			if !h.suppressThinking {
				h.emitThinkingDelta("\n[Done]\n")
				h.writeUpstreamEventSSE(msg)
			}
		}
		return

	case "fs_operation":
		// Throttle keep-alives and passthrough to avoid flooding
		h.mu.Lock()
		if time.Since(h.lastScanTime) < 1*time.Second {
			h.mu.Unlock()
			return
		}
		h.lastScanTime = time.Now()
		h.mu.Unlock()

		if h.config.DebugEnabled {
			slog.Debug("Upstream active", "op", msg.Event["operation"])
		}
		if h.isStream {
			h.writeUpstreamEventSSE(msg)
		} else {
			h.writeKeepAlive()
		}
		return

	case "fs_operation_result":
		// Just pass through the event, no internal tool result handling in proxy mode
		return

	case "model.tool-input-start":
		h.closeActiveBlock() // Tool input starts a separate block mechanism
		toolID, _ := msg.Event["id"].(string)
		toolName, _ := msg.Event["toolName"].(string)
		toolName = normalizeUpstreamToolName(toolName)
		if toolID == "" || toolName == "" {
			return
		}
		h.currentToolInputID = toolID
		h.toolInputNames[toolID] = toolName
		h.toolInputBuffers[toolID] = perf.AcquireStringBuilder()
		h.toolInputHadDelta[toolID] = false
		// 婵犵數濮烽弫鎼佸磻濞戔懞鍥敇閵忕姷顦悗骞垮劚椤︻垳绮堥崼婢濆綊鎮℃惔锝嗘喖闂佸搫鎷嬮崜姘跺箞閵娿儺娼ㄩ柛鈩冦仦缁ㄤ粙姊洪懡銈呮瀾缂佽鐗撻獮鍐倻閽樺宓嗗┑顔斤耿绾危椤斿皷鏀介柣姗嗗亜娴?tool-input-start 闂傚倷娴囬褏鎹㈤幇顔藉床闁归偊鍓涢弳锔姐亜閹烘垵鏆斿ù婊冪秺閺屾稑鐣濋埀顒勫磻閻愮儤鍊?tool_use闂傚倸鍊烽悞锔锯偓绗涘懐鐭欓柟杈鹃檮閸庢鏌涚仦鍓р槈妞ゆ洟浜堕弻宥夊传閸曨剙娅ｇ紓浣插亾闁稿本澹曢崑鎾荤嵁閸喖濮庨柣搴㈠嚬閸ｏ綁骞冮悜钘夌疀妞ゆ挾濮烽鏇㈡⒑閻熸澘鈷旂紒顕呭灠閳诲秴顭ㄩ崼鐔哄幘闂佸壊鐓堥崑鍕倶鐎电硶鍋撳▓鍨珮闁告挾鍠栭妴浣割潨閳ь剟骞冨鍫濆耿婵°倓绶￠崯宀勬⒒閸屾瑨鍏岄柛妯犲洤搴婇柡灞诲劜閸嬨倝鏌曟繛鍨壔?tool_result闂?		return

	case "model.tool-input-delta":
		toolID, _ := msg.Event["id"].(string)
		delta, _ := msg.Event["delta"].(string)
		if toolID == "" {
			return
		}
		if buf, ok := h.toolInputBuffers[toolID]; ok {
			buf.WriteString(delta)
		}
		if delta != "" {
			h.toolInputHadDelta[toolID] = true
		}
		return

	case "model.tool-input-end":
		toolID, _ := msg.Event["id"].(string)
		if toolID == "" {
			return
		}
		if h.currentToolInputID == toolID {
			h.currentToolInputID = ""
		}
		name, ok := h.toolInputNames[toolID]
		if !ok || name == "" {
			if buf, ok := h.toolInputBuffers[toolID]; ok {
				perf.ReleaseStringBuilder(buf)
			}
			delete(h.toolInputBuffers, toolID)
			delete(h.toolInputHadDelta, toolID)
			delete(h.toolInputNames, toolID)
			return
		}
		inputStr := ""
		if buf, ok := h.toolInputBuffers[toolID]; ok {
			inputStr = strings.TrimSpace(buf.String())
			perf.ReleaseStringBuilder(buf)
		}
		inputStr = sanitizeToolInput(name, inputStr)
		delete(h.toolInputBuffers, toolID)
		delete(h.toolInputHadDelta, toolID)
		delete(h.toolInputNames, toolID)
		if h.toolCallHandled[toolID] {
			return
		}
		call := toolCall{id: toolID, name: name, input: inputStr}
		if !h.shouldAcceptToolCall(call) {
			return
		}
		h.toolCallHandled[toolID] = true
		if h.isStream {
			if inputStr != "" {
				h.addOutputTokens(inputStr)
			}
			h.emitToolUseFromInput(toolID, name, inputStr)
			return
		}
		h.handleToolCallAfterChecks(call)

	case "model.tool-call":
		toolID, _ := msg.Event["toolCallId"].(string)
		toolName, _ := msg.Event["toolName"].(string)
		toolName = normalizeUpstreamToolName(toolName)
		inputStr, _ := msg.Event["input"].(string)
		inputStr = sanitizeToolInput(toolName, inputStr)
		if toolID == "" {
			toolID = fallbackToolCallID(toolName, inputStr)
			if toolID == "" {
				return
			}
		}
		if h.toolCallHandled[toolID] {
			return
		}
		call := toolCall{id: toolID, name: toolName, input: inputStr}
		if !h.shouldAcceptToolCall(call) {
			return
		}
		if h.currentToolInputID == toolID {
			h.currentToolInputID = ""
		}
		if buf, ok := h.toolInputBuffers[toolID]; ok {
			perf.ReleaseStringBuilder(buf)
		}
		delete(h.toolInputBuffers, toolID)
		delete(h.toolInputHadDelta, toolID)
		delete(h.toolInputNames, toolID)
		h.toolCallHandled[toolID] = true
		if h.isStream {
			h.emitToolUseFromInput(toolID, toolName, inputStr)
			return
		}
		h.handleToolCallAfterChecks(call)

	case "model.tokens-used":
		usage := msg.Event
		inputTokens, hasIn := getUsageInt(usage, "inputTokens")
		outputTokens, hasOut := getUsageInt(usage, "outputTokens")
		if !hasIn {
			inputTokens, hasIn = getUsageInt(usage, "input_tokens")
		}
		if !hasOut {
			outputTokens, hasOut = getUsageInt(usage, "output_tokens")
		}
		if hasIn || hasOut {
			in := -1
			out := -1
			if hasIn {
				in = inputTokens
			}
			if hasOut {
				out = outputTokens
			}
			h.setUsageTokens(in, out)
		}
		return

	case "model.finish":
		stopReason := "end_turn"
		if usage, ok := msg.Event["usage"].(map[string]interface{}); ok {
			inputTokens, hasIn := getUsageInt(usage, "inputTokens")
			outputTokens, hasOut := getUsageInt(usage, "outputTokens")
			if !hasIn {
				inputTokens, hasIn = getUsageInt(usage, "input_tokens")
			}
			if !hasOut {
				outputTokens, hasOut = getUsageInt(usage, "output_tokens")
			}
			if hasIn || hasOut {
				in := -1
				out := -1
				if hasIn {
					in = inputTokens
				}
				if hasOut {
					out = outputTokens
				}
				h.setUsageTokens(in, out)
			}
		}
		if finishReason, ok := msg.Event["finishReason"].(string); ok {
			switch finishReason {
			case "tool-calls", "tool_use":
				stopReason = "tool_use"
			case "stop", "end_turn":
				stopReason = "end_turn"
			}
		}

		h.mu.Lock()
		toolUseEmitted := len(h.toolCallEmitted) > 0
		hadToolCalls := h.toolCallCount > 0 ||
			len(h.pendingToolCalls) > 0 ||
			toolUseEmitted
		h.mu.Unlock()

		// Force stopReason to tool_use if we have emitted tool calls
		if toolUseEmitted {
			stopReason = "tool_use"
		}

		// If upstream claims tool_use but we didn't actually handle any tool calls, treat as end_turn.
		if stopReason == "tool_use" && !hadToolCalls {
			stopReason = "end_turn"
		}

		h.closeActiveBlock()
		h.finishResponse(stopReason)
	}
}

func (h *streamHandler) emitThinkingDelta(delta string) {
	if delta == "" || h.suppressThinking {
		return
	}
	h.mu.Lock()
	sseIdx := h.activeThinkingSSEIndex
	internalIdx := h.activeThinkingBlockIndex
	h.mu.Unlock()

	if sseIdx < 0 {
		sseIdx = h.ensureBlock("thinking")
		h.mu.Lock()
		internalIdx = h.activeThinkingBlockIndex
		h.mu.Unlock()
	}

	h.addOutputTokens(delta)

	h.mu.Lock()
	if internalIdx >= 0 && internalIdx < len(h.contentBlocks) {
		builder, ok := h.thinkingBlockBuilders[internalIdx]
		if !ok {
			builder = perf.AcquireStringBuilder()
			h.thinkingBlockBuilders[internalIdx] = builder
		}
		builder.WriteString(delta)
	}
	h.mu.Unlock()

	h.writeSSEContentBlockDeltaThinking(sseIdx, delta, false)
}

func (h *streamHandler) emitTextDelta(delta string) {
	if delta == "" {
		return
	}
	h.markTextOutput()

	h.mu.Lock()
	sseIdx := h.activeTextSSEIndex
	internalIdx := h.activeTextBlockIndex
	h.mu.Unlock()

	if sseIdx < 0 {
		sseIdx = h.ensureBlock("text")
		h.mu.Lock()
		internalIdx = h.activeTextBlockIndex
		h.mu.Unlock()
	}

	h.addOutputTokens(delta)

	h.mu.Lock()
	if internalIdx >= 0 && internalIdx < len(h.contentBlocks) {
		builder, ok := h.textBlockBuilders[internalIdx]
		if !ok {
			builder = perf.AcquireStringBuilder()
			h.textBlockBuilders[internalIdx] = builder
		}
		builder.WriteString(delta)
	}
	h.mu.Unlock()

	h.writeSSEContentBlockDeltaText(sseIdx, delta, false)
}

// InjectErrorText injects an error message as a text delta into the stream or buffer.
func (h *streamHandler) InjectErrorText(logMsg, errorMsg string) {
	if h.config != nil && h.config.DebugEnabled {
		slog.Info(logMsg, "error_msg", errorMsg, "is_stream", h.isStream)
	}
	h.markTextOutput()
	idx := h.ensureBlock("text")
	internalIdx := h.activeTextBlockIndex

	if h.isStream {
		data, _ := marshalSSEContentBlockDeltaTextBytes(idx, errorMsg)
		h.writeSSEBytes("content_block_delta", data)
	} else {
		h.mu.Lock()
		if builder, ok := h.textBlockBuilders[internalIdx]; ok {
			builder.WriteString(errorMsg)
		}
		h.mu.Unlock()
	}
}

func (h *streamHandler) InjectAuthError(category, errStr string) {
	var errorMsg string
	switch {
	case strings.Contains(errStr, "401"):
		errorMsg = "Authentication Error: Session expired (401). Please update your account credentials."
	case strings.Contains(errStr, "403"):
		errorMsg = "Access Forbidden (403): Your account might be flagged or blocked. Try re-enabling it in the Admin UI."
	default:
		errorMsg = fmt.Sprintf("Request Failed: %s. Please check your account status.", errStr)
	}
	h.InjectErrorText("Injecting auth error to client", errorMsg)
}

func (h *streamHandler) InjectRetryExhaustedError(lastErr string) {
	errorMsg := fmt.Sprintf("Request failed: retries exhausted. Last error: %s", lastErr)
	h.InjectErrorText("Injecting retry exhausted error to client", errorMsg)
}

func (h *streamHandler) InjectNoAvailableAccountError(lastErr string, selectErr error) {
	errorMsg := "Request failed: retries exhausted and no available accounts. Please check account statuses in Admin UI or add valid accounts."
	if selectErr != nil {
		errorMsg = fmt.Sprintf("%s (selector: %v, last error: %s)", errorMsg, selectErr, lastErr)
	}
	h.InjectErrorText("Injecting no available account error to client", errorMsg)
}
