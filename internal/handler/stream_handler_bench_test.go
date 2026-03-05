package handler

import (
	"testing"

	"github.com/goccy/go-json"

	"orchids-api/internal/perf"
	"orchids-api/internal/upstream"
)

func BenchmarkMaskDedupKey(b *testing.B) {
	key := "bash:echo hello world"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = maskDedupKey(key)
	}
}

func BenchmarkFallbackToolCallID(b *testing.B) {
	name := "Write"
	input := `{"file_path":"/tmp/a.txt","content":"hello"}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = fallbackToolCallID(name, input)
	}
}

func BenchmarkHasRequiredToolInputUnknownMalformed(b *testing.B) {
	tool := "UnknownTool"
	input := `{"bad":`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = hasRequiredToolInput(tool, input)
	}
}

func BenchmarkToolValidationAndDedupWrite_Separate(b *testing.B) {
	tool := "Write"
	nameKey := "write"
	input := `{"file_path":"/tmp/a.txt","content":"hello"}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !hasRequiredToolInput(tool, input) {
			b.Fatal("unexpected invalid input")
		}
		_ = sideEffectToolDedupKey(nameKey, input)
	}
}

func BenchmarkToolValidationAndDedupWrite_Combined(b *testing.B) {
	tool := "Write"
	input := `{"file_path":"/tmp/a.txt","content":"hello"}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, ok := evaluateToolCallInput(tool, input)
		if !ok {
			b.Fatal("unexpected invalid input")
		}
	}
}

func BenchmarkMarshalContentBlockDeltaText_MapStyle(b *testing.B) {
	idx := 7
	text := "hello world"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m := perf.AcquireMap()
		m["type"] = "content_block_delta"
		m["index"] = idx
		delta := perf.AcquireMap()
		delta["type"] = "text_delta"
		delta["text"] = text
		m["delta"] = delta
		raw, _ := json.Marshal(m)
		_ = string(raw)
		perf.ReleaseMap(delta)
		perf.ReleaseMap(m)
	}
}

func BenchmarkMarshalContentBlockDeltaText_StructStyle(b *testing.B) {
	idx := 7
	text := "hello world"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		raw, _ := marshalSSEContentBlockDeltaText(idx, text)
		_ = raw
	}
}

func BenchmarkMarshalContentBlockStartToolUse_MapStyle(b *testing.B) {
	idx := 3
	id := "tool_123"
	name := "Write"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		startMap := perf.AcquireMap()
		startMap["type"] = "content_block_start"
		startMap["index"] = idx
		contentBlock := perf.AcquireMap()
		contentBlock["type"] = "tool_use"
		contentBlock["id"] = id
		contentBlock["name"] = name
		contentBlock["input"] = perf.AcquireMap()
		startMap["content_block"] = contentBlock
		raw, _ := json.Marshal(startMap)
		_ = string(raw)
		perf.ReleaseMap(contentBlock["input"].(map[string]interface{}))
		perf.ReleaseMap(contentBlock)
		perf.ReleaseMap(startMap)
	}
}

func BenchmarkMarshalContentBlockStartToolUse_StructStyle(b *testing.B) {
	idx := 3
	id := "tool_123"
	name := "Write"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		raw, _ := marshalSSEContentBlockStartToolUse(idx, id, name)
		_ = raw
	}
}

func BenchmarkMarshalEventPayload_MapStyle(b *testing.B) {
	msg := upstream.SSEMessage{
		Type: "coding_agent.Write.content.chunk",
		Event: map[string]interface{}{
			"type": "coding_agent.Write.content.chunk",
			"data": map[string]interface{}{
				"file_path": "/tmp/a.txt",
				"text":      "hello world",
			},
		},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		raw, _ := marshalEventPayload(msg)
		_ = raw
	}
}

func BenchmarkMarshalEventPayload_RawJSON(b *testing.B) {
	msg := upstream.SSEMessage{
		Type:    "coding_agent.Write.content.chunk",
		RawJSON: json.RawMessage(`{"type":"coding_agent.Write.content.chunk","data":{"file_path":"/tmp/a.txt","text":"hello world"}}`),
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		raw, _ := marshalEventPayload(msg)
		_ = raw
	}
}

func BenchmarkSanitizeToolInput_NoOpUnknown(b *testing.B) {
	input := `{"foo":"bar","n":1}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = sanitizeToolInput("unknown", input)
	}
}

func BenchmarkSanitizeToolInput_WriteMap(b *testing.B) {
	input := `{"path":"/tmp/a.txt","content":"hello","overwrite":true}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = sanitizeToolInput("Write", input)
	}
}
