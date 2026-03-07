package orchids

import (
	"strings"
	"testing"
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
