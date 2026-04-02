package bolt

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-json"

	"orchids-api/internal/prompt"
)

var boltInvalidPathMarkers = []string{"/tmp/cc-agent/", "/mnt/", "d:\\", "c:\\", "~/"}
var boltAssistantCompletionMarkers = []string{
	"已创建", "创建完成", "已经创建", "已经完成", "已完成", "已成功完成",
	"文件已成功创建", "文件已经存在", "项目中已经有一个", "已在项目目录",
	"已包含", "无需重复修改", "已更新", "已经被更新", "运行方式",
	"可以直接运行", "当前文件状态是好的", "无需进一步操作",
	"成功更新", "成功修改", "成功添加", "成功写入", "成功创建",
	"已经添加", "已经修改", "修改完成", "添加完成", "更新完成", "写入完成",
	"已经成功", "添加了新", "新增了",
	"任务完成", "通过bash成功", "添加了一行", "添加了一",
	"已经通过", "执行完成", "操作完成", "已添加", "已写入",
	"created successfully", "has been created", "already exists", "already have",
	"has been updated", "updated successfully", "run it with", "can run",
	"no further action", "current file state is good",
	"successfully updated", "successfully modified", "successfully added",
	"successfully written", "has been modified", "has been added",
	"modification complete", "update complete", "task complete", "done.",
}
var boltEnvironmentBlockMarkers = []string{
	"# environment", "primary working directory:", "# auto memory",
	"gitstatus:", "you have been invoked in the following environment",
}

type boltToolUseMetadata struct {
	Name        string
	Path        string
	InvalidPath bool
	Aliases     map[string]struct{}
}

type boltToolResultOnlySummary struct {
	IsToolResultOnly          bool
	ReadAliases               map[string]struct{}
	UnchangedReadAliases      map[string]struct{}
	SuccessfulMutationAliases map[string]struct{}
	FailedMutationAliases     map[string]struct{}
}

