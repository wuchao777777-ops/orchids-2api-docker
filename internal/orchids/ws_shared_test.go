package orchids

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-json"

	"orchids-api/internal/prompt"
	"orchids-api/internal/tiktoken"
)

func TestFallbackOrchidsToolCallID(t *testing.T) {
	id1 := fallbackOrchidsToolCallID("Write", `{"file_path":"/tmp/a.txt","content":"x"}`)
	id2 := fallbackOrchidsToolCallID("write", `{"file_path":"/tmp/a.txt","content":"x"}`)
	if id1 == "" {
		t.Fatalf("expected non-empty fallback id")
	}
	if id1 != id2 {
		t.Fatalf("expected stable/lowercased id, got %q vs %q", id1, id2)
	}
	if len(id1) < len("orchids_anon_") || id1[:len("orchids_anon_")] != "orchids_anon_" {
		t.Fatalf("unexpected prefix: %q", id1)
	}
}

func TestFallbackOrchidsToolCallID_EmptyTool(t *testing.T) {
	if got := fallbackOrchidsToolCallID("", `{}`); got != "" {
		t.Fatalf("expected empty id for empty tool name, got %q", got)
	}
}

func legacyEstimateCompactedToolsTokens(tools []interface{}) int {
	compacted := compactIncomingTools(tools)
	if len(compacted) == 0 {
		return 0
	}
	raw, err := json.Marshal(compacted)
	if err != nil {
		return 0
	}
	return tiktoken.EstimateTextTokens(string(raw))
}

func sampleIncomingTools() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "Write",
				"description": "write file content safely",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"file_path": map[string]interface{}{"type": "string"},
						"content":   map[string]interface{}{"type": "string", "description": "utf-8 内容"},
					},
				},
			},
		},
		map[string]interface{}{
			"name":        "Read",
			"description": "read file content",
			"input_schema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"file_path": map[string]interface{}{"type": "string"},
				},
			},
		},
	}
}

func TestEstimateCompactedToolsTokensMatchesLegacy(t *testing.T) {
	tools := sampleIncomingTools()
	if got, want := EstimateCompactedToolsTokens(tools), legacyEstimateCompactedToolsTokens(tools); got != want {
		t.Fatalf("EstimateCompactedToolsTokens=%d want=%d", got, want)
	}
}

func BenchmarkEstimateCompactedToolsTokens_Legacy(b *testing.B) {
	tools := sampleIncomingTools()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = legacyEstimateCompactedToolsTokens(tools)
	}
}

func BenchmarkEstimateCompactedToolsTokens_Current(b *testing.B) {
	tools := sampleIncomingTools()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = EstimateCompactedToolsTokens(tools)
	}
}

