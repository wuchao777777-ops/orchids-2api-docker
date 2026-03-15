package orchids

import (
	"strings"

	"github.com/goccy/go-json"
)

type canonicalToolMapper struct {
	toOrchids   map[string]string
	fromOrchids map[string]string
}

// ToolMapper mirrors CodeFreeMax's per-request client tool index.
type ToolMapper struct {
	Tools []map[string]interface{}
	index map[string]map[string]interface{}
}

// NormalizedTool mirrors CodeFreeMax's tool-name normalization tuple.
type NormalizedTool struct {
	Original  string
	Lowercase string
	SnakeCase string
}

// DefaultToolMapper remains the thin repo-compatibility bridge used outside the Orchids stream path.
var DefaultToolMapper = newCanonicalToolMapper()

func newCanonicalToolMapper() *canonicalToolMapper {
	tm := &canonicalToolMapper{
		toOrchids:   make(map[string]string),
		fromOrchids: make(map[string]string),
	}

	tm.addMapping("Str_Replace_Editor", "Edit")
	tm.addMapping("str_replace_editor", "Edit")
	tm.addMapping("View", "Read")
	tm.addMapping("view", "Read")
	tm.addMapping("ReadFile", "Read")
	tm.addMapping("read_file", "Read")
	tm.addMapping("ListDir", "Glob")
	tm.addMapping("list_dir", "Glob")
	tm.addMapping("list_directory", "Glob")
	tm.addMapping("LS", "Glob")
	tm.addMapping("RipGrepTool", "Grep")
	tm.addMapping("ripgrep", "Grep")
	tm.addMapping("search_code", "Grep")
	tm.addMapping("SearchCode", "Grep")
	tm.addMapping("GlobTool", "Glob")
	tm.addMapping("glob", "Glob")
	tm.addMapping("find_files", "Glob")
	tm.addMapping("Execute", "Bash")
	tm.addMapping("execute", "Bash")
	tm.addMapping("execute_command", "Bash")
	tm.addMapping("execute-command", "Bash")
	tm.addMapping("run_command", "Bash")
	tm.addMapping("runcommand", "Bash")
	tm.addMapping("RunCommand", "Bash")
	tm.addMapping("launch-process", "Bash")
	tm.addMapping("WriteFile", "Write")
	tm.addMapping("write_file", "Write")
	tm.addMapping("CreateFile", "Write")
	tm.addMapping("create_file", "Write")
	tm.addMapping("save-file", "Write")
	tm.addMapping("Write", "Write")

	tm.addMapping("run_shell_command", "Bash")
	tm.addMapping("write_to_long_running_shell_command", "Bash")
	tm.addMapping("search_codebase", "Grep")
	tm.addMapping("grep", "Grep")
	tm.addMapping("file_glob", "Glob")
	tm.addMapping("file_glob_v2", "Glob")
	tm.addMapping("read_files", "Read")
	tm.addMapping("apply_file_diffs", "Edit")

	tm.addMapping("update_todo_list", "TodoWrite")
	tm.addMapping("todo", "TodoWrite")
	tm.addMapping("todo_write", "TodoWrite")
	tm.addMapping("todowrite", "TodoWrite")
	tm.addMapping("ask_followup_question", "AskUserQuestion")
	tm.addMapping("ask", "AskUserQuestion")
	tm.addMapping("enter_plan_mode", "EnterPlanMode")
	tm.addMapping("exit_plan_mode", "ExitPlanMode")
	tm.addMapping("new_task", "Task")
	tm.addMapping("task_output", "TaskOutput")
	tm.addMapping("task_stop", "TaskStop")
	tm.addMapping("use_skill", "Skill")
	tm.addMapping("skill", "Skill")

	tm.addMapping("web_fetch", "WebFetch")
	tm.addMapping("webfetch", "WebFetch")
	tm.addMapping("fetch", "WebFetch")

	tm.addMapping("query-docs", "mcp__context7__query-docs")
	tm.addMapping("resolve-library-id", "mcp__context7__resolve-library-id")
	tm.addMapping("mcp__context7__query-docs", "mcp__context7__query-docs")
	tm.addMapping("mcp__context7__resolve-library-id", "mcp__context7__resolve-library-id")

	tm.addCanonical("Bash")
	tm.addCanonical("Read")
	tm.addCanonical("Edit")
	tm.addCanonical("Write")
	tm.addCanonical("Glob")
	tm.addCanonical("Grep")
	tm.addCanonical("TodoWrite")
	tm.addCanonical("AskUserQuestion")
	tm.addCanonical("EnterPlanMode")
	tm.addCanonical("ExitPlanMode")
	tm.addCanonical("Task")
	tm.addCanonical("TaskOutput")
	tm.addCanonical("TaskStop")
	tm.addCanonical("Skill")
	tm.addCanonical("WebFetch")
	tm.addCanonical("mcp__context7__query-docs")
	tm.addCanonical("mcp__context7__resolve-library-id")

	return tm
}

func (tm *canonicalToolMapper) addMapping(from, to string) {
	tm.toOrchids[from] = to
	tm.toOrchids[strings.ToLower(from)] = to
}