func compactMessages(messages []prompt.Message, workdir string) []prompt.Message {
	messages = trimBoltMessagesAfterSupersededEmptyProjectClarification(messages)

	toolUses := collectBoltToolUseMetadata(messages, workdir)
	drop := make([]bool, len(messages))

	seenSuccessfulMutation := false
	for i := 0; i < len(messages); i++ {
		summary := summarizeBoltToolResultOnlyMessage(messages[i], toolUses)

		if seenSuccessfulMutation && isBoltMisleadingEmptyProjectProbeMessage(messages[i], toolUses) {
			drop[i] = true
		}
		if len(summary.SuccessfulMutationAliases) > 0 {
			seenSuccessfulMutation = true
		}

		if !drop[i] && isBoltMisleadingEmptyProjectProbeMessage(messages[i], toolUses) {
			for j := i + 1; j < len(messages); j++ {
				if extractBoltStandaloneUserText(messages[j]) != "" {
					break
				}
				if isBoltPositiveProjectProbeMessage(messages[j], toolUses) {
					drop[i] = true
					break
				}
			}
		}

		if !drop[i] && isBoltPositiveProjectProbeMessage(messages[i], toolUses) {
			for j := i + 1; j < len(messages); j++ {
				if extractBoltStandaloneUserText(messages[j]) != "" {
					break
				}
				next := summarizeBoltToolResultOnlyMessage(messages[j], toolUses)
				if !next.IsToolResultOnly {
					continue
				}
				if (len(next.ReadAliases) > 0 && isBoltPositiveProjectProbeForAliases(messages[i], toolUses, next.ReadAliases)) ||
					(len(next.SuccessfulMutationAliases) > 0 && isBoltPositiveProjectProbeForAliases(messages[i], toolUses, next.SuccessfulMutationAliases)) {
					drop[i] = true
					break
				}
			}
		}

		if !drop[i] && isBoltPositiveProjectProbeMessage(messages[i], toolUses) {
			for j := i - 1; j >= 0; j-- {
				if extractBoltStandaloneUserText(messages[j]) != "" {
					break
				}
				prev := summarizeBoltToolResultOnlyMessage(messages[j], toolUses)
				if !prev.IsToolResultOnly {
					continue
				}
				if (len(prev.ReadAliases) > 0 && isBoltPositiveProjectProbeForAliases(messages[i], toolUses, prev.ReadAliases)) ||
					(len(prev.SuccessfulMutationAliases) > 0 && isBoltPositiveProjectProbeForAliases(messages[i], toolUses, prev.SuccessfulMutationAliases)) {
					drop[i] = true
					break
				}
			}
		}

		if !drop[i] {
			if standaloneUserText := extractBoltStandaloneUserText(messages[i]); standaloneUserText != "" &&
				hasLaterEquivalentStandaloneUserRetry(messages, drop, i+1, standaloneUserText) {
				drop[i] = true
			}
		}

		if !drop[i] && strings.EqualFold(strings.TrimSpace(messages[i].Role), "assistant") {
			text := extractBoltAssistantHistoryContent(normalizeBlocks(messages[i]))
			if looksLikeBoltAssistantCompletionSummary(text) {
				if nextBoltStandaloneUserMessageIndex(messages, i+1) >= 0 {
					drop[i] = true
				}
			}
			if !drop[i] && looksLikeBoltInjectedFailureText(text) {
				if nextBoltStandaloneUserMessageIndex(messages, i+1) >= 0 {
					drop[i] = true
				}
			}
			if !drop[i] && looksLikeBoltAssistantLostReadContext(text) {
				if nextBoltStandaloneUserMessageIndex(messages, i+1) >= 0 {
					prevIdx := previousBoltVisibleMessageIndex(messages, i-1)
					if prevIdx >= 0 {
						prevSummary := summarizeBoltToolResultOnlyMessage(messages[prevIdx], toolUses)
						if prevSummary.IsToolResultOnly && (len(prevSummary.ReadAliases) > 0 || len(prevSummary.UnchangedReadAliases) > 0) {
							drop[i] = true
						}
					}
					if !drop[i] && hasRecentBoltReadContext(messages, toolUses, drop, i-1) {
						drop[i] = true
					}
				}
			}
			if !drop[i] && looksLikeBoltAssistantReadbackSummary(text) {
				if nextBoltStandaloneUserMessageIndex(messages, i+1) >= 0 {
					prevIdx := previousBoltVisibleMessageIndex(messages, i-1)
					if prevIdx >= 0 {
						prevSummary := summarizeBoltToolResultOnlyMessage(messages[prevIdx], toolUses)
						if prevSummary.IsToolResultOnly && (len(prevSummary.ReadAliases) > 0 || len(prevSummary.UnchangedReadAliases) > 0) {
							drop[i] = true
						}
					}
					if !drop[i] && hasRecentBoltReadContext(messages, toolUses, drop, i-1) {
						drop[i] = true
					}
				}
			}
			if !drop[i] && looksLikeBoltAssistantNoContentPlaceholder(text) {
				if nextBoltStandaloneUserMessageIndex(messages, i+1) >= 0 {
					drop[i] = true
				}
			}
			if !drop[i] && looksLikeBoltAssistantRawToolCallJSON(text) {
				if nextBoltStandaloneUserMessageIndex(messages, i+1) >= 0 {
					drop[i] = true
				}
			}
		}

		if !drop[i] && summary.IsToolResultOnly && len(summary.UnchangedReadAliases) > 0 {
			for j := i - 1; j >= 0; j-- {
				if drop[j] {
					continue
				}
				prev := summarizeBoltToolResultOnlyMessage(messages[j], toolUses)
				if !prev.IsToolResultOnly {
					continue
				}
				if sharesBoltPathAlias(summary.UnchangedReadAliases, prev.ReadAliases) {
					drop[i] = true
					break
				}
			}
		}

		if !drop[i] && summary.IsToolResultOnly && len(summary.ReadAliases) > 0 {
			for j := i + 1; j < len(messages); j++ {
				if extractBoltStandaloneUserText(messages[j]) != "" {
					break
				}
				next := summarizeBoltToolResultOnlyMessage(messages[j], toolUses)
				if !next.IsToolResultOnly {
					continue
				}
				if sharesBoltPathAlias(summary.ReadAliases, next.ReadAliases) ||
					sharesBoltPathAlias(summary.ReadAliases, next.SuccessfulMutationAliases) {
					drop[i] = true
					break
				}
			}
		}

		if !drop[i] && summary.IsToolResultOnly && len(summary.SuccessfulMutationAliases) > 0 {
			for j := i + 1; j < len(messages); j++ {
				next := summarizeBoltToolResultOnlyMessage(messages[j], toolUses)
				if !next.IsToolResultOnly {
					continue
				}
				if sharesBoltPathAlias(summary.SuccessfulMutationAliases, next.ReadAliases) ||
					sharesBoltPathAlias(summary.SuccessfulMutationAliases, next.SuccessfulMutationAliases) {
					drop[i] = true
					break
				}
			}
		}

		if !drop[i] && summary.IsToolResultOnly && len(summary.FailedMutationAliases) > 0 {
			for j := i + 1; j < len(messages); j++ {
				if extractBoltStandaloneUserText(messages[j]) != "" {
					break
				}
				next := summarizeBoltToolResultOnlyMessage(messages[j], toolUses)
				if !next.IsToolResultOnly {
					continue
				}
				if sharesBoltPathAlias(summary.FailedMutationAliases, next.ReadAliases) ||
					sharesBoltPathAlias(summary.FailedMutationAliases, next.FailedMutationAliases) ||
					sharesBoltPathAlias(summary.FailedMutationAliases, next.SuccessfulMutationAliases) {
					drop[i] = true
					break
				}
			}
		}
	}

	messages = dropMissingWorkspaceTargets(messages, workdir, toolUses, drop)

	return filterMessages(messages, drop)
}