func TestCompactIncomingTools_FiltersUnsupportedAndMinimizesSupportedSchemas(t *testing.T) {
	tools := []interface{}{
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "Agent",
				"description": strings.Repeat("unsupported", 40),
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"prompt": map[string]interface{}{"type": "string", "description": "very long prompt description"},
					},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "Bash",
				"description": strings.Repeat("shell command ", 30),
				"parameters": map[string]interface{}{
					"type":     "object",
					"required": []interface{}{"command", "ignored"},
					"properties": map[string]interface{}{
						"command":                   map[string]interface{}{"type": "string", "description": "command to run"},
						"description":               map[string]interface{}{"type": "string", "description": "user-facing summary"},
						"dangerouslyDisableSandbox": map[string]interface{}{"type": "boolean", "description": "disable sandbox"},
						"timeout":                   map[string]interface{}{"type": "number", "description": "milliseconds"},
						"ignored":                   map[string]interface{}{"type": "string", "description": "should be removed"},
					},
				},
			},
		},
		map[string]interface{}{
			"name":        "View",
			"description": "Read file contents with offsets",
			"input_schema": map[string]interface{}{
				"type":     "object",
				"required": []interface{}{"file_path", "ignored"},
				"properties": map[string]interface{}{
					"file_path": map[string]interface{}{"type": "string", "description": "path to read"},
					"offset":    map[string]interface{}{"type": "number", "description": "line offset"},
					"limit":     map[string]interface{}{"type": "number"},
					"ignored":   map[string]interface{}{"type": "string", "description": "remove me"},
				},
			},
		},
	}

	got := compactIncomingTools(tools)
	if len(got) != 2 {
		t.Fatalf("compactIncomingTools() len=%d want=2", len(got))
	}

	bashTool, ok := got[0].(map[string]interface{})
	if !ok {
		t.Fatalf("bash tool type = %T", got[0])
	}
	bashFn, ok := bashTool["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("bash function type = %T", bashTool["function"])
	}
	if gotName, _ := bashFn["name"].(string); gotName != "Bash" {
		t.Fatalf("bash function name = %q want %q", gotName, "Bash")
	}
	if desc, _ := bashFn["description"].(string); !strings.HasSuffix(desc, "...[truncated]") {
		t.Fatalf("bash description = %q, want truncated suffix", desc)
	}
	bashParams, ok := bashFn["parameters"].(map[string]interface{})
	if !ok {
		t.Fatalf("bash parameters type = %T", bashFn["parameters"])
	}
	bashProps, ok := bashParams["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("bash properties type = %T", bashParams["properties"])
	}
	for _, key := range []string{"command", "description", "dangerouslyDisableSandbox", "timeout"} {
		if _, exists := bashProps[key]; !exists {
			t.Fatalf("bash properties missing %q", key)
		}
	}
	if _, exists := bashProps["ignored"]; exists {
		t.Fatalf("bash properties unexpectedly kept ignored field")
	}
	if cmdSchema, ok := bashProps["command"].(map[string]interface{}); ok {
		if _, exists := cmdSchema["description"]; exists {
			t.Fatalf("bash command schema unexpectedly kept description")
		}
	}
	if required, ok := bashParams["required"].([]interface{}); ok {
		if len(required) != 1 || required[0] != "command" {
			t.Fatalf("bash required = %#v want [command]", required)
		}
	}

	viewTool, ok := got[1].(map[string]interface{})
	if !ok {
		t.Fatalf("view tool type = %T", got[1])
	}
	if gotName, _ := viewTool["name"].(string); gotName != "View" {
		t.Fatalf("view tool name = %q want %q", gotName, "View")
	}
	viewSchema, ok := viewTool["input_schema"].(map[string]interface{})
	if !ok {
		t.Fatalf("view schema type = %T", viewTool["input_schema"])
	}
	viewProps, ok := viewSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("view properties type = %T", viewSchema["properties"])
	}
	for _, key := range []string{"file_path", "offset", "limit"} {
		if _, exists := viewProps[key]; !exists {
			t.Fatalf("view properties missing %q", key)
		}
	}
	if _, exists := viewProps["ignored"]; exists {
		t.Fatalf("view properties unexpectedly kept ignored field")
	}
	if required, ok := viewSchema["required"].([]interface{}); ok {
		if len(required) != 1 || required[0] != "file_path" {
			t.Fatalf("view required = %#v want [file_path]", required)
		}
	}
}

func TestFormatToolResultContentLocalForHistory_RedactsJWT(t *testing.T) {
	jwt := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ0ZXN0Iiwic2NvcGUiOiJhZG1pbiJ9.c2lnbmF0dXJlLXRva2VuLXZhbHVl"
	got := formatToolResultContentLocalForHistory("token=" + jwt + "\nnext")
	if strings.Contains(got, jwt) {
		t.Fatalf("expected JWT to be redacted, got: %s", got)
	}
	if !strings.Contains(got, "[redacted_jwt]") {
		t.Fatalf("expected redaction marker, got: %s", got)
	}
}

func TestEstimateCompactedToolsTokens_IgnoresUnsupportedTools(t *testing.T) {
	supported := []interface{}{
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "Bash",
				"description": "run shell command",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
	}
	mixed := append([]interface{}{}, supported...)
	mixed = append(mixed, map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "Agent",
			"description": strings.Repeat("very expensive unsupported tool ", 100),
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"prompt": map[string]interface{}{"type": "string", "description": strings.Repeat("payload ", 100)},
				},
			},
		},
	})

	if got, want := EstimateCompactedToolsTokens(mixed), EstimateCompactedToolsTokens(supported); got != want {
		t.Fatalf("EstimateCompactedToolsTokens(mixed)=%d want %d", got, want)
	}
}

