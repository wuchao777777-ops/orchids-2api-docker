package handler

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"orchids-api/internal/adapter"
	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/testutil"
	"orchids-api/internal/upstream"
)

func TestHasRequiredToolInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		tool     string
		input    string
		expected bool
	}{
		{name: "edit empty json", tool: "Edit", input: `{}`, expected: false},
		{name: "edit missing old/new", tool: "Edit", input: `{"file_path":"/tmp/a"}`, expected: false},
		{name: "edit valid", tool: "Edit", input: `{"file_path":"/tmp/a","old_string":"a","new_string":"b"}`, expected: true},
		{name: "write empty json", tool: "Write", input: `{}`, expected: false},
		{name: "write valid", tool: "Write", input: `{"file_path":"/tmp/a","content":"x"}`, expected: true},
		{name: "lowercase write empty json", tool: "write", input: `{}`, expected: false},
		{name: "lowercase write valid", tool: "write", input: `{"file_path":"a","content":"x"}`, expected: true},
		{name: "lowercase write legacy path", tool: "write", input: `{"path":"a","content":"x"}`, expected: true},
		{name: "lowercase bash empty cmd", tool: "bash", input: `{"cmd":""}`, expected: false},
		{name: "bash empty", tool: "Bash", input: `{}`, expected: false},
		{name: "bash valid", tool: "Bash", input: `{"command":"ls"}`, expected: true},
		{name: "unknown tool malformed json", tool: "Unknown", input: `{`, expected: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := validToolCallInput(tc.tool, tc.input)
			testutil.Equal(t, got, tc.expected)
		})
	}
}

func TestToolCallSameIDInvalidThenValid_UsesValidOne(t *testing.T) {
	t.Parallel()

	h := newToolValidationHandler(t)

	// First frame is incomplete and should be rejected.
	sendToolCall(h, "tool_same_id", "Edit", "{}")

	// Second frame (same toolCallId) is valid and should be accepted.
	sendToolCall(h, "tool_same_id", "Write", `{"file_path":"/tmp/a.txt","content":"x"}`)

	sendToolFinish(h)

	testutil.Equal(t, len(h.contentBlocks), 1)

	block := h.contentBlocks[0]
	if got, _ := block["type"].(string); got != "tool_use" {
		t.Fatalf("expected tool_use block, got %q", got)
	}
	if got, _ := block["name"].(string); got != "Write" {
		t.Fatalf("expected Write tool call, got %q", got)
	}
}

func TestWriteToolCallDifferentIDsSameInput_Preserved(t *testing.T) {
	t.Parallel()

	h := newToolValidationHandler(t)

	input := `{"file_path":"/tmp/a.txt","content":"x"}`
	sendToolCall(h, "tool_id_1", "Write", input)
	sendToolCall(h, "tool_id_2", "Write", input)

	sendToolFinish(h)

	testutil.Equal(t, len(h.contentBlocks), 2)
	block := h.contentBlocks[0]
	if got, _ := block["type"].(string); got != "tool_use" {
		t.Fatalf("expected tool_use block, got %q", got)
	}
	if got, _ := block["name"].(string); got != "Write" {
		t.Fatalf("expected Write tool call, got %q", got)
	}
}