func dropMissingWorkspaceTargets(messages []prompt.Message, workdir string, toolUses map[string]boltToolUseMetadata, drop []bool) []prompt.Message {
	missingPaths, missingAliases := collectMissingWorkspaceHistory(messages, workdir)
	if len(missingAliases) == 0 {
		return messages
	}

	for i, msg := range messages {
		if drop[i] {
			continue
		}
		summary := summarizeBoltToolResultOnlyMessage(msg, toolUses)
		if summary.IsToolResultOnly && (sharesBoltPathAlias(summary.ReadAliases, missingAliases) ||
			sharesBoltPathAlias(summary.SuccessfulMutationAliases, missingAliases) ||
			sharesBoltPathAlias(summary.FailedMutationAliases, missingAliases)) {
			drop[i] = true
			continue
		}

		if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			text := extractBoltAssistantHistoryContent(normalizeBlocks(msg))
			if looksLikeBoltAssistantCompletionSummary(text) || looksLikeBoltStaleFilePresenceSummary(text) {
				prevIdx := previousBoltVisibleMessageIndex(messages, i-1)
				if prevIdx >= 0 {
					prevSummary := summarizeBoltToolResultOnlyMessage(messages[prevIdx], toolUses)
					if prevSummary.IsToolResultOnly && (sharesBoltPathAlias(prevSummary.ReadAliases, missingAliases) ||
						sharesBoltPathAlias(prevSummary.SuccessfulMutationAliases, missingAliases) ||
						sharesBoltPathAlias(prevSummary.FailedMutationAliases, missingAliases)) {
						drop[i] = true
						continue
					}
				}
				for _, path := range missingPaths {
					if strings.Contains(text, path) || strings.Contains(text, filepath.Base(path)) {
						drop[i] = true
						break
					}
				}
			}
		}

		if strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			text := extractBoltStandaloneUserText(msg)
			if looksLikeBoltInjectedRetryPromptForMissingPaths(text, missingPaths) {
				drop[i] = true
			}
		}
	}
	return messages
}

func filterMessages(messages []prompt.Message, drop []bool) []prompt.Message {
	out := make([]prompt.Message, 0, len(messages))
	for i, msg := range messages {
		if !drop[i] {
			out = append(out, msg)
		}
	}
	return out
}

func collectBoltToolUseMetadata(messages []prompt.Message, workdir string) map[string]boltToolUseMetadata {
	toolUses := make(map[string]boltToolUseMetadata)
	for _, msg := range messages {
		for _, block := range normalizeBlocks(msg) {
			if block.Type != "tool_use" || strings.TrimSpace(block.ID) == "" {
				continue
			}
			path, invalid := normalizeBoltToolPath(extractBoltToolPath(block.Input), workdir)
			toolUses[strings.TrimSpace(block.ID)] = boltToolUseMetadata{
				Name:        strings.TrimSpace(block.Name),
				Path:        path,
				InvalidPath: invalid,
				Aliases:     buildBoltPathAliases(path),
			}
		}
	}
	return toolUses
}