func TestFormatToolResultContentLocal_CompactsDirectoryListing(t *testing.T) {
	input := strings.Join([]string{
		"/Users/dailin/Documents/GitHub/TEST/.git/info/exclude",
		"/Users/dailin/Documents/GitHub/TEST/.git/hooks/pre-commit.sample",
		"/Users/dailin/Documents/GitHub/TEST/.git/objects/pack/pack-abc.pack",
		"/Users/dailin/Documents/GitHub/TEST/src/main.go",
		"/Users/dailin/Documents/GitHub/TEST/src/app.go",
		"/Users/dailin/Documents/GitHub/TEST/README.md",
		"/Users/dailin/Documents/GitHub/TEST/web/index.html",
		"/Users/dailin/Documents/GitHub/TEST/internal/api/server.go",
	}, "\n")

	got := formatToolResultContentLocal(input)
	if strings.Contains(got, "/.git/") {
		t.Fatalf("directory listing unexpectedly kept git metadata: %q", got)
	}
	for _, want := range []string{"./src/main.go", "./src/app.go", "./README.md"} {
		if !strings.Contains(got, want) {
			t.Fatalf("directory listing missing shortened path %q in %q", want, got)
		}
	}
	if !strings.Contains(got, "[directory listing trimmed:") {
		t.Fatalf("directory listing missing trim summary in %q", got)
	}
}

func TestFormatToolResultContentLocal_SummarizesNestedWorkspaceListing(t *testing.T) {
	input := strings.Join([]string{
		"/Users/dailin/Documents/GitHub/TEST/.DS_Store",
		"/Users/dailin/Documents/GitHub/TEST/.claude/settings.local.json",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/.git/info/exclude",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/.git/hooks/pre-commit.sample",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/.claude-plugin/PLUGIN_SCHEMA_NOTES.md",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/.claude-plugin/README.md",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/.claude-plugin/plugin.json",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/.github/workflows/ci.yml",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/.opencode/commands/build-fix.md",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/.opencode/commands/code-review.md",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/.opencode/plugins/index.ts",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/README.md",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/README.zh-CN.md",
		"/Users/dailin/Documents/GitHub/TEST/everything-claude-code/agents/architect.md",
		"/Users/dailin/Documents/GitHub/TEST/orchids_accounts.txt",
		"/Users/dailin/Documents/GitHub/TEST/test.py",
		"(Results are truncated. Consider using a more specific path or pattern.)",
	}, "\n")

	got := formatToolResultContentLocal(input)
	for _, want := range []string{
		"./.claude/ (sample: settings.local.json)",
		"./everything-claude-code/ (sample:",
		"./orchids_accounts.txt",
		"./test.py",
		"[directory listing summarized:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("workspace summary missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "/.git/") {
		t.Fatalf("workspace summary unexpectedly kept git metadata: %q", got)
	}
	if strings.Contains(got, ".DS_Store") {
		t.Fatalf("workspace summary unexpectedly kept OS metadata: %q", got)
	}
	if strings.Contains(got, "/Users/dailin/Documents/GitHub/TEST/everything-claude-code/") {
		t.Fatalf("workspace summary should not keep absolute nested paths: %q", got)
	}
}

func TestFormatToolResultContentLocal_CompactsBareLSListing(t *testing.T) {
	input := strings.Join([]string{
		".DS_Store",
		".claude",
		"everything-claude-code",
		"orchids_accounts.txt",
		"test.py",
	}, "\n")

	got := formatToolResultContentLocal(input)
	for _, want := range []string{".claude", "everything-claude-code", "orchids_accounts.txt", "test.py"} {
		if !strings.Contains(got, want) {
			t.Fatalf("bare ls listing missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, ".DS_Store") {
		t.Fatalf("bare ls listing unexpectedly kept OS metadata: %q", got)
	}
	if !strings.Contains(got, "[directory listing trimmed:") {
		t.Fatalf("bare ls listing missing trim summary in %q", got)
	}
}

func TestFormatToolResultContentLocal_ExpandsPersistedOutput(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".claude", "projects", "demo", "tool-results")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(base, "tool.txt")
	body := "1→import requests\n2→from fastapi import FastAPI\n3→app = FastAPI()"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	input := strings.Join([]string{
		"<persisted-output>",
		"Output too large (57.9KB). Full output saved to: " + path,
		"",
		"Preview (first 2KB):",
		"1→placeholder",
		"</persisted-output>",
	}, "\n")

	got := formatToolResultContentLocal(input)
	if got != body {
		t.Fatalf("expected persisted output body, got %q", got)
	}
}

func TestFormatToolResultContentLocalForHistory_SummarizesLongText(t *testing.T) {
	lines := make([]string, 0, 18)
	for i := 0; i < 18; i++ {
		lines = append(lines, "line "+strings.Repeat("x", 40))
	}
	got := formatToolResultContentLocalForHistory(strings.Join(lines, "\n"))
	if !strings.Contains(got, "[tool_result summary: omitted") {
		t.Fatalf("history tool result missing summary marker in %q", got)
	}
	if runeLen(got) > 900 {
		t.Fatalf("history tool result not compact enough: %d", runeLen(got))
	}
}

func TestResolveCurrentUserTurnText_ToolResultOnlyKeepsOriginalQuestion(t *testing.T) {
	messages := []prompt.Message{
		{
			Role: "user",
			Content: prompt.MessageContent{
				Blocks: []prompt.ContentBlock{
					{Type: "text", Text: "当前运行的目录是什么？"},
				},
			},
		},
		{
			Role: "assistant",
			Content: prompt.MessageContent{
				Blocks: []prompt.ContentBlock{
					{Type: "tool_use", Name: "Bash", Input: map[string]interface{}{"command": "pwd"}},
				},
			},
		},
		{
			Role: "user",
			Content: prompt.MessageContent{
				Blocks: []prompt.ContentBlock{
					{Type: "tool_result", ToolUseID: "tool-1", Content: "/Users/dailin/Documents/GitHub/TEST"},
				},
			},
		},
	}

	got := resolveCurrentUserTurnText(messages, 2, "/Users/dailin/Documents/GitHub/TEST")
	for _, want := range []string{
		"Original user request:",
		"当前运行的目录",
		"Tool result:",
		"/Users/dailin/Documents/GitHub/TEST",
		"Use the tool result above to answer the original user request directly.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("resolved current user text missing %q in %q", want, got)
		}
	}
}

