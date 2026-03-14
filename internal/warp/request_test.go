package warp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchids-api/internal/prompt"
)

func TestEstimateInputTokens_TracksWarpToolCost(t *testing.T) {
	t.Parallel()

	messages := []prompt.Message{
		{
			Role: "user",
			Content: prompt.MessageContent{
				Text: "当前目录",
			},
		},
	}

	noToolEstimate, err := EstimateInputTokens("", "auto-efficient", messages, nil, true)
	if err != nil {
		t.Fatalf("EstimateInputTokens without tools: %v", err)
	}
	if noToolEstimate.Total <= 0 {
		t.Fatalf("expected positive total without tools, got %+v", noToolEstimate)
	}
	if noToolEstimate.Profile != "warp-no-tools" {
		t.Fatalf("unexpected no-tool profile: %s", noToolEstimate.Profile)
	}

	withToolEstimate, err := EstimateInputTokens("", "auto-efficient", messages, []interface{}{
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "Bash",
				"description": strings.Repeat("Run shell command. ", 20),
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command":     map[string]interface{}{"type": "string", "description": "command"},
						"description": map[string]interface{}{"type": "string", "description": "reason"},
						"ignored":     map[string]interface{}{"type": "string", "description": "should be dropped"},
					},
					"required": []interface{}{"command", "ignored"},
				},
			},
		},
	}, false)
	if err != nil {
		t.Fatalf("EstimateInputTokens with tools: %v", err)
	}
	if withToolEstimate.Profile != "warp-tools" {
		t.Fatalf("unexpected tool profile: %s", withToolEstimate.Profile)
	}
	if withToolEstimate.ToolSchemaTokens <= 0 {
		t.Fatalf("expected tool schema tokens > 0, got %+v", withToolEstimate)
	}
	if withToolEstimate.Total <= noToolEstimate.Total {
		t.Fatalf("expected tools to increase total tokens: no_tools=%d with_tools=%d", noToolEstimate.Total, withToolEstimate.Total)
	}
}

func TestConvertTools_FiltersUnsupportedAndMinimizesSchema(t *testing.T) {
	t.Parallel()

	defs := convertTools([]interface{}{
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "Bash",
				"description": strings.Repeat("desc ", 100),
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command":     map[string]interface{}{"type": "string"},
						"description": map[string]interface{}{"type": "string"},
						"ignored":     map[string]interface{}{"type": "string"},
					},
					"required": []interface{}{"command", "ignored"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "Agent",
				"description": "unsupported",
				"parameters": map[string]interface{}{
					"type": "object",
				},
			},
		},
	})

	if len(defs) != 1 {
		t.Fatalf("expected only supported tool to remain, got %d defs", len(defs))
	}
	if defs[0].Name != "Bash" {
		t.Fatalf("expected Bash, got %s", defs[0].Name)
	}
	if len([]rune(defs[0].Description)) > maxWarpToolDescLen {
		t.Fatalf("description not compacted: len=%d", len([]rune(defs[0].Description)))
	}
	props, ok := defs[0].Schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected schema properties, got %#v", defs[0].Schema["properties"])
	}
	if _, ok := props["command"]; !ok {
		t.Fatalf("expected command property to remain")
	}
	if _, ok := props["ignored"]; ok {
		t.Fatalf("expected ignored property to be dropped")
	}
	req, ok := defs[0].Schema["required"].([]interface{})
	if !ok || len(req) != 1 || req[0] != "command" {
		t.Fatalf("expected required to keep only command, got %#v", defs[0].Schema["required"])
	}
}