func normalizeBoltToolPath(path, workdir string) (string, bool) {
	path = strings.Trim(strings.TrimSpace(path), "\"'`")
	if path == "" {
		return "", false
	}
	if relativePath, ok := boltWorkspaceRelativePath(path, workdir); ok {
		return relativePath, false
	}
	return path, looksLikeInvalidBoltPath(path)
}

func summarizeBoltToolResultOnlyMessage(msg prompt.Message, toolUses map[string]boltToolUseMetadata) boltToolResultOnlySummary {
	summary := boltToolResultOnlySummary{}
	if !isBoltToolResultOnlyUserMessage(msg) {
		return summary
	}
	summary.IsToolResultOnly = true

	for _, block := range normalizeBlocks(msg) {
		if !strings.EqualFold(strings.TrimSpace(block.Type), "tool_result") {
			continue
		}
		rawText := sanitizeBoltToolResultText(stringifyContent(block.Content))
		if rawText == "" {
			continue
		}
		toolMeta := toolUses[strings.TrimSpace(block.ToolUseID)]
		if toolMeta.InvalidPath || len(toolMeta.Aliases) == 0 {
			continue
		}
		switch strings.TrimSpace(toolMeta.Name) {
		case "Read":
			if !isBoltToolResultError(block, rawText) {
				target := &summary.ReadAliases
				if looksLikeBoltReadUnchangedSummary(rawText) {
					target = &summary.UnchangedReadAliases
				}
				if *target == nil {
					*target = make(map[string]struct{})
				}
				for alias := range toolMeta.Aliases {
					(*target)[alias] = struct{}{}
				}
			}
		case "Write", "Edit":
			if isBoltToolResultSuccess(toolMeta.Name, rawText) {
				if summary.SuccessfulMutationAliases == nil {
					summary.SuccessfulMutationAliases = make(map[string]struct{})
				}
				for alias := range toolMeta.Aliases {
					summary.SuccessfulMutationAliases[alias] = struct{}{}
				}
			}
			if isBoltToolResultError(block, rawText) {
				if summary.FailedMutationAliases == nil {
					summary.FailedMutationAliases = make(map[string]struct{})
				}
				for alias := range toolMeta.Aliases {
					summary.FailedMutationAliases[alias] = struct{}{}
				}
			}
		}
	}
	return summary
}

func normalizeBlocks(msg prompt.Message) []prompt.ContentBlock {
	if msg.Content.IsString() {
		text := msg.Content.GetText()
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []prompt.ContentBlock{{Type: "text", Text: text}}
	}
	return msg.Content.GetBlocks()
}

func shouldSkipBoltMessage(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), "tool")
}

func isBoltToolResultOnlyUserMessage(msg prompt.Message) bool {
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
		return false
	}
	blocks := normalizeBlocks(msg)
	if len(blocks) == 0 {
		return false
	}
	hasToolResult := false
	for _, block := range blocks {
		switch strings.ToLower(strings.TrimSpace(block.Type)) {
		case "tool_result":
			hasToolResult = true
		case "text":
			if sanitizeBoltMessageText(block.Text) != "" {
				return false
			}
		default:
			return false
		}
	}
	return hasToolResult
}

func extractBoltStandaloneUserText(msg prompt.Message) string {
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
		return ""
	}
	if msg.Content.IsString() {
		return sanitizeBoltMessageText(msg.Content.GetText())
	}

	blocks := msg.Content.GetBlocks()
	if len(blocks) == 0 {
		return ""
	}

	var sb strings.Builder
	first := true
	hasToolResult := false
	for _, block := range blocks {
		switch strings.ToLower(strings.TrimSpace(block.Type)) {
		case "tool_result":
			hasToolResult = true
		case "text":
			if text := sanitizeBoltMessageText(block.Text); text != "" {
				if !first {
					sb.WriteByte('\n')
				}
				sb.WriteString(text)
				first = false
			}
		}
	}
	if hasToolResult {
		return ""
	}
	return sb.String()
}

