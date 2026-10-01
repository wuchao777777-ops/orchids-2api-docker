package grok

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"orchids-api/internal/testutil"
	"strings"
	"testing"
)

func TestStreamBuildChatSeparatesReasoningFromContent(t *testing.T) {
	recorder := httptest.NewRecorder()
	stream := strings.Join([]string{
		"event: response.reasoning_summary_text.delta",
		`data: {"type":"response.reasoning_summary_text.delta","delta":"plan first"}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"final answer"}`,
		"",
	}, "\n")
	(&Handler{}).streamBuildChatHolding(recorder, &ChatCompletionsRequest{Model: "grok-4.3"}, strings.NewReader(stream), nil)
	raw := recorder.Body.String()
	testutil.MustContain(t, raw, `"reasoning_content":"plan first"`)
	testutil.MustContain(t, raw, `"content":"final answer"`)
	testutil.MustNotContain(t, raw, `"content":"plan first"`)
}

func TestStreamBuildChatStreamsFirstReasoningSourceWithoutDuplicates(t *testing.T) {
	recorder := httptest.NewRecorder()
	stream := strings.Join([]string{
		"event: response.reasoning_summary_text.delta",
		`data: {"type":"response.reasoning_summary_text.delta","delta":"duplicate summary"}`,
		"",
		"event: response.reasoning_text.delta",
		`data: {"type":"response.reasoning_text.delta","delta":"raw reasoning"}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"answer"}`,
		"",
	}, "\n")
	(&Handler{}).streamBuildChatHolding(recorder, &ChatCompletionsRequest{Model: "grok-4.3"}, strings.NewReader(stream), nil)
	raw := recorder.Body.String()
	if !strings.Contains(raw, `"reasoning_content":"duplicate summary"`) || strings.Contains(raw, "raw reasoning") {
		t.Fatalf("first reasoning source should stream immediately without later duplication: %q", raw)
	}
}

func TestCollectBuildChatSeparatesReasoningFromContent(t *testing.T) {
	recorder := httptest.NewRecorder()
	body := `{"id":"resp_1","output":[{"id":"rs_1","type":"reasoning","content":[{"type":"reasoning_text","text":"private plan"}],"summary":[{"type":"summary_text","text":"duplicate summary"}]},{"id":"msg_1","type":"message","content":[{"type":"output_text","text":"public answer"}]}]}`
	(&Handler{}).collectBuildChat(recorder, &ChatCompletionsRequest{Model: "grok-4.3"}, strings.NewReader(body))
	var response map[string]interface{}
	testutil.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	choice := interfaceSlice(response["choices"])[0].(map[string]interface{})
	message := choice["message"].(map[string]interface{})
	if message["content"] != "public answer" || message["reasoning_content"] != "private plan" {
		t.Fatalf("message=%#v", message)
	}
}

type failingBuildStreamReader struct {
	served bool
}

func (r *failingBuildStreamReader) Read(dst []byte) (int, error) {
	if r.served {
		return 0, errors.New("upstream connection interrupted")
	}
	r.served = true
	return copy(dst, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n"), nil
}

func TestStreamBuildChatReportsMalformedSSEData(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Handler{}).streamBuildChatHolding(recorder, &ChatCompletionsRequest{Model: "grok-4.3"}, strings.NewReader("data: {not-json}\n"), nil)

	body := recorder.Body.String()
	testutil.MustContainAll(t, body, "event: error", "Use the request ID", "data: [DONE]")
}

func TestStreamBuildChatReportsScannerError(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Handler{}).streamBuildChatHolding(recorder, &ChatCompletionsRequest{Model: "grok-4.3"}, &failingBuildStreamReader{}, nil)

	body := recorder.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "Use the request ID") || strings.Contains(body, "upstream connection interrupted") || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("stream=%q want explicit SSE read error and terminator", body)
	}
}