func TestBuildWarpQuery_CompactsDirectoryToolResults(t *testing.T) {
	t.Parallel()

	listing := strings.Join([]string{
		"/Users/dailin/Documents/GitHub/TEST/.DS_Store",
		"/Users/dailin/Documents/GitHub/TEST/.claude/settings.local.json",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/.git/config",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/README.md",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/src/main.ts",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/src/commands/build.ts",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/src/commands/review.ts",
		"/Users/dailin/Documents/GitHub/TEST/orchids_accounts.txt",
		"/Users/dailin/Documents/GitHub/TEST/test.py",
		"/Users/dailin/Documents/GitHub/TEST/tmp/cache/data.json",
		"/Users/dailin/Documents/GitHub/TEST/tmp/cache/index.json",
		"/Users/dailin/Documents/GitHub/TEST/tmp/cache/log.txt",
	}, "\n")

	query, isNew := buildWarpQuery("这个项目是干什么的", nil, []warpToolResult{{
		ToolCallID: "tool_1",
		Content:    listing,
	}}, true, "")
	if isNew {
		t.Fatalf("expected tool-result follow-up to reuse conversation context")
	}
	if strings.Contains(query, ".DS_Store") {
		t.Fatalf("directory summary unexpectedly kept .DS_Store: %q", query)
	}
	if strings.Contains(query, "orchids_accounts.txt") {
		t.Fatalf("directory summary unexpectedly kept sensitive token file: %q", query)
	}
	if strings.Contains(query, "/.git/") {
		t.Fatalf("directory summary unexpectedly kept git metadata: %q", query)
	}
	if !strings.Contains(query, "[directory listing summarized:") && !strings.Contains(query, "[directory listing trimmed:") {
		t.Fatalf("expected directory summary marker in %q", query)
	}
	if !strings.Contains(query, "./everything-claude-code/") {
		t.Fatalf("expected top-level directory summary in %q", query)
	}
	if !strings.Contains(query, "RESPONSE RULES:") {
		t.Fatalf("expected base response rules in %q", query)
	}
	if !strings.Contains(query, "TOOL RULES:") {
		t.Fatalf("expected tool rules in %q", query)
	}
}

func TestBuildWarpQuery_CompactsLsLongDirectoryToolResults(t *testing.T) {
	t.Parallel()

	listing := strings.Join([]string{
		"drwxr-xr-x@ 15 dailin  staff    480 Mar  7 20:26 .",
		"drwxr-xr-x@  7 dailin  staff    224 Mar 10 21:26 ..",
		"drwxr-xr-x@ 12 dailin  staff    384 Mar  5 21:41 .git",
		"drwxr-xr-x@  9 dailin  staff    288 Mar  5 22:27 .venv",
		"-rw-r--r--@  1 dailin  staff   7191 Mar  5 21:41 README.md",
		"-rw-r--r--@  1 dailin  staff  54313 Mar  5 21:56 api.py",
		"-rw-r--r--@  1 dailin  staff    401 Mar  5 21:41 requirements.txt",
		"drwxr-xr-x@ 15 dailin  staff    480 Mar  7 20:26 web-ui",
	}, "\n")

	query, isNew := buildWarpQuery("帮我优化这个项目", nil, []warpToolResult{{
		ToolCallID: "tool_1",
		Content:    listing,
	}}, true, "")
	if isNew {
		t.Fatalf("expected tool-result follow-up to reuse conversation context")
	}
	if strings.Contains(query, "drwxr-xr-x") || strings.Contains(query, "-rw-r--r--") {
		t.Fatalf("expected ls -la metadata to be compacted out: %q", query)
	}
	if strings.Contains(query, "./.git") {
		t.Fatalf("expected git metadata to be dropped: %q", query)
	}
	if !strings.Contains(query, "README.md") || !strings.Contains(query, "web-ui") {
		t.Fatalf("expected top-level entries to remain: %q", query)
	}
}