func previousBoltVisibleMessageIndex(messages []prompt.Message, start int) int {
	for i := start; i >= 0; i-- {
		if shouldSkipBoltMessage(messages[i].Role) {
			continue
		}
		return i
	}
	return -1
}

func nextBoltStandaloneUserMessageIndex(messages []prompt.Message, start int) int {
	for i := start; i < len(messages); i++ {
		if extractBoltStandaloneUserText(messages[i]) != "" {
			return i
		}
	}
	return -1
}

func hasLaterEquivalentStandaloneUserRetry(messages []prompt.Message, drop []bool, start int, text string) bool {
	want := strings.TrimSpace(text)
	if want == "" {
		return false
	}

	sawLaterSame := false
	for i := start; i < len(messages); i++ {
		if i < len(drop) && drop[i] {
			continue
		}
		if nextText := extractBoltStandaloneUserText(messages[i]); nextText != "" {
			if strings.EqualFold(strings.TrimSpace(nextText), want) {
				sawLaterSame = true
				continue
			}
			return sawLaterSame
		}
		if isBoltToolResultOnlyUserMessage(messages[i]) {
			return sawLaterSame
		}
		if isBoltIgnorableStandaloneRetryGap(messages[i]) {
			continue
		}
		return sawLaterSame
	}
	return sawLaterSame
}

func isBoltIgnorableStandaloneRetryGap(msg prompt.Message) bool {
	if shouldSkipBoltMessage(msg.Role) {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
		return false
	}

	blocks := normalizeBlocks(msg)
	if len(blocks) == 0 {
		return true
	}

	sawText := false
	for _, block := range blocks {
		if !strings.EqualFold(strings.TrimSpace(block.Type), "text") {
			return false
		}
		sawText = true
		text := sanitizeBoltAssistantText(block.Text)
		if text == "" {
			continue
		}
		if !looksLikeBoltAssistantNoContentPlaceholder(text) &&
			!looksLikeBoltInjectedFailureText(text) {
			return false
		}
	}
	return sawText
}

