package orchids

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/goccy/go-json"

	"orchids-api/internal/upstream"
)

func legacyOrchidsSSEDataPayload(eventData string) (string, bool) {
	lines := strings.Split(eventData, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, orchidsSSEDataPrefix) {
			rawData := strings.TrimPrefix(line, orchidsSSEDataPrefix)
			if rawData == "" {
				return "", false
			}
			return rawData, true
		}
	}
	return "", false
}

func TestOrchidsSSEDataPayload(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
		ok   bool
	}{
		{name: "json line", line: "data: {\"type\":\"output_text_delta\",\"text\":\"hello\"}\n", want: "{\"type\":\"output_text_delta\",\"text\":\"hello\"}", ok: true},
		{name: "json line crlf", line: "data: {\"type\":\"output_text_delta\",\"text\":\"hello\"}\r\n", want: "{\"type\":\"output_text_delta\",\"text\":\"hello\"}", ok: true},
		{name: "blank data", line: "data: \n", want: "", ok: false},
		{name: "event header", line: "event: message\n", want: "", ok: false},
		{name: "blank line", line: "\n", want: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := orchidsSSEDataPayload(tt.line)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("orchidsSSEDataPayload(%q) = (%q, %v), want (%q, %v)", tt.line, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestOrchidsSSEDataPayloadBytes(t *testing.T) {
	tests := []struct {
		name string
		line []byte
		want string
		ok   bool
	}{
		{name: "json line", line: []byte("data: {\"type\":\"output_text_delta\",\"text\":\"hello\"}\n"), want: "{\"type\":\"output_text_delta\",\"text\":\"hello\"}", ok: true},
		{name: "json line crlf", line: []byte("data: {\"type\":\"output_text_delta\",\"text\":\"hello\"}\r\n"), want: "{\"type\":\"output_text_delta\",\"text\":\"hello\"}", ok: true},
		{name: "blank data", line: []byte("data: \n"), want: "", ok: false},
		{name: "event header", line: []byte("event: message\n"), want: "", ok: false},
		{name: "blank line", line: []byte("\n"), want: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := orchidsSSEDataPayloadBytes(tt.line)
			if ok != tt.ok || string(got) != tt.want {
				t.Fatalf("orchidsSSEDataPayloadBytes(%q) = (%q, %v), want (%q, %v)", tt.line, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestOrchidsSSEDataPayloadMatchesLegacySingleLineEvent(t *testing.T) {
	eventData := "event: message\ndata: {\"type\":\"output_text_delta\",\"text\":\"hello\"}\n\n"
	legacy, legacyOK := legacyOrchidsSSEDataPayload(eventData)
	if !legacyOK {
		t.Fatal("expected legacy parser to find data payload")
	}

	direct, directOK := orchidsSSEDataPayload("data: {\"type\":\"output_text_delta\",\"text\":\"hello\"}\n")
	if !directOK {
		t.Fatal("expected direct parser to find data payload")
	}
	if direct != legacy {
		t.Fatalf("direct payload = %q, want %q", direct, legacy)
	}
}

func BenchmarkOrchidsSSEDataPayload_Legacy(b *testing.B) {
	eventData := "event: message\ndata: {\"type\":\"output_text_delta\",\"text\":\"hello world\"}\n\n"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = legacyOrchidsSSEDataPayload(eventData)
	}
}

func BenchmarkOrchidsSSEDataPayload_Direct(b *testing.B) {
	line := "data: {\"type\":\"output_text_delta\",\"text\":\"hello world\"}\n"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = orchidsSSEDataPayload(line)
	}
}

func BenchmarkOrchidsSSEDataPayload_DirectBytes(b *testing.B) {
	line := []byte("data: {\"type\":\"output_text_delta\",\"text\":\"hello world\"}\n")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = orchidsSSEDataPayloadBytes(line)
	}
}

func collectOrchidsEvents(client *Client, state *requestState, raw string, fast bool) ([]upstream.SSEMessage, bool, bool) {
	events := make([]upstream.SSEMessage, 0, 2)
	onMessage := func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}
	rawBytes := []byte(raw)
	if fast {
		handled, shouldBreak := client.handleOrchidsRawMessage(rawBytes, state, onMessage, nil)
		return events, handled, shouldBreak
	}
	var msg map[string]interface{}
	if err := json.Unmarshal(rawBytes, &msg); err != nil {
		panic(err)
	}
	return events, true, client.handleOrchidsMessage(msg, rawBytes, state, onMessage, nil, nil, nil, "")
}

func collectOrchidsEventsWithFallback(client *Client, state *requestState, raw string) ([]upstream.SSEMessage, bool) {
	events := make([]upstream.SSEMessage, 0, 2)
	onMessage := func(msg upstream.SSEMessage) {
		events = append(events, msg)
	}
	rawBytes := []byte(raw)
	handled, shouldBreak := client.handleOrchidsRawMessage(rawBytes, state, onMessage, nil)
	if handled {
		return events, shouldBreak
	}
	var msg map[string]interface{}
	if err := json.Unmarshal(rawBytes, &msg); err != nil {
		panic(err)
	}
	return events, client.handleOrchidsMessage(msg, rawBytes, state, onMessage, nil, nil, nil, "")
}

func TestHandleOrchidsRawMessageMatchesDecodedTextEvents(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "output_text_delta text", raw: `{"type":"output_text_delta","text":"hello"}`},
		{name: "response_chunk object", raw: `{"type":"coding_agent.response.chunk","chunk":{"content":"hello"}}`},
		{name: "reasoning chunk", raw: `{"type":"coding_agent.reasoning.chunk","data":{"text":"think"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{}
			var legacyState requestState
			wantEvents, _, wantBreak := collectOrchidsEvents(client, &legacyState, tt.raw, false)

			var fastState requestState
			gotEvents, handled, gotBreak := collectOrchidsEvents(client, &fastState, tt.raw, true)
			if !handled {
				t.Fatal("expected fast path to handle message")
			}
			if gotBreak != wantBreak {
				t.Fatalf("shouldBreak=%v want=%v", gotBreak, wantBreak)
			}
			wantJSON, _ := json.Marshal(wantEvents)
			gotJSON, _ := json.Marshal(gotEvents)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("events=%s want=%s", gotJSON, wantJSON)
			}
		})
	}
}

func TestHandleOrchidsRawMessageMatchesDecodedCrossChannelDedup(t *testing.T) {
	client := &Client{}
	var legacyState requestState
	_, _, _ = collectOrchidsEvents(client, &legacyState, `{"type":"output_text_delta","text":"hello"}`, false)
	legacyEvents, _, _ := collectOrchidsEvents(client, &legacyState, `{"type":"coding_agent.response.chunk","chunk":"hello"}`, false)

	var fastState requestState
	_, _, _ = collectOrchidsEvents(client, &fastState, `{"type":"output_text_delta","text":"hello"}`, true)
	fastEvents, handled, _ := collectOrchidsEvents(client, &fastState, `{"type":"coding_agent.response.chunk","chunk":"hello"}`, true)
	if !handled {
		t.Fatal("expected fast path to handle response chunk")
	}
	if len(fastEvents) != len(legacyEvents) {
		t.Fatalf("event count=%d want=%d", len(fastEvents), len(legacyEvents))
	}
}

func BenchmarkHandleOrchidsTextMessage_Map(b *testing.B) {
	client := &Client{}
	raw := []byte(`{"type":"output_text_delta","text":"hello world"}`)
	onMessage := func(upstream.SSEMessage) {}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var state requestState
		var msg map[string]interface{}
		if err := json.Unmarshal(raw, &msg); err != nil {
			b.Fatal(err)
		}
		_ = client.handleOrchidsMessage(msg, raw, &state, onMessage, nil, nil, nil, "")
	}
}

func BenchmarkHandleOrchidsTextMessage_Fast(b *testing.B) {
	client := &Client{}
	raw := []byte(`{"type":"output_text_delta","text":"hello world"}`)
	onMessage := func(upstream.SSEMessage) {}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var state requestState
		handled, _ := client.handleOrchidsRawMessage(raw, &state, onMessage, nil)
		if !handled {
			b.Fatal("expected fast path to handle raw text message")
		}
	}
}

func TestHandleOrchidsRawMessageMatchesDecodedResponseDone(t *testing.T) {
	raw := `{"type":"response_done","response":{"usage":{"inputTokens":12,"outputTokens":34},"output":[{"type":"tool_use","name":"Write","input":{"file_path":"/tmp/a.txt","content":"hello"}}]}}`
	client := &Client{}

	var legacyState requestState
	wantEvents, _, wantBreak := collectOrchidsEvents(client, &legacyState, raw, false)

	var fastState requestState
	gotEvents, handled, gotBreak := collectOrchidsEvents(client, &fastState, raw, true)
	if !handled {
		t.Fatal("expected fast path to handle response.done")
	}
	if gotBreak != wantBreak {
		t.Fatalf("shouldBreak=%v want=%v", gotBreak, wantBreak)
	}
	wantJSON, _ := json.Marshal(wantEvents)
	gotJSON, _ := json.Marshal(gotEvents)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("events=%s want=%s", gotJSON, wantJSON)
	}
}

func TestHandleOrchidsRawMessageMatchesDecodedModelFinish(t *testing.T) {
	raw := `{"type":"model","event":{"type":"finish","finishReason":"stop"}}`
	client := &Client{}

	legacyState := requestState{textStarted: true}
	wantEvents, _, wantBreak := collectOrchidsEvents(client, &legacyState, raw, false)

	fastState := requestState{textStarted: true}
	gotEvents, gotBreak := collectOrchidsEventsWithFallback(client, &fastState, raw)
	if gotBreak != wantBreak {
		t.Fatalf("shouldBreak=%v want=%v", gotBreak, wantBreak)
	}
	wantJSON, _ := json.Marshal(wantEvents)
	gotJSON, _ := json.Marshal(gotEvents)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("events=%s want=%s", gotJSON, wantJSON)
	}
}

func TestHandleOrchidsRawMessageSuppressesDuplicateModelText(t *testing.T) {
	raw := `{"type":"model","event":{"type":"text-delta","delta":"hello"}}`
	client := &Client{}
	state := requestState{preferCodingAgent: true}
	events, handled, shouldBreak := collectOrchidsEvents(client, &state, raw, true)
	if !handled {
		t.Fatal("expected fast path to suppress duplicate model text")
	}
	if shouldBreak {
		t.Fatal("did not expect break for suppressed model text")
	}
	if len(events) != 0 {
		t.Fatalf("expected no emitted events, got %d", len(events))
	}
}

func TestHandleOrchidsRawMessageMatchesDecodedError(t *testing.T) {
	raw := `{"type":"error","data":{"code":"bad_request","message":"boom"}}`
	client := &Client{}

	var legacyState requestState
	wantEvents, _, wantBreak := collectOrchidsEvents(client, &legacyState, raw, false)

	var fastState requestState
	gotEvents, handled, gotBreak := collectOrchidsEvents(client, &fastState, raw, true)
	if !handled {
		t.Fatal("expected fast path to handle error")
	}
	if gotBreak != wantBreak {
		t.Fatalf("shouldBreak=%v want=%v", gotBreak, wantBreak)
	}
	wantJSON, _ := json.Marshal(wantEvents)
	gotJSON, _ := json.Marshal(gotEvents)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("events=%s want=%s", gotJSON, wantJSON)
	}
}

func BenchmarkHandleOrchidsResponseDone_Map(b *testing.B) {
	client := &Client{}
	raw := []byte(`{"type":"response_done","response":{"usage":{"inputTokens":12,"outputTokens":34},"output":[{"type":"tool_use","name":"Write","input":{"file_path":"/tmp/a.txt","content":"hello"}}]}}`)
	onMessage := func(upstream.SSEMessage) {}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var state requestState
		var msg map[string]interface{}
		if err := json.Unmarshal(raw, &msg); err != nil {
			b.Fatal(err)
		}
		_ = client.handleOrchidsMessage(msg, raw, &state, onMessage, nil, nil, nil, "")
	}
}

func BenchmarkHandleOrchidsResponseDone_Fast(b *testing.B) {
	client := &Client{}
	raw := []byte(`{"type":"response_done","response":{"usage":{"inputTokens":12,"outputTokens":34},"output":[{"type":"tool_use","name":"Write","input":{"file_path":"/tmp/a.txt","content":"hello"}}]}}`)
	onMessage := func(upstream.SSEMessage) {}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var state requestState
		handled, _ := client.handleOrchidsRawMessage(raw, &state, onMessage, nil)
		if !handled {
			b.Fatal("expected fast path to handle response.done")
		}
	}
}

func BenchmarkHandleOrchidsModelEvent_Map(b *testing.B) {
	client := &Client{}
	raw := []byte(`{"type":"model","event":{"type":"text-delta","delta":"hello world"}}`)
	onMessage := func(upstream.SSEMessage) {}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		state := requestState{preferCodingAgent: true}
		var msg map[string]interface{}
		if err := json.Unmarshal(raw, &msg); err != nil {
			b.Fatal(err)
		}
		_ = client.handleOrchidsMessage(msg, raw, &state, onMessage, nil, nil, nil, "")
	}
}

func BenchmarkHandleOrchidsModelEvent_Fast(b *testing.B) {
	client := &Client{}
	raw := []byte(`{"type":"model","event":{"type":"text-delta","delta":"hello world"}}`)
	onMessage := func(upstream.SSEMessage) {}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		state := requestState{preferCodingAgent: true}
		handled, _ := client.handleOrchidsRawMessage(raw, &state, onMessage, nil)
		if !handled {
			b.Fatal("expected fast path to handle model event")
		}
	}
}

func BenchmarkHandleOrchidsErrorEvent_Map(b *testing.B) {
	client := &Client{}
	raw := []byte(`{"type":"error","data":{"code":"bad_request","message":"boom"}}`)
	onMessage := func(upstream.SSEMessage) {}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(oldLogger)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var state requestState
		var msg map[string]interface{}
		if err := json.Unmarshal(raw, &msg); err != nil {
			b.Fatal(err)
		}
		_ = client.handleOrchidsMessage(msg, raw, &state, onMessage, nil, nil, nil, "")
	}
}

func BenchmarkHandleOrchidsErrorEvent_Fast(b *testing.B) {
	client := &Client{}
	raw := []byte(`{"type":"error","data":{"code":"bad_request","message":"boom"}}`)
	onMessage := func(upstream.SSEMessage) {}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(oldLogger)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var state requestState
		handled, _ := client.handleOrchidsRawMessage(raw, &state, onMessage, nil)
		if !handled {
			b.Fatal("expected fast path to handle error event")
		}
	}
}

func BenchmarkHandleOrchidsHTTPTextLine_StringFlow(b *testing.B) {
	client := &Client{}
	line := "data: {\"type\":\"output_text_delta\",\"text\":\"hello world\"}\n"
	onMessage := func(upstream.SSEMessage) {}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var state requestState
		raw, ok := orchidsSSEDataPayload(line)
		if !ok {
			b.Fatal("expected payload")
		}
		bytesRaw := []byte(raw)
		handled, _ := client.handleOrchidsRawMessage(bytesRaw, &state, onMessage, nil)
		if !handled {
			b.Fatal("expected string flow to handle line")
		}
	}
}

func BenchmarkHandleOrchidsHTTPTextLine_BytesFlow(b *testing.B) {
	client := &Client{}
	line := []byte("data: {\"type\":\"output_text_delta\",\"text\":\"hello world\"}\n")
	onMessage := func(upstream.SSEMessage) {}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var state requestState
		raw, ok := orchidsSSEDataPayloadBytes(line)
		if !ok {
			b.Fatal("expected payload")
		}
		handled, _ := client.handleOrchidsRawMessage(raw, &state, onMessage, nil)
		if !handled {
			b.Fatal("expected bytes flow to handle line")
		}
	}
}