func TestBuildWarpQuery_FollowupStripsHistoricalToolCalls(t *testing.T) {
	t.Parallel()

	query, isNew := buildWarpQuery(
		"这个项目是干什么的",
		[]warpHistoryMessage{
			{Role: "user", Content: "这个项目是干什么的"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []warpToolCall{{
					ID:        "call_1",
					Name:      "Read",
					Arguments: `{"file_path":"README.md"}`,
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "README content"},
		},
		[]warpToolResult{{
			ToolCallID: "call_2",
			Content:    "./README.md\n./main.go",
			ToolName:   "Glob",
			Arguments:  `{"path":".","pattern":"*"}`,
		}},
		true,
		"",
	)
	if isNew {
		t.Fatalf("expected follow-up query, got new conversation")
	}
	if strings.Contains(query, "<tool_use id=\"call_1\"") {
		t.Fatalf("expected historical tool_use to be stripped from follow-up query: %q", query)
	}
	if strings.Contains(query, "<tool_result tool_call_id=\"call_1\">") {
		t.Fatalf("expected historical tool_result to be stripped from follow-up query: %q", query)
	}
	if !strings.Contains(query, "User: 这个项目是干什么的") {
		t.Fatalf("expected original user intent to remain in follow-up query: %q", query)
	}
	if !strings.Contains(query, "<tool_result id=\"call_2\">") {
		t.Fatalf("expected current tool_result to remain in follow-up query: %q", query)
	}
	if !strings.Contains(query, "<tool_use id=\"call_2\" name=\"Glob\">") {
		t.Fatalf("expected current tool provenance to remain in follow-up query: %q", query)
	}
}

func TestBuildWarpQuery_FollowupPrunesExploratoryAssistantPrefaces(t *testing.T) {
	t.Parallel()

	query, isNew := buildWarpQuery(
		"",
		[]warpHistoryMessage{
			{Role: "user", Content: "帮我优化一下这个项目"},
			{Role: "assistant", Content: "Let me first do a thorough review of the codebase to identify optimization opportunities."},
			{Role: "assistant", Content: "api.py exposes FastAPI routes and also performs background analysis work."},
		},
		[]warpToolResult{{
			ToolCallID: "tool_1",
			Content:    "from fastapi import FastAPI",
		}},
		true,
		"",
	)
	if isNew {
		t.Fatalf("expected tool-result follow-up to stay in follow-up mode")
	}
	if strings.Contains(query, "Let me first do a thorough review") {
		t.Fatalf("expected exploratory assistant preface to be pruned from follow-up history, got %q", query)
	}
	if !strings.Contains(query, "api.py exposes FastAPI routes") {
		t.Fatalf("expected meaningful assistant history to remain, got %q", query)
	}
}

func TestBuildWarpQuery_InterpretsAbstractOptimizationAsCurrentProject(t *testing.T) {
	t.Parallel()

	query, isNew := buildWarpQuery("帮我优化这个方案", nil, nil, false, "/Users/dailin/Documents/GitHub/truth_social_scraper")
	if !isNew {
		t.Fatalf("expected initial request to remain a new conversation")
	}
	if !strings.Contains(query, "Treat \"this plan/design/implementation\" as the current local project/codebase") {
		t.Fatalf("expected current-project interpretation hint, got %q", query)
	}
	if !strings.Contains(query, "/Users/dailin/Documents/GitHub/truth_social_scraper") {
		t.Fatalf("expected working directory in interpretation hint, got %q", query)
	}
}

func TestBuildWarpQuery_InterpretsAbstractOptimizationWithoutWorkdir(t *testing.T) {
	t.Parallel()

	query, isNew := buildWarpQuery("帮我优化这个方案", nil, nil, false, "")
	if !isNew {
		t.Fatalf("expected initial request to remain a new conversation")
	}
	if !strings.Contains(query, "Treat \"this plan/design/implementation\" as the current local project/codebase.") {
		t.Fatalf("expected current-project interpretation hint without workdir, got %q", query)
	}
	if !strings.Contains(query, "inspect the current local repository with client tools") {
		t.Fatalf("expected client-tool inspection hint, got %q", query)
	}
}

func TestExtractWarpConversation_LastUserTextAndToolResultRemainCurrent(t *testing.T) {
	t.Parallel()

	userText, history, toolResults, err := extractWarpConversation([]prompt.Message{
		{
			Role: "assistant",
			Content: prompt.MessageContent{
				Blocks: []prompt.ContentBlock{{
					Type:  "tool_use",
					ID:    "tool_1",
					Name:  "Glob",
					Input: map[string]interface{}{"path": "/tmp", "pattern": "*"},
				}},
			},
		},
		{
			Role: "user",
			Content: prompt.MessageContent{
				Blocks: []prompt.ContentBlock{
					{Type: "tool_result", ToolUseID: "tool_1", Content: "./README.md\n./main.go"},
					{Type: "text", Text: "这个项目是干什么的"},
				},
			},
		},
	}, "")
	if err != nil {
		t.Fatalf("extractWarpConversation: %v", err)
	}
	if userText != "这个项目是干什么的" {
		t.Fatalf("unexpected userText: %q", userText)
	}
	if len(toolResults) != 1 {
		t.Fatalf("expected current tool_result, got %d", len(toolResults))
	}
	if toolResults[0].ToolName != "Glob" {
		t.Fatalf("expected tool provenance to be preserved, got %#v", toolResults[0])
	}
	if !strings.Contains(toolResults[0].Arguments, `"pattern":"*"`) {
		t.Fatalf("expected tool arguments to be preserved, got %#v", toolResults[0])
	}
	if len(history) != 1 || history[0].Role != "assistant" {
		t.Fatalf("expected only assistant tool_use in history, got %#v", history)
	}
}

func TestExtractWarpConversation_ToolResultOnlyCurrentTurnDoesNotReuseOlderUserText(t *testing.T) {
	t.Parallel()

	userText, history, toolResults, err := extractWarpConversation([]prompt.Message{
		{
			Role: "user",
			Content: prompt.MessageContent{
				Text: "帮我优化一下这个项目",
			},
		},
		{
			Role: "assistant",
			Content: prompt.MessageContent{
				Blocks: []prompt.ContentBlock{
					{
						Type:  "tool_use",
						ID:    "tool_api",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": "api.py"},
					},
					{
						Type:  "tool_use",
						ID:    "tool_utils",
						Name:  "Read",
						Input: map[string]interface{}{"file_path": "utils.py"},
					},
				},
			},
		},
		{
			Role: "user",
			Content: prompt.MessageContent{
				Blocks: []prompt.ContentBlock{
					{Type: "tool_result", ToolUseID: "tool_api", Content: "from fastapi import FastAPI"},
					{Type: "tool_result", ToolUseID: "tool_utils", Content: "import json"},
				},
			},
		},
	}, "")
	if err != nil {
		t.Fatalf("extractWarpConversation: %v", err)
	}
	if userText != "" {
		t.Fatalf("expected current tool_result-only turn to keep empty userText, got %q", userText)
	}
	if len(toolResults) != 2 {
		t.Fatalf("expected 2 current tool results, got %d", len(toolResults))
	}
	if len(history) != 2 {
		t.Fatalf("expected prior user question and assistant tool_use in history, got %#v", history)
	}
	if history[0].Role != "user" || history[0].Content != "帮我优化一下这个项目" {
		t.Fatalf("expected original user request to stay in history, got %#v", history)
	}
	if history[1].Role != "assistant" || len(history[1].ToolCalls) != 2 {
		t.Fatalf("expected assistant tool calls to stay in history, got %#v", history)
	}
}

func TestEstimateInputTokens_TreatsLastUserToolResultAsFollowup(t *testing.T) {
	t.Parallel()

	estimate, err := EstimateInputTokens("", "auto-efficient", []prompt.Message{
		{
			Role: "assistant",
			Content: prompt.MessageContent{
				Blocks: []prompt.ContentBlock{{
					Type:  "tool_use",
					ID:    "tool_1",
					Name:  "Glob",
					Input: map[string]interface{}{"path": "/tmp", "pattern": "*"},
				}},
			},
		},
		{
			Role: "user",
			Content: prompt.MessageContent{
				Blocks: []prompt.ContentBlock{
					{Type: "tool_result", ToolUseID: "tool_1", Content: "./README.md\n./main.go"},
					{Type: "text", Text: "这个项目是干什么的"},
				},
			},
		},
	}, nil, true)
	if err != nil {
		t.Fatalf("EstimateInputTokens: %v", err)
	}
	if estimate.Profile != "warp-tool-result" {
		t.Fatalf("expected warp-tool-result profile, got %+v", estimate)
	}
}

func TestExtractWarpConversation_SkipsSyntheticClaudeContextUserMessage(t *testing.T) {
	t.Parallel()

	userText, history, toolResults, err := extractWarpConversation([]prompt.Message{
		{
			Role: "user",
			Content: prompt.MessageContent{
				Text: "You are an interactive agent that helps users with software engineering tasks.\n# auto memory\n# Environment\n - Primary working directory: /Users/dailin/Documents/GitHub/truth_social_scraper\ngitStatus:\nCurrent branch: main",
			},
		},
		{
			Role: "user",
			Content: prompt.MessageContent{
				Text: "这个项目使用了哪些技术架构",
			},
		},
	}, "")
	if err != nil {
		t.Fatalf("extractWarpConversation: %v", err)
	}
	if userText != "这个项目使用了哪些技术架构" {
		t.Fatalf("unexpected userText: %q", userText)
	}
	if len(history) != 0 {
		t.Fatalf("expected synthetic context to be dropped from history, got %#v", history)
	}
	if len(toolResults) != 0 {
		t.Fatalf("expected no tool results, got %#v", toolResults)
	}
}

func TestFormatWarpHistory_CompactsHistoricalToolResults(t *testing.T) {
	t.Parallel()

	var lines []string
	for i := 0; i < 18; i++ {
		lines = append(lines, "line "+strings.Repeat("x", 30))
	}
	history := []warpHistoryMessage{{
		Role:       "tool",
		ToolCallID: "tool_2",
		Content:    strings.Join(lines, "\n"),
	}}

	parts := formatWarpHistory(history)
	if len(parts) != 1 {
		t.Fatalf("expected one history entry, got %d", len(parts))
	}
	if !strings.Contains(parts[0], "[tool_result summary: omitted") {
		t.Fatalf("expected historical tool_result summary marker in %q", parts[0])
	}
	if strings.Count(parts[0], "line ") >= len(lines) {
		t.Fatalf("expected historical tool_result to be compacted, got %q", parts[0])
	}
}

func TestExtractWarpConversation_ExpandsPersistedToolResult(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	toolResultPath := filepath.Join(tmp, ".claude", "projects", "session", "tool-results", "toolu_1.txt")
	if err := os.MkdirAll(filepath.Dir(toolResultPath), 0o755); err != nil {
		t.Fatalf("mkdir tool results: %v", err)
	}

	fullContent := strings.Join([]string{
		"1→import os",
		"2→import json",
		"3→from fastapi import FastAPI",
		"4→app = FastAPI()",
		"5→",
		"40→@app.get(\"/health\")",
		"41→def health():",
		"42→    return {\"ok\": True}",
	}, "\n")
	if err := os.WriteFile(toolResultPath, []byte(fullContent), 0o644); err != nil {
		t.Fatalf("write tool result: %v", err)
	}

	placeholder := strings.Join([]string{
		"<persisted-output>",
		"Output too large (57.9KB). Full output saved to: " + toolResultPath,
		"",
		"Preview (first 2KB):",
		"1→import os",
		"2→import json",
		"...",
		"</persisted-output>",
	}, "\n")

	_, _, toolResults, err := extractWarpConversation([]prompt.Message{
		{Role: "user", Content: prompt.MessageContent{Text: "帮我优化这个项目"}},
		{
			Role: "assistant",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{{
				Type:  "tool_use",
				ID:    "tool_1",
				Name:  "Read",
				Input: map[string]interface{}{"file_path": "api.py"},
			}}},
		},
		{
			Role: "user",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{{
				Type:      "tool_result",
				ToolUseID: "tool_1",
				Content:   placeholder,
			}}},
		},
	}, "")
	if err != nil {
		t.Fatalf("extractWarpConversation: %v", err)
	}
	if len(toolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(toolResults))
	}
	if strings.Contains(toolResults[0].Content, "Output too large") {
		t.Fatalf("expected persisted-output wrapper to be expanded, got %q", toolResults[0].Content)
	}
	if !strings.Contains(toolResults[0].Content, `40→@app.get("/health")`) {
		t.Fatalf("expected expanded content to include later lines, got %q", toolResults[0].Content)
	}
}