func looksLikeBoltAssistantCompletionSummary(text string) bool {
	text = sanitizeBoltAssistantText(text)
	if strings.TrimSpace(text) == "" {
		return false
	}
	if len([]rune(text)) > 900 {
		return false
	}
	lower := strings.ToLower(text)
	for _, marker := range boltAssistantCompletionMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func looksLikeBoltStaleFilePresenceSummary(text string) bool {
	text = sanitizeBoltAssistantText(text)
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"可直接运行", "已经完整实现", "已经存在", "无需重复添加", "功能已经存在",
		"file is already created", "already exists",
	} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func looksLikeBoltInjectedFailureText(text string) bool {
	text = sanitizeBoltAssistantText(text)
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	for _, prefix := range []string{
		"request failed:", "request failed：", "request exhausted:",
		"authentication error:", "access forbidden (403):",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func looksLikeBoltAssistantLostReadContext(text string) bool {
	text = sanitizeBoltAssistantText(text)
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	switch {
	case strings.Contains(lower, "文件的新修改需求是什么"):
		return true
	case strings.Contains(lower, "内容才能继续修改") && strings.Contains(lower, "请告诉我文件里有什么内容"):
		return true
	case strings.Contains(lower, "i need to see the earlier content from this conversation"):
		return true
	default:
		return false
	}
}

func looksLikeBoltReadUnchangedSummary(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "file unchanged since last read") &&
		strings.Contains(lower, "earlier read tool_result") &&
		strings.Contains(lower, "still current")
}

func looksLikeBoltAssistantReadbackSummary(text string) bool {
	text = sanitizeBoltAssistantText(text)
	if strings.TrimSpace(text) == "" {
		return false
	}
	if len([]rune(text)) > 240 {
		return false
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"the file contains",
		"the file currently contains",
		"文件现在包含",
		"文件当前包含",
		"当前文件包含",
	} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func looksLikeBoltAssistantNoContentPlaceholder(text string) bool {
	text = strings.ToLower(strings.TrimSpace(sanitizeBoltAssistantText(text)))
	switch text {
	case "(no content)", "no content":
		return true
	default:
		return false
	}
}

func looksLikeBoltAssistantRawToolCallJSON(text string) bool {
	text = sanitizeBoltAssistantText(text)
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if len([]rune(trimmed)) > 2400 {
		return false
	}
	if leadingJSON, trailing, ok := extractLeadingJSONValue(trimmed); ok {
		if strings.TrimSpace(trailing) == "" && len(extractToolCallsFromJSON([]byte(leadingJSON))) > 0 {
			return true
		}
	}
	return false
}

func hasRecentBoltReadContext(messages []prompt.Message, toolUses map[string]boltToolUseMetadata, drop []bool, start int) bool {
	sawStandaloneUser := false
	for i := start; i >= 0; i-- {
		if i < len(drop) && drop[i] {
			continue
		}
		summary := summarizeBoltToolResultOnlyMessage(messages[i], toolUses)
		if summary.IsToolResultOnly {
			if len(summary.SuccessfulMutationAliases) > 0 || len(summary.FailedMutationAliases) > 0 {
				return false
			}
			if len(summary.ReadAliases) > 0 || len(summary.UnchangedReadAliases) > 0 {
				return sawStandaloneUser
			}
			continue
		}
		if text := extractBoltStandaloneUserText(messages[i]); text != "" {
			sawStandaloneUser = true
		}
	}
	return false
}

func looksLikeBoltEmptyProjectClarification(text string) bool {
	text = sanitizeBoltAssistantText(text)
	if strings.TrimSpace(text) == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"这是一个空项目", "项目目录为空", "请告诉我你想要构建什么",
		"project directory is empty", "what would you like to build", "please describe your application",
	} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func looksLikeBoltInjectedRetryPromptForMissingPaths(text string, missingPaths []string) bool {
	text = strings.TrimSpace(text)
	if text == "" || len(missingPaths) == 0 {
		return false
	}
	matchedPath := false
	for _, path := range missingPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if strings.Contains(text, "`"+path+"`") || strings.Contains(text, filepath.Base(path)) {
			matchedPath = true
			break
		}
	}
	if !matchedPath {
		return false
	}
	for _, marker := range []string{
		"上一轮你在没有任何新的成功 Write/Edit 工具结果的情况下直接声称已经完成，这是错误的。",
		"你刚刚已经成功 Read 过",
		"上一轮在已经拿到",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func isBoltMisleadingEmptyProjectProbeMessage(msg prompt.Message, toolUses map[string]boltToolUseMetadata) bool {
	if !isBoltToolResultOnlyUserMessage(msg) {
		return false
	}
	blocks := normalizeBlocks(msg)
	hasProbe := false
	for _, block := range blocks {
		if !strings.EqualFold(strings.TrimSpace(block.Type), "tool_result") {
			continue
		}
		rawText := strings.TrimSpace(sanitizeBoltToolResultText(stringifyContent(block.Content)))
		if rawText == "" {
			continue
		}
		toolMeta := toolUses[strings.TrimSpace(block.ToolUseID)]
		if !isBoltEmptyProjectProbeResult(toolMeta, rawText) {
			return false
		}
		hasProbe = true
	}
	return hasProbe
}

func isBoltPositiveProjectProbeMessage(msg prompt.Message, toolUses map[string]boltToolUseMetadata) bool {
	if !isBoltToolResultOnlyUserMessage(msg) {
		return false
	}
	blocks := normalizeBlocks(msg)
	hasPositive := false
	for _, block := range blocks {
		if !strings.EqualFold(strings.TrimSpace(block.Type), "tool_result") {
			continue
		}
		rawText := strings.TrimSpace(sanitizeBoltToolResultText(stringifyContent(block.Content)))
		if rawText == "" {
			continue
		}
		toolMeta := toolUses[strings.TrimSpace(block.ToolUseID)]
		if !isBoltProjectProbeTool(toolMeta) {
			return false
		}
		if isBoltToolResultError(block, rawText) || isBoltEmptyProjectProbeResult(toolMeta, rawText) {
			return false
		}
		hasPositive = true
	}
	return hasPositive
}

func isBoltPositiveProjectProbeForAliases(msg prompt.Message, toolUses map[string]boltToolUseMetadata, aliases map[string]struct{}) bool {
	if len(aliases) == 0 || !isBoltToolResultOnlyUserMessage(msg) {
		return false
	}
	blocks := normalizeBlocks(msg)
	hasPositive := false
	for _, block := range blocks {
		if !strings.EqualFold(strings.TrimSpace(block.Type), "tool_result") {
			continue
		}
		rawText := strings.TrimSpace(sanitizeBoltToolResultText(stringifyContent(block.Content)))
		if rawText == "" {
			continue
		}
		toolMeta := toolUses[strings.TrimSpace(block.ToolUseID)]
		if !isBoltProjectProbeTool(toolMeta) || isBoltToolResultError(block, rawText) || isBoltEmptyProjectProbeResult(toolMeta, rawText) {
			return false
		}
		if !containsBoltPathAliasInText(rawText, aliases) {
			return false
		}
		hasPositive = true
	}
	return hasPositive
}

func containsBoltPathAliasInText(text string, aliases map[string]struct{}) bool {
	if len(aliases) == 0 {
		return false
	}
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(text), "\\", "/"))
	if normalized == "" {
		return false
	}
	for alias := range aliases {
		if alias != "" && strings.Contains(normalized, strings.ToLower(strings.ReplaceAll(alias, "\\", "/"))) {
			return true
		}
	}
	return false
}

func isBoltEmptyProjectProbeResult(toolMeta boltToolUseMetadata, rawText string) bool {
	return strings.TrimSpace(rawText) == "No files found" && isBoltProjectProbeTool(toolMeta)
}

func isBoltProjectProbeTool(toolMeta boltToolUseMetadata) bool {
	return isBoltRootProjectProbeToolCall(toolMeta.Name, toolMeta.Path)
}

func isBoltMutationTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "Write", "Edit":
		return true
	default:
		return false
	}
}