func (tm *canonicalToolMapper) addCanonical(name string) {
	if strings.TrimSpace(name) == "" {
		return
	}
	tm.fromOrchids[strings.ToLower(name)] = name
}

func (tm *canonicalToolMapper) ToOrchids(name string) string {
	if mapped, ok := tm.toOrchids[name]; ok {
		return mapped
	}
	if mapped, ok := tm.toOrchids[strings.ToLower(strings.TrimSpace(name))]; ok {
		return mapped
	}
	return name
}

func (tm *canonicalToolMapper) FromOrchids(name string) string {
	if mapped, ok := tm.fromOrchids[strings.ToLower(strings.TrimSpace(name))]; ok {
		return mapped
	}
	return name
}

func (tm *canonicalToolMapper) IsBlocked(string) bool {
	return false
}

func buildClientToolMapper(clientTools []interface{}) *ToolMapper {
	tools := toolMapsFromInterfaces(clientTools)
	if len(tools) == 0 {
		return nil
	}
	tm := &ToolMapper{Tools: tools}
	tm.buildIndex()
	return tm
}

func (tm *ToolMapper) buildIndex() {
	if tm == nil {
		return
	}
	tm.index = make(map[string]map[string]interface{}, len(tm.Tools))
	for _, tool := range tm.Tools {
		name := toolSpecName(tool)
		if name == "" {
			continue
		}
		tm.index[strings.ToLower(name)] = tool
	}
}

func NormalizeToolName(name string) string {
	return DefaultToolMapper.ToOrchids(name)
}

func normalizeToolMatchName(name string) *NormalizedTool {
	name = strings.TrimSpace(name)
	if name == "" {
		return &NormalizedTool{}
	}
	lower := strings.ToLower(name)
	return &NormalizedTool{
		Original:  name,
		Lowercase: lower,
		SnakeCase: toSnakeCase(lower),
	}
}

func MapToolNameToClient(orchidsName string, clientTools []interface{}, toolMapper *ToolMapper) string {
	normalized := normalizeToolMatchName(orchidsName)
	if normalized.Original == "" {
		return orchidsName
	}

	tools := toolMapperClientTools(clientTools, toolMapper)
	if len(tools) == 0 {
		return MapOrchidsToolToAnthropic(orchidsName)
	}

	for _, tool := range tools {
		name := toolSpecName(tool)
		if name == normalized.Original {
			return name
		}
	}

	for _, tool := range tools {
		name := toolSpecName(tool)
		if name == "" {
			continue
		}
		if strings.ToLower(name) == normalized.Lowercase {
			return name
		}
	}

	for _, tool := range tools {
		name := toolSpecName(tool)
		if name == "" {
			continue
		}
		if toSnakeCase(strings.ToLower(name)) == normalized.SnakeCase {
			return name
		}
	}

	if aliases, ok := orchidsToolAliases[normalized.SnakeCase]; ok {
		for _, tool := range tools {
			name := toolSpecName(tool)
			if name == "" {
				continue
			}
			toolSnake := toSnakeCase(strings.ToLower(name))
			for _, alias := range aliases {
				if toolSnake == alias || strings.ToLower(name) == alias {
					return name
				}
			}
		}
	}

	for _, tool := range tools {
		name := toolSpecName(tool)
		if name == "" {
			continue
		}
		toolSnake := toSnakeCase(strings.ToLower(name))
		if aliases, ok := orchidsToolAliases[toolSnake]; ok {
			for _, alias := range aliases {
				if alias == normalized.SnakeCase || alias == normalized.Lowercase {
					return name
				}
			}
		}
	}

	if aliases, ok := orchidsToolAliases[normalized.SnakeCase]; ok {
		for _, tool := range tools {
			name := toolSpecName(tool)
			if name == "" {
				continue
			}
			for _, alias := range aliases {
				if strings.EqualFold(name, alias) {
					return name
				}
			}
		}
	}

	if toolMapper != nil && len(toolMapper.Tools) > 0 {
		type candidate struct {
			name       string
			matchCount int
			distance   int
		}

		aliasKeys := collectAliasKeys(toolMapper)
		var candidates []candidate

		for _, tool := range tools {
			name := toolSpecName(tool)
			if name == "" {
				continue
			}
			toolAliases := getToolAliases(tool)
			if len(toolAliases) == 0 {
				continue
			}

			matchCount := 0
			for _, key := range aliasKeys {
				if _, found := toolMapper.index[key]; found {
					break
				}
				matchCount++
			}
			if matchCount < len(aliasKeys) || len(aliasKeys) == 0 {
				continue
			}

			candidates = append(candidates, candidate{
				name:       name,
				matchCount: matchCount,
				distance:   len(toolAliases) - matchCount,
			})
		}

		if len(candidates) > 0 {
			best := candidates[0]
			for _, candidate := range candidates[1:] {
				if candidate.matchCount > best.matchCount ||
					(candidate.matchCount == best.matchCount && candidate.distance < best.distance) {
					best = candidate
				}
			}
			return best.name
		}
	}

	// This last bridge is intentionally repo-local: our client tool names are not Orchids-native.
	canonical := strings.TrimSpace(DefaultToolMapper.ToOrchids(normalized.Original))
	if canonical != "" {
		for _, tool := range tools {
			name := toolSpecName(tool)
			if name == "" {
				continue
			}
			if strings.EqualFold(DefaultToolMapper.ToOrchids(name), canonical) {
				return name
			}
		}
	}

	return MapOrchidsToolToAnthropic(orchidsName)
}