func TestResolveCurrentUserTurnText_ToolResultOnlyIncludesToolProvenance(t *testing.T) {
	messages := []prompt.Message{
		{Role: "user", Content: prompt.MessageContent{Text: "帮我优化这个项目"}},
		{
			Role: "assistant",
			Content: prompt.MessageContent{
				Blocks: []prompt.ContentBlock{
					{Type: "tool_use", ID: "tool-1", Name: "Read", Input: map[string]interface{}{"file_path": "/Users/dailin/Documents/GitHub/truth_social_scraper/api.py"}},
					{Type: "tool_use", ID: "tool-2", Name: "Read", Input: map[string]interface{}{"file_path": "/Users/dailin/Documents/GitHub/truth_social_scraper/utils.py"}},
				},
			},
		},
		{
			Role: "user",
			Content: prompt.MessageContent{
				Blocks: []prompt.ContentBlock{
					{Type: "tool_result", ToolUseID: "tool-1", Content: "\"\"\"\nTruth Social Monitor API\n\"\"\"\nfrom fastapi import FastAPI"},
					{Type: "tool_result", ToolUseID: "tool-2", Content: "import json\nimport os"},
				},
			},
		},
	}

	got := resolveCurrentUserTurnText(messages, 2, "\"\"\"\nTruth Social Monitor API\n\"\"\"\nfrom fastapi import FastAPI\nimport json\nimport os")
	for _, want := range []string{
		"Original user request:",
		"帮我优化这个项目",
		"Tool result:",
		"[Read /Users/dailin/Documents/GitHub/truth_social_scraper/api.py]",
		"[Read /Users/dailin/Documents/GitHub/truth_social_scraper/utils.py]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("resolved current user text missing %q in %q", want, got)
		}
	}
}

func TestBuildToolResultFollowUpUserText_DirectoryListingAddsRootHint(t *testing.T) {
	toolResult := strings.Join([]string{
		"./.claude/ (sample: settings.local.json)",
		"./everything-claude-code/ (sample: README.md, .claude-plugin/plugin.json)",
		"./orchids_accounts.txt",
		"./test.py",
		"[directory listing summarized: 4 root entries from 16 lines; omitted 2 git metadata entries, 1 non-path lines, and 0 additional root entries]",
	}, "\n")

	got := buildToolResultFollowUpUserText("这个项目是干什么的", toolResult)
	if !strings.Contains(got, "Interpret the directory listing from the root entries first.") {
		t.Fatalf("follow-up prompt missing directory interpretation hint in %q", got)
	}
	if !strings.Contains(got, "Do not assume the largest nested subdirectory is the whole project.") {
		t.Fatalf("follow-up prompt missing nested directory warning in %q", got)
	}
	if !strings.Contains(got, "Ignore OS metadata like .DS_Store") {
		t.Fatalf("follow-up prompt missing OS metadata guidance in %q", got)
	}
	if !strings.Contains(got, "at most 2-3 short sentences") {
		t.Fatalf("follow-up prompt missing concise answer guidance in %q", got)
	}
	if !strings.Contains(got, "Do not enumerate every visible entry") {
		t.Fatalf("follow-up prompt missing enumeration guard in %q", got)
	}
}
