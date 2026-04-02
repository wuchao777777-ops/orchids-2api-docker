package bolt

import (
	"strings"

	"orchids-api/internal/prompt"
)

const maxBoltSystemTextChars = 2000

func buildSystemPromptParts(system []prompt.SystemItem, workdir string, tools []interface{}, noTools bool, messages []prompt.Message) systemPromptParts {
	parts := systemPromptParts{
		BasePrompt: buildMinimalAdapterPrompt(workdir),
		ToolPrompt: buildBoltToolPrompt(workdir, tools, noTools, messages),
	}

	custom := make([]string, 0, len(system))
	for _, item := range system {
		text := sanitizeBoltSystemText(item.Text)
		if strings.TrimSpace(text) != "" {
			custom = append(custom, text)
		}
	}
	parts.SystemPrompt = strings.Join(custom, "\n\n")

	combined := make([]string, 0, 3)
	for _, part := range []string{parts.BasePrompt, parts.ToolPrompt, parts.SystemPrompt} {
		if strings.TrimSpace(part) != "" {
			combined = append(combined, part)
		}
	}
	parts.FullPrompt = strings.Join(combined, "\n\n")
	return parts
}

func buildMinimalAdapterPrompt(workdir string) string {
	if strings.TrimSpace(workdir) == "" {
		return "若已有上下文足够且用户只是在询问信息，就直接回答；若用户请求需要实际读写文件、执行命令或搜索项目，必须返回对应的 JSON 工具调用来完成任务，不要把计划、文件内容摘要或口头说明当成完成。"
	}
	return "当前工作目录: " + workdir + "\n若已有上下文足够且用户只是在询问信息，就直接回答；若用户请求需要实际读写文件、执行命令或搜索项目，必须返回对应的 JSON 工具调用来完成任务，不要把计划、文件内容摘要或口头说明当成完成。"
}

func buildBoltToolPrompt(workdir string, tools []interface{}, noTools bool, messages []prompt.Message) string {
	if noTools {
		return "这次回合不要发起任何工具调用，只基于已有上下文和已返回的工具结果直接回答。"
	}

	toolNames := supportedBoltToolNames(tools)
	if len(toolNames) == 0 {
		return ""
	}

	toolHints := make([]string, 0, len(toolNames))
	for _, name := range toolNames {
		toolHints = append(toolHints, supportedToolHint(name))
	}

	parts := []string{
		"可用工具: " + strings.Join(toolHints, "; "),
		"工具调用只返回 JSON，例如 {\"tool\":\"Read\",\"parameters\":{\"file_path\":\"README.md\"}} 或 {\"tool_calls\":[...]}",
	}
	if hasSupportedBoltTool(toolNames, "Read") {
		parts = append(parts, "Read 只用于获取后续操作需要的上下文；除非用户明确要查看文件内容，否则不要在 Read 之后仅复述内容。")
	}
	if hasSupportedBoltTool(toolNames, "Edit") || hasSupportedBoltTool(toolNames, "Write") {
		parts = append(parts, "当用户要求修改、追加、创建或更新文件时，必须实际调用 Edit/Write 完成；如果已经通过 Read 拿到了足够上下文，就继续执行修改，不要只回答“文件内容是……”或要求用户重复文件内容。")
	}
	if hasSupportedBoltTool(toolNames, "Bash") {
		parts = append(parts, "当用户要求运行、构建、测试、提交或执行命令时，必须实际调用 Bash；不要只描述将要执行什么。")
	}
	return strings.Join(parts, "\n")
}

func buildBoltWorkspacePrompt(workdir string) []string {
	if strings.TrimSpace(workdir) == "" {
		return nil
	}
	return []string{"当前项目工作目录: " + workdir}
}

func supportedBoltToolNames(tools []interface{}) []string {
	if len(tools) == 0 {
		return nil
	}
	rawNames := make([]string, 0, len(tools))
	for _, raw := range tools {
		name := strings.TrimSpace(extractBoltToolName(raw))
		if name == "" {
			continue
		}
		rawNames = append(rawNames, name)
	}
	return FilterSupportedToolNames(rawNames)
}