func TestCompactWarpToolResultContent_UsesStructuralCodeLinesForReadOutput(t *testing.T) {
	t.Parallel()

	lines := make([]string, 0, 80)
	for i := 1; i <= 80; i++ {
		switch i {
		case 1:
			lines = append(lines, "1→import os")
		case 2:
			lines = append(lines, "2→import json")
		case 30:
			lines = append(lines, `30→@app.get("/health")`)
		case 31:
			lines = append(lines, "31→def health():")
		case 60:
			lines = append(lines, "60→class Service:")
		case 61:
			lines = append(lines, "61→    def run(self):")
		default:
			lines = append(lines, fmt.Sprintf("%d→value_%d = %d // %s", i, i, i, strings.Repeat("x", 48)))
		}
	}

	compacted := compactWarpToolResultContent(strings.Join(lines, "\n"), false)
	if !strings.Contains(compacted, `30→@app.get("/health")`) {
		t.Fatalf("expected route definition to survive compaction, got %q", compacted)
	}
	if !strings.Contains(compacted, "60→class Service:") {
		t.Fatalf("expected later class definition to survive compaction, got %q", compacted)
	}
	if !strings.Contains(compacted, "[read output summary: omitted") {
		t.Fatalf("expected read-output summary markers, got %q", compacted)
	}
}