func isBoltToolResultError(block prompt.ContentBlock, text string) bool {
	if block.IsError {
		return true
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"<tool_use_error>", "error editing file", "string to replace not found",
		"failed to edit", "edit failed", "hook pretooluse", "denied this tool",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isBoltToolResultSuccess(toolName, text string) bool {
	if !isBoltMutationTool(toolName) {
		return false
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"updated successfully", "created successfully", "written successfully",
		"wrote ", "applied successfully", "has been updated",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func buildBoltPathAliases(path string) map[string]struct{} {
	aliases := make(map[string]struct{})
	trimmed := strings.Trim(strings.TrimSpace(path), "\"'`")
	if trimmed == "" {
		return aliases
	}
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	canonical := strings.ToLower(normalized)
	aliases[canonical] = struct{}{}
	base := strings.TrimSpace(filepath.Base(filepath.Clean(normalized)))
	if base != "" && base != "." && base != string(filepath.Separator) {
		aliases[strings.ToLower(strings.ReplaceAll(base, "\\", "/"))] = struct{}{}
	}
	return aliases
}

func sharesBoltPathAlias(left, right map[string]struct{}) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for alias := range left {
		if _, ok := right[alias]; ok {
			return true
		}
	}
	return false
}

func looksLikeInvalidBoltPath(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	if lower == "" {
		return false
	}
	for _, marker := range boltInvalidPathMarkers {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func boltWorkspaceRelativePath(path, workdir string) (string, bool) {
	path = strings.Trim(strings.TrimSpace(path), "\"'`")
	workdir = strings.Trim(strings.TrimSpace(workdir), "\"'`")
	if path == "" || workdir == "" {
		return "", false
	}
	if filepath.IsAbs(path) {
		rel, err := filepath.Rel(workdir, path)
		if err != nil {
			return "", false
		}
		rel = filepath.Clean(rel)
		if rel == "." || strings.HasPrefix(rel, "..") {
			return "", false
		}
		return filepath.ToSlash(rel), true
	}
	if looksLikeInvalidBoltPath(path) {
		return "", false
	}
	rel := filepath.Clean(path)
	if rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func boltWorkspacePathExists(relativePath, workdir string) bool {
	relativePath = strings.Trim(strings.TrimSpace(relativePath), "\"'`")
	workdir = strings.Trim(strings.TrimSpace(workdir), "\"'`")
	if relativePath == "" || workdir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(workdir, filepath.FromSlash(relativePath)))
	return err == nil
}

func collectMissingWorkspaceHistory(messages []prompt.Message, workdir string) ([]string, map[string]struct{}) {
	workdir = strings.Trim(strings.TrimSpace(workdir), "\"'`")
	if workdir == "" || len(messages) == 0 {
		return nil, nil
	}

	toolUses := collectBoltToolUseMetadata(messages, workdir)
	pathSet := make(map[string]struct{})
	aliasSet := make(map[string]struct{})

	for _, msg := range messages {
		for _, block := range normalizeBlocks(msg) {
			if !strings.EqualFold(strings.TrimSpace(block.Type), "tool_result") {
				continue
			}
			toolMeta := toolUses[strings.TrimSpace(block.ToolUseID)]
			if toolMeta.InvalidPath || strings.TrimSpace(toolMeta.Path) == "" || len(toolMeta.Aliases) == 0 {
				continue
			}
			rawText := sanitizeBoltToolResultText(stringifyContent(block.Content))
			if strings.TrimSpace(rawText) == "" {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(toolMeta.Name), "Read") {
				if isBoltToolResultError(block, rawText) {
					continue
				}
			} else if isBoltMutationTool(toolMeta.Name) {
				if !isBoltToolResultSuccess(toolMeta.Name, rawText) && !isBoltToolResultError(block, rawText) {
					continue
				}
			} else {
				continue
			}

			relativePath, ok := boltWorkspaceRelativePath(toolMeta.Path, workdir)
			if !ok {
				continue
			}
			if boltWorkspacePathExists(relativePath, workdir) {
				continue
			}
			pathSet[relativePath] = struct{}{}
			for alias := range toolMeta.Aliases {
				aliasSet[alias] = struct{}{}
			}
		}
	}

	if len(pathSet) == 0 {
		return nil, nil
	}
	paths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, aliasSet
}

func isBoltMissingPathResult(content interface{}) bool {
	lower := strings.ToLower(strings.TrimSpace(stringifyContent(content)))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"no such file or directory", "cannot access", "does not exist",
		"path does not exist", "系统找不到指定的路径", "找不到指定的路径", "enoent", "not found",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func extractInvalidBoltPathFromValue(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return extractInvalidBoltPathFromString(v)
	case map[string]interface{}:
		for _, nested := range v {
			if path := extractInvalidBoltPathFromValue(nested); path != "" {
				return path
			}
		}
	case []interface{}:
		for _, nested := range v {
			if path := extractInvalidBoltPathFromValue(nested); path != "" {
				return path
			}
		}
	case []prompt.ContentBlock:
		for _, nested := range v {
			if path := extractInvalidBoltPathFromValue(nested.Content); path != "" {
				return path
			}
			if path := extractInvalidBoltPathFromString(nested.Text); path != "" {
				return path
			}
		}
	default:
		if data, err := json.Marshal(v); err == nil {
			return extractInvalidBoltPathFromString(string(data))
		}
	}
	return ""
}

func extractInvalidBoltPathFromString(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	for _, field := range strings.Fields(text) {
		candidate := strings.Trim(field, "\"'`,;()[]{}")
		if looksLikeInvalidBoltPath(candidate) {
			return candidate
		}
	}
	lower := strings.ToLower(text)
	for _, marker := range boltInvalidPathMarkers {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		end := len(text)
		for i := idx; i < len(text); i++ {
			switch text[i] {
			case ' ', '\n', '\r', '\t', '"', '\'', '`':
				end = i
				goto found
			}
		}
	found:
		candidate := strings.Trim(text[idx:end], "\"'`,;()[]{}")
		if looksLikeInvalidBoltPath(candidate) {
			return candidate
		}
	}
	return ""
}

func trimBoltMessagesAfterSupersededEmptyProjectClarification(messages []prompt.Message) []prompt.Message {
	if len(messages) < 3 {
		return messages
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			continue
		}
		if !looksLikeBoltEmptyProjectClarification(msg.ExtractText()) {
			continue
		}
		replyIdx := -1
		for j := i + 1; j < len(messages); j++ {
			if text := extractBoltStandaloneUserText(messages[j]); text != "" {
				replyIdx = j
				break
			}
		}
		if replyIdx < 0 {
			return messages
		}
		return append([]prompt.Message(nil), messages[replyIdx:]...)
	}
	return messages
}
