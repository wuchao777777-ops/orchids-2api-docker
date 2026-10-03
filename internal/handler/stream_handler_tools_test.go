package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"encoding/json"

	"orchids-api/internal/adapter"
	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/testutil"
	"orchids-api/internal/upstream"
)

func TestSanitizeToolInput_FieldMapping(t *testing.T) {
	in := `{"path":"a.txt","content":"hi","overwrite":true}`
	out := sanitizeToolInput("write", in)
	var m map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(out), &m), "expected json out: %v")
	if _, ok := m["overwrite"]; ok {
		t.Fatalf("expected overwrite removed")
	}
	testutil.Equal(t, m["file_path"], "a.txt")
	if _, ok := m["path"]; ok {
		t.Fatalf("expected path removed")
	}
}

func TestNormalizeUpstreamToolCall_ListDirUsesTopLevelBash(t *testing.T) {
	name, input := normalizeUpstreamToolCall("LS", `{"path":"/tmp/project"}`)
	testutil.Equal(t, name, "Bash")
	var payload map[string]string
	testutil.NoError(t, json.Unmarshal([]byte(input), &payload), "expected json input, got %v")
	testutil.Equal(t, payload["command"], `ls -1A -- "/tmp/project"`)
	testutil.Equal(t, payload["description"], "List top-level directory entries")
}

func TestNormalizeUpstreamToolCall_GlobPreservesGlob(t *testing.T) {
	name, input := normalizeUpstreamToolCall("Glob", `{"path":"/tmp/project"}`)
	testutil.Equal(t, name, "Glob")
	testutil.MustContain(t, input, `"pattern":"*"`)
}

func TestRewriteToolCallToClient_PrunesNestedUnknownTodoFields(t *testing.T) {
	h := newStreamHandler(&config.Config{}, httptest.NewRecorder(), debug.New(false, false), false, false, adapter.FormatAnthropic)
	defer h.release()
	h.setClientTools([]interface{}{map[string]interface{}{
		"name": "TodoWrite",
		"input_schema": map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"todos": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]interface{}{
							"content": map[string]interface{}{"type": "string"},
							"status":  map[string]interface{}{"type": "string"},
						},
					},
				},
			},
		},
	}})
	name, input := h.rewriteToolCallToClient("TodoWrite", `{"todos":[{"content":"one","status":"pending","id":"1"}]}`)
	testutil.Falsef(t, name != "TodoWrite" || strings.Contains(input, `"id"`), "unexpected sanitized todo call: name=%s input=%s", name, input)
	var payload map[string]interface{}
	testutil.NoError(t, json.Unmarshal([]byte(input), &payload))
	todos := payload["todos"].([]interface{})
	_, ok := todos[0].(map[string]interface{})["content"]
	testutil.Falsef(t, !ok, "content was lost: %s", input)
}

func TestStreamHandler_NoToolsGateSuppressesValidToolCall(t *testing.T) {
	sh, rec := newStreamTestHandler(t, false, true, adapter.FormatAnthropic)

	sh.setDisallowToolCalls(true)
	sh.handleMessage(upstream.SSEMessage{
		Type: "model",
		Event: map[string]any{
			"type":       "tool-call",
			"toolCallId": "tool_1",
			"toolName":   "Read",
			"input":      `{"file_path":"README.md"}`,
		},
	})
	sh.finishResponse("tool_use")

	out := rec.buf.String()
	testutil.MustNotContain(t, out, `"type":"tool_use"`)
	testutil.MustContain(t, out, `"stop_reason":"end_turn"`)
}

func TestStreamHandler_NoToolsWriteReturnsContentAsText(t *testing.T) {
	sh, rec := newStreamTestHandler(t, false, true, adapter.FormatAnthropic)

	sh.setAllowedToolNames(nil)
	sh.setSurfaceToolRejects(true)
	sh.setDisallowToolCalls(true)
	sh.handleMessage(upstream.SSEMessage{
		Type: "model.tool-call",
		Event: map[string]any{
			"toolCallId": "tool_write_1",
			"toolName":   "Write",
			"input":      `{"file_path":"index.html","content":"<!doctype html><h1>Ready</h1>"}`,
		},
	})
	sh.finishResponse("tool_use")

	out := rec.buf.String()
	testutil.MustNotContain(t, out, `"type":"tool_use"`)
	testutil.MustContain(t, out, "Ready")
	testutil.MustContain(t, out, `"stop_reason":"end_turn"`)
}