func TestExtractWarpConversation_DedupsRepeatedLargeReadToolResults(t *testing.T) {
	t.Parallel()

	lines := make([]string, 0, 64)
	for i := 1; i <= 64; i++ {
		switch i {
		case 1:
			lines = append(lines, "1→import os")
		case 20:
			lines = append(lines, "20→@app.get(\"/health\")")
		case 21:
			lines = append(lines, "21→def health():")
		default:
			lines = append(lines, fmt.Sprintf("%d→value_%d = %d", i, i, i))
		}
	}
	largeContent := strings.Join(lines, "\n")

	_, _, toolResults, err := extractWarpConversation([]prompt.Message{
		{Role: "user", Content: prompt.MessageContent{Text: "帮我优化这个项目"}},
		{
			Role: "assistant",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{{
				Type:  "tool_use",
				ID:    "tool_1",
				Name:  "Read",
				Input: map[string]interface{}{"file_path": "api.py"},
			}}},
		},
		{
			Role: "user",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{{
				Type:      "tool_result",
				ToolUseID: "tool_1",
				Content:   largeContent,
			}}},
		},
		{
			Role: "assistant",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{{
				Type:  "tool_use",
				ID:    "tool_2",
				Name:  "Read",
				Input: map[string]interface{}{"file_path": "api.py"},
			}}},
		},
		{
			Role: "user",
			Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{{
				Type:      "tool_result",
				ToolUseID: "tool_2",
				Content:   largeContent,
			}}},
		},
	}, "")
	if err != nil {
		t.Fatalf("extractWarpConversation: %v", err)
	}
	if len(toolResults) != 1 {
		t.Fatalf("expected repeated large read output to be deduped, got %d results", len(toolResults))
	}
}