func hasSupportedBoltTool(toolNames []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, name := range toolNames {
		if strings.EqualFold(strings.TrimSpace(name), want) {
			return true
		}
	}
	return false
}

func extractBoltToolName(raw interface{}) string {
	rawMap, ok := raw.(map[string]interface{})
	if !ok {
		return ""
	}
	if function, ok := rawMap["function"].(map[string]interface{}); ok {
		if name, ok := function["name"].(string); ok {
			return strings.TrimSpace(name)
		}
	}
	if name, ok := rawMap["name"].(string); ok {
		return strings.TrimSpace(name)
	}
	return ""
}

func sanitizeBoltSystemText(text string) string {
	text = stripTaggedBoltText(text)
	text = stripCCEntrypointLines(text)
	text = stripBoltEnvironmentLines(text)
	text = compactBoltSystemText(text)
	return text
}

func compactBoltSystemText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if looksLikeClaudeCodeBoilerplate(text) {
		text = stripClaudeCodeBoilerplate(text)
	}
	if looksLikeOpenClawSystemPrompt(text) {
		text = stripOpenClawBoilerplate(text)
	}
	return truncateBoltSystemText(text, maxBoltSystemTextChars)
}

func looksLikeClaudeCodeBoilerplate(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"you are an interactive agent that helps users with software engineering tasks",
		"# doing tasks",
		"# using your tools",
		"# auto memory",
		"# mcp server instructions",
		"the following mcp servers have provided instructions",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func stripClaudeCodeBoilerplate(text string) string {
	dropSections := map[string]bool{
		"# system": true, "# doing tasks": true, "# executing actions with care": true,
		"# using your tools": true, "# tone and style": true, "# output efficiency": true,
		"# auto memory": true, "## types of memory": true, "## what not to save in memory": true,
		"## how to save memories": true, "## when to access memories": true,
		"## before recommending from memory": true, "## memory and other forms of persistence": true,
		"# environment": true, "# committing changes with git": true,
		"# session-specific guidance": true,
	}
	suppressHeadings := map[string]bool{
		"# mcp server instructions": true, "## context7": true, "# vscode extension context": true,
	}
	isNoiseLine := func(lower string) bool {
		for _, marker := range []string{
			"anthropic's official cli for claude",
			"you are claude code",
			"you are an interactive cli tool",
			"you are an interactive agent that helps users with software engineering tasks",
			"claude agent sdk",
			"the system will automatically compress prior messages",
			"assistant knowledge cutoff",
			"the following mcp servers have provided instructions",
			"persistent, file-based memory system",
			"build up this memory system over time",
			"memory/",
			"askuserquestion",
			"the `!` prefix runs the command",
			"type `! ",
			"use the agent tool with",
			"subagent_type=",
			"/<skill-name>",
			"use the skill tool to execute them",
		} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
		return false
	}

	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	keepSection := true
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			lower := strings.ToLower(trimmed)
			if dropSections[lower] {
				keepSection = false
				continue
			}
			keepSection = true
			if suppressHeadings[lower] {
				continue
			}
			kept = append(kept, trimmed)
			continue
		}
		if !keepSection {
			continue
		}
		lower := strings.ToLower(trimmed)
		if !isNoiseLine(lower) {
			kept = append(kept, trimmed)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n")
}

func looksLikeOpenClawSystemPrompt(text string) bool {
	lower := strings.ToLower(text)
	markers := 0
	for _, marker := range []string{
		"you are a personal assistant running inside openclaw",
		"## skills (mandatory)",
		"## openclaw cli quick reference",
		"## current date & time",
		"## authorized senders",
		"# project context",
		"/workspace/agents.md",
		"/workspace/memory.md",
	} {
		if strings.Contains(lower, marker) {
			markers++
		}
	}
	return markers >= 2
}

func stripOpenClawBoilerplate(text string) string {
	dropSections := map[string]bool{
		"## tooling": true, "## openclaw cli quick reference": true,
		"## skills (mandatory)": true, "## openclaw self-update": true,
		"## reply tags": true, "## messaging": true, "### message tool": true,
		"## group chat context": true, "## inbound context (trusted metadata)": true,
		"# project context": true,
	}
	suppressHeadings := map[string]bool{
		"## authorized senders": true, "## current date & time": true,
		"## workspace": true, "## memory recall": true,
	}

	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	dropSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			lower := strings.ToLower(trimmed)
			if dropSections[lower] ||
				strings.HasPrefix(lower, "## /home/") ||
				strings.HasPrefix(lower, "# agents.md") ||
				strings.HasPrefix(lower, "# soul.md") ||
				strings.HasPrefix(lower, "# tools.md") ||
				strings.HasPrefix(lower, "# identity.md") ||
				strings.HasPrefix(lower, "# user.md") ||
				strings.HasPrefix(lower, "# heartbeat.md") {
				dropSection = true
				continue
			}
			dropSection = false
			if suppressHeadings[lower] {
				continue
			}
			kept = append(kept, trimmed)
			continue
		}
		if dropSection {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "<skill>") || strings.HasPrefix(lower, "</skill>") ||
			strings.HasPrefix(lower, "<name>") || strings.HasPrefix(lower, "<description>") ||
			strings.HasPrefix(lower, "<location>") || strings.HasPrefix(lower, "</description>") ||
			strings.HasPrefix(lower, "</location>") ||
			strings.Contains(lower, "/usr/lib/node_modules/openclaw/skills/") ||
			strings.Contains(lower, "~/.openclaw/extensions/") ||
			strings.Contains(lower, "~/.openclaw/workspace/") ||
			strings.Contains(lower, "/home/zhangdailin/.openclaw/workspace/") ||
			strings.Contains(lower, "openclaw gateway") ||
			strings.HasPrefix(lower, "tool availability (filtered by policy):") ||
			strings.HasPrefix(lower, "tool names are case-sensitive.") ||
			strings.HasPrefix(lower, "for acp harness thread spawns") ||
			strings.HasPrefix(lower, "do not poll `subagents list`") {
			continue
		}
		if strings.HasPrefix(lower, "- read:") || strings.HasPrefix(lower, "- write:") ||
			strings.HasPrefix(lower, "- edit:") || strings.HasPrefix(lower, "- exec:") ||
			strings.HasPrefix(lower, "- process:") || strings.HasPrefix(lower, "- web_search:") ||
			strings.HasPrefix(lower, "- message:") || strings.HasPrefix(lower, "- data_connectors:") ||
			strings.HasPrefix(lower, "- remember:") || strings.HasPrefix(lower, "- recall:") {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "\n")
}

func truncateBoltSystemText(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	if text == "" || maxChars <= 0 || len(text) <= maxChars {
		return text
	}
	if maxChars <= 200 {
		return strings.TrimSpace(text[:maxChars])
	}

	headChars := maxChars * 2 / 3
	tailChars := maxChars - headChars - len("\n[system context truncated]\n")
	if tailChars < 120 {
		tailChars = 120
		headChars = maxChars - tailChars - len("\n[system context truncated]\n")
	}
	if headChars < 120 {
		headChars = 120
	}
	if headChars+tailChars >= len(text) {
		return text
	}

	head := strings.TrimSpace(text[:headChars])
	tail := strings.TrimSpace(text[len(text)-tailChars:])
	return head + "\n[system context truncated]\n" + tail
}

// Legacy wrappers kept for test compatibility.

func buildBoltToolUsagePrompt(toolNames []string, messages []prompt.Message) []string {
	toolHints := make([]string, 0, len(toolNames))
	for _, name := range toolNames {
		toolHints = append(toolHints, supportedToolHint(name))
	}
	return []string{
		"可用工具: " + strings.Join(toolHints, "; "),
		"工具调用只返回 JSON，不要加解释。",
	}
}