func TestWriteToolCallDifferentIDsSameWorkdirTarget_Preserved(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()
	h := newToolValidationHandler(t)

	relativeInput := `{"file_path":"calculator.py","content":"x"}`
	absoluteInput := `{"file_path":"` + strings.ReplaceAll(filepath.Join(workdir, "calculator.py"), `\`, `\\`) + `","content":"x"}`

	sendToolCall(h, "tool_rel", "Write", relativeInput)
	sendToolCall(h, "tool_abs", "Write", absoluteInput)

	sendToolFinish(h)

	testutil.Equal(t, len(h.contentBlocks), 2)
}

func TestReadToolCallDifferentIDsSameInput_BothAccepted(t *testing.T) {
	t.Parallel()

	h := newToolValidationHandler(t)

	input := `{"file_path":"/tmp/a.txt"}`
	sendToolCall(h, "read_id_1", "Read", input)
	sendToolCall(h, "read_id_2", "Read", input)

	sendToolFinish(h)

	testutil.Equal(t, len(h.contentBlocks), 2)
}

func TestWriteToolCallDifferentIDsDifferentContent_BothAccepted(t *testing.T) {
	t.Parallel()

	h := newToolValidationHandler(t)

	sendToolCall(h, "write_id_1", "Write", `{"file_path":"/tmp/a.txt","content":"x"}`)
	sendToolCall(h, "write_id_2", "Write", `{"file_path":"/tmp/a.txt","content":"y"}`)

	sendToolFinish(h)

	testutil.Equal(t, len(h.contentBlocks), 2)
}

// runToolCall drives one tool call through a handler that declares allowed, then
// returns the handler for the caller to assert on what survived the finish frame.
func runToolCall(t *testing.T, allowed []string, callID, name, args string) *streamHandler {
	t.Helper()
	h := newToolValidationHandler(t)
	h.setAllowedToolNames(allowed)
	sendToolCall(h, callID, name, args)
	sendToolFinish(h)
	return h
}

// assertToolCallSurvived checks the call was kept and renamed to wantName.
func assertToolCallSurvived(t *testing.T, h *streamHandler, wantName string) {
	t.Helper()
	testutil.Equal(t, len(h.contentBlocks), 1)
	if got, _ := h.contentBlocks[0]["type"].(string); got != "tool_use" {
		t.Fatalf("expected tool_use block, got %q", got)
	}
	if got, _ := h.contentBlocks[0]["name"].(string); got != wantName {
		t.Fatalf("expected tool name %s, got %q", wantName, got)
	}
	testutil.Equal(t, h.suppressedToolCalls, 0)
}

// assertToolCallSuppressed checks the call was dropped and the turn still ended.
func assertToolCallSuppressed(t *testing.T, h *streamHandler, name string) {
	t.Helper()
	testutil.Equal(t, len(h.contentBlocks), 0)
	testutil.Equal(t, h.suppressedToolCalls, 1)
	testutil.Equal(t, h.finalStopReason, "end_turn")
}

func TestToolCallNotDeclaredInCurrentRequest_IsSuppressed(t *testing.T) {
	t.Parallel()
	h := runToolCall(t, []string{"Read", "Bash"}, "readfolder_1", "ReadFolder", `{"path":"/tmp"}`)
	assertToolCallSuppressed(t, h, "ReadFolder")
}

func TestWriteToolCallNotDeclaredInCurrentRequest_IsSuppressed(t *testing.T) {
	t.Parallel()
	h := runToolCall(t, []string{"Read", "Glob", "Grep"}, "write_undeclared_1", "Write", `{"file_path":"calculator.py","content":"print(1)"}`)
	assertToolCallSuppressed(t, h, "Write")
}

func TestSandboxMetadataReadToolCall_IsSuppressed(t *testing.T) {
	t.Parallel()
	h := runToolCall(t, []string{"Read", "Bash"}, "sandbox_meta_read", "Read", `{"file_path":"/tmp/cc-agent/sb1-demo/.claude/.claude.json"}`)
	assertToolCallSuppressed(t, h, "Read")
}

func TestTodoWriteToolCall_IsSuppressedWhenNotDeclared(t *testing.T) {
	t.Parallel()
	h := runToolCall(t, []string{"Read", "Write", "Edit", "Bash", "Glob", "Grep"}, "todo_1", "TodoWrite", `{"todos":[{"content":"Create calculator app with scientific notation support","status":"in_progress"}]}`)
	assertToolCallSuppressed(t, h, "TodoWrite")
}

func TestTaskToolCall_IsAcceptedWhenClientDeclaredAgent(t *testing.T) {
	t.Parallel()
	h := runToolCall(t, declaredToolNames([]interface{}{map[string]interface{}{"name": "Agent"}}), "task_1", "Task", `{"description":"Explore calculator codebase","prompt":"Find calculator files","subagent_type":"Explore"}`)
	assertToolCallSurvived(t, h, "Task")
}

func TestCustomMCPWebSearchToolCall_MapsToDeclaredWebSearch(t *testing.T) {
	t.Parallel()
	h := runToolCall(t, []string{"web_search"}, "ws_1", "mcp__tavily__web_search", `{"query":"Akron Ohio weather today March 29 2026 why so cold","timeRange":"day"}`)
	assertToolCallSurvived(t, h, "web_search")
}

func TestCustomMCPFetchToolCall_MapsToDeclaredWebFetch(t *testing.T) {
	t.Parallel()
	h := runToolCall(t, []string{"web_fetch"}, "wf_1", "mcp__fetch__fetch", `{"url":"https://example.com","max_length":4000}`)
	assertToolCallSurvived(t, h, "web_fetch")
}

func TestWebFetchToolCall_RewritesToDeclaredClientToolName(t *testing.T) {
	t.Parallel()

	h := newToolValidationHandler(t)
	h.setAllowedToolNames([]string{"web_fetch", "mcp__tavily__web_extract"})
	h.setClientTools([]interface{}{map[string]interface{}{"name": "mcp__tavily__web_extract"}})
	sendToolCall(h, "wf_2", "web_fetch", `{"url":"https://linux.do/t/topic/1872670"}`)
	sendToolFinish(h)

	assertToolCallSurvived(t, h, "mcp__tavily__web_extract")
}

func TestTaskToolCall_IsAcceptedWhenDelegatedToolsStayWithinAllowedSet(t *testing.T) {
	t.Parallel()
	h := runToolCall(t, []string{"Read"}, "task_1", "Task", `{"description":"Get weather","prompt":"Read weather skill","allowed_tools":["Read"]}`)
	assertToolCallSurvived(t, h, "Task")
}

func TestTaskToolCall_IsRejectedWhenDelegatedToolsExceedAllowedSet(t *testing.T) {
	t.Parallel()

	h := newToolValidationHandler(t)

	h.setAllowedToolNames([]string{"Read"})

	sendToolCall(h, "task_1", "Task", `{"description":"Get weather","prompt":"Run shell","allowed_tools":["Bash"]}`)
	sendToolFinish(h)

	testutil.Equal(t, len(h.contentBlocks), 0)
	testutil.NotEqual(t, h.suppressedToolCalls, 0)
}

func TestSkillToolCall_IsAcceptedWhenClientDeclaredSkill(t *testing.T) {
	t.Parallel()
	h := runToolCall(t, []string{"Skill"}, "skill_1", "Skill", `{"skill":"weather","args":"Yangzhou, China"}`)
	assertToolCallSurvived(t, h, "Skill")
}

func TestBashToolCallDifferentIDsSameCommand_Preserved(t *testing.T) {
	t.Parallel()

	h := newToolValidationHandler(t)

	input := `{"command":"rm /Users/dailin/Documents/GitHub/TEST/calculator.py"}`
	sendToolCall(h, "bash_id_1", "Bash", input)
	sendToolCall(h, "bash_id_2", "Bash", input)

	sendToolFinish(h)

	testutil.Equal(t, len(h.contentBlocks), 2)
	if got, _ := h.contentBlocks[0]["name"].(string); got != "Bash" {
		t.Fatalf("expected Bash tool call, got %q", got)
	}
}

func TestBashToolCallDifferentIDsDifferentCommands_BothAccepted(t *testing.T) {
	t.Parallel()

	h := newToolValidationHandler(t)

	sendToolCall(h, "bash_id_1", "Bash", `{"command":"pwd"}`)
	sendToolCall(h, "bash_id_2", "Bash", `{"command":"ls -la"}`)

	sendToolFinish(h)

	testutil.Equal(t, len(h.contentBlocks), 2)
}

func TestToolCallMissingID_IsSuppressed(t *testing.T) {
	t.Parallel()

	h := newToolValidationHandler(t)

	sendToolCallWithoutID(h, "Bash", `{"command":"pwd"}`)

	sendToolFinish(h)

	testutil.Equal(t, len(h.contentBlocks), 0)
}

// newToolValidationHandler builds the non-stream handler these tool-call
// validation tests assert against, and releases it when the test ends.
func newToolValidationHandler(t *testing.T) *streamHandler {
	t.Helper()
	h := newStreamHandler(
		&config.Config{},
		httptest.NewRecorder(),
		debug.New(false, false),
		false,
		false, // non-stream mode for easier assertions
		adapter.FormatAnthropic,
	)
	t.Cleanup(h.release)
	return h
}

// sendToolCall feeds one model.tool-call frame.
func sendToolCall(h *streamHandler, id, name, input string) {
	h.handleMessage(upstream.SSEMessage{
		Type: "model.tool-call",
		Event: map[string]interface{}{
			"toolCallId": id,
			"toolName":   name,
			"input":      input,
		},
	})
}

// sendToolCallWithoutID feeds a tool-call frame that carries no toolCallId.
func sendToolCallWithoutID(h *streamHandler, name, input string) {
	h.handleMessage(upstream.SSEMessage{
		Type: "model.tool-call",
		Event: map[string]interface{}{
			"toolName": name,
			"input":    input,
		},
	})
}

// sendToolFinish closes the turn with a tool_use finish reason.
func sendToolFinish(h *streamHandler) {
	h.handleMessage(upstream.SSEMessage{
		Type:  "model.finish",
		Event: map[string]interface{}{"finishReason": "tool_use"},
	})
}