func TransformToolInput(toolName, clientName string, input map[string]interface{}) map[string]interface{} {
	if input == nil {
		input = make(map[string]interface{})
	}

	lowerTool := strings.ToLower(strings.TrimSpace(toolName))
	lowerClient := strings.ToLower(strings.TrimSpace(clientName))
	if lowerTool == "" {
		return copyToolInput(input)
	}

	if lowerTool == "ls" || lowerTool == "list" || lowerTool == "glob" {
		if strings.Contains(lowerClient, "/") {
			return copyToolInput(input)
		}
	}

	if lowerTool == "ls" && lowerClient == "glob" {
		result := copyToolInput(input)
		if _, ok := result["content"]; !ok {
			result["content"] = []interface{}{}
		}
		return result
	}

	if lowerTool == "ls" && strings.Contains(lowerClient, ".") {
		return copyToolInput(input)
	}

	if lowerTool == "read" || lowerTool == "readfile" {
		result := make(map[string]interface{})
		filePath := mapStringValue(input, "file_path", "path")
		if filePath == "" {
			filePath = mapStringValue(input, "content")
		}
		result["file_path"] = filePath
		for key, value := range input {
			if key == "file_path" || key == "path" {
				continue
			}
			result[key] = value
		}
		return result
	}

	if lowerTool == "bash" || strings.Contains(lowerTool, "edit") || strings.Contains(lowerTool, "write") {
		result := make(map[string]interface{})
		if content := mapStringValue(input, "content"); content != "" {
			result["content"] = content
		}
		if path := mapStringValue(input, "path"); path != "" {
			if existing, ok := result["content"].(string); ok && existing != "" {
				result["content"] = existing + "\n" + path
			} else {
				result["content"] = path
			}
		} else if _, ok := result["content"]; !ok {
			result["content"] = ""
		}
		return result
	}

	return copyToolInput(input)
}

func MapOrchidsToolToAnthropic(orchidsName string) string {
	if mapped, ok := orchidsToAnthropicMap[strings.TrimSpace(orchidsName)]; ok {
		return mapped
	}
	return orchidsName
}

func transformToolInputJSON(toolName, clientName, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}

	var input map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return raw
	}

	normalized := TransformToolInput(toolName, clientName, input)
	if normalized == nil {
		return raw
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return raw
	}
	return string(encoded)
}

func toolMapsFromInterfaces(clientTools []interface{}) []map[string]interface{} {
	if len(clientTools) == 0 {
		return nil
	}

	out := make([]map[string]interface{}, 0, len(clientTools))
	for _, raw := range clientTools {
		tool, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		out = append(out, tool)
	}
	return out
}

func toolMapperClientTools(clientTools []interface{}, toolMapper *ToolMapper) []map[string]interface{} {
	if toolMapper != nil && len(toolMapper.Tools) > 0 {
		return toolMapper.Tools
	}
	return toolMapsFromInterfaces(clientTools)
}

func copyToolInput(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	cloned := make(map[string]interface{}, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func toolSpecName(tool map[string]interface{}) string {
	return strings.TrimSpace(extractToolName(tool))
}

func extractToolName(tool map[string]interface{}) string {
	name, _, _ := extractToolSpecFields(tool)
	return name
}

var orchidsToolAliases = map[string][]string{
	"id":      {"text"},
	"name":    {"text"},
	"content": {"code"},
	"source":  {"input"},
}

var orchidsToAnthropicMap = map[string]string{
	"str_replace_editor": "str_replace_editor",
	"bash":               "bash",
	"computer":           "computer",
	"text_editor":        "text_editor",
}

func toSnakeCase(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var out strings.Builder
	for i, r := range value {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				out.WriteByte('_')
			}
			out.WriteByte(byte(r - 'A' + 'a'))
			continue
		}
		if r == '-' || r == ' ' {
			out.WriteByte('_')
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func collectAliasKeys(tm *ToolMapper) []string {
	if tm == nil || len(tm.index) == 0 {
		return nil
	}
	keys := make([]string, 0, len(tm.index))
	for key := range tm.index {
		keys = append(keys, key)
	}
	return keys
}

func getToolAliases(tool map[string]interface{}) []string {
	if len(tool) == 0 {
		return nil
	}
	if aliases := extractAliasStrings(tool["aliases"]); len(aliases) > 0 {
		return aliases
	}
	if fn, ok := tool["function"].(map[string]interface{}); ok {
		if aliases := extractAliasStrings(fn["aliases"]); len(aliases) > 0 {
			return aliases
		}
	}
	return nil
}

func extractAliasStrings(raw interface{}) []string {
	aliases, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		value, ok := alias.(string)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}
