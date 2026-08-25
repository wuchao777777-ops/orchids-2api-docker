package grok

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

type failingConsoleStreamReader struct {
	served bool
}

func (r *failingConsoleStreamReader) Read(dst []byte) (int, error) {
	if r.served {
		return 0, errors.New("upstream connection interrupted")
	}
	r.served = true
	return copy(dst, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n"), nil
}

func TestStreamConsoleChatReportsMalformedSSEData(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Handler{}).streamConsoleChat(recorder, &ChatCompletionsRequest{Model: "grok-4.3"}, strings.NewReader("data: {not-json}\n"))

	body := recorder.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "console stream parse error") || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("stream=%q want explicit SSE parse error and terminator", body)
	}
}

func TestStreamConsoleChatReportsScannerError(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Handler{}).streamConsoleChat(recorder, &ChatCompletionsRequest{Model: "grok-4.3"}, &failingConsoleStreamReader{})

	body := recorder.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "upstream connection interrupted") || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("stream=%q want explicit SSE read error and terminator", body)
	}
}
