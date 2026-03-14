package warp

import (
	"fmt"
	"sort"
	"strings"
)

const (
	warpCurrentToolResultLimit      = 900
	warpHistoryToolResultLimit      = 500
	warpCurrentReadOutputLimit      = 3200
	warpHistoryReadOutputLimit      = 1800
	warpDedupLargeReadLineCount     = 40
	warpDedupLargeReadRuneThreshold = 4096
)

func compactWarpToolResultContent(text string, historyMode bool) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if looksLikeWarpNumberedReadOutput(text) && looksLikeWarpCodeReadOutput(text) {
		return compactWarpCodeReadOutput(text, historyMode)
	}
	if looksLikeWarpDirectoryListing(text) {
		return compactWarpDirectoryListing(text, historyMode)
	}
	if historyMode {
		return compactHistoricalWarpToolResult(text)
	}
	return compactCurrentWarpToolResult(text)
}

func looksLikeWarpDirectoryListing(text string) bool {
	lines := nonEmptyWarpLines(text)
	if len(lines) < 4 {
		return false
	}
	entryLike := 0
	strongSignals := 0
	for _, line := range lines {
		switch {
		case looksLikeWarpPathLine(line):
			entryLike++
			strongSignals++
		case looksLikeWarpBareDirectoryEntryLine(line):
			entryLike++
			if hasWarpDirectoryEntrySignal(line) {
				strongSignals++
			}
		}
	}
	return entryLike*100/len(lines) >= 70 && strongSignals > 0
}

func looksLikeWarpPathLine(line string) bool {
	line = normalizeWarpDirectoryCandidateLine(line)
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "/") || strings.HasPrefix(line, "./") || strings.HasPrefix(line, "../") {
		return true
	}
	return len(line) >= 3 && ((line[1] == ':' && line[2] == '\\') || (line[1] == ':' && line[2] == '/'))
}

func looksLikeWarpBareDirectoryEntryLine(line string) bool {
	line = normalizeWarpDirectoryCandidateLine(line)
	if line == "" {
		return false
	}
	if strings.ContainsAny(line, "\r\n\t") {
		return false
	}
	lower := strings.ToLower(line)
	if strings.Contains(lower, "results are truncated") {
		return false
	}
	if strings.HasPrefix(line, "[") || strings.HasPrefix(line, "<") {
		return false
	}
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "• ") {
		return false
	}
	if strings.Contains(line, ": ") || strings.ContainsAny(line, "{}<>|`") {
		return false
	}
	if strings.HasSuffix(line, ".") || strings.HasSuffix(line, "。") || strings.HasSuffix(line, ":") {
		return false
	}
	if strings.Count(line, " ") > 2 {
		return false
	}
	return hasWarpDirectoryEntrySignal(line)
}

func hasWarpDirectoryEntrySignal(line string) bool {
	line = normalizeWarpDirectoryCandidateLine(line)
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, ".") {
		return true
	}
	return strings.ContainsAny(line, "._-/\\")
}

func compactWarpDirectoryListing(text string, historyMode bool) string {
	lines := nonEmptyWarpLines(text)
	if len(lines) == 0 {
		return ""
	}
	total := len(lines)
	pathLines, nonPathCount := splitWarpDirectoryListingLines(lines)
	prefix := sharedWarpDirectoryPrefix(pathLines)

	filtered := make([]string, 0, len(pathLines))
	omittedNoise := 0
	for _, line := range pathLines {
		if shouldDropWarpDirectoryLine(line) {
			omittedNoise++
			continue
		}
		filtered = append(filtered, shortenWarpDirectoryLine(line, prefix))
	}
	if len(filtered) == 0 {
		return fmt.Sprintf("[directory listing trimmed: omitted %d metadata entries and %d non-path lines]", total, nonPathCount)
	}

	if shouldSummarizeWarpDirectoryTopLevel(filtered) {
		summarized, summarizedRoots, omittedRoots := summarizeWarpDirectoryTopLevel(filtered, historyMode)
		if omittedNoise > 0 || nonPathCount > 0 || omittedRoots > 0 {
			summarized = append(summarized, fmt.Sprintf("[directory listing summarized: %d root entries from %d lines; omitted %d metadata entries, %d non-path lines, and %d additional root entries]", summarizedRoots, total, omittedNoise, nonPathCount, omittedRoots))
		}
		result := strings.Join(summarized, "\n")
		if historyMode {
			return truncateWarpResultWithEllipsis(result, warpHistoryToolResultLimit)
		}
		return truncateWarpResultWithEllipsis(result, warpCurrentToolResultLimit)
	}

	limit := 16
	if historyMode {
		limit = 8
	}
	kept := len(filtered)
	extra := 0
	if kept > limit {
		extra = kept - limit
		filtered = filtered[:limit]
	}
	if omittedNoise > 0 || nonPathCount > 0 || extra > 0 {
		filtered = append(filtered, fmt.Sprintf("[directory listing trimmed: kept %d of %d entries; omitted %d metadata entries, %d non-path lines, and %d extra entries]", kept-extra, total, omittedNoise, nonPathCount, extra))
	}

	result := strings.Join(filtered, "\n")
	if historyMode {
		return truncateWarpResultWithEllipsis(result, warpHistoryToolResultLimit)
	}
	return truncateWarpResultWithEllipsis(result, warpCurrentToolResultLimit)
}

func splitWarpDirectoryListingLines(lines []string) ([]string, int) {
	pathLines := make([]string, 0, len(lines))
	nonPathCount := 0
	for _, line := range lines {
		normalized := normalizeWarpDirectoryCandidateLine(line)
		if looksLikeWarpPathLine(normalized) || looksLikeWarpBareDirectoryEntryLine(normalized) {
			pathLines = append(pathLines, normalized)
			continue
		}
		nonPathCount++
	}
	return pathLines, nonPathCount
}

func normalizeWarpDirectoryCandidateLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	if entry, ok := extractWarpLongLsEntry(line); ok {
		return entry
	}
	return line
}

func extractWarpLongLsEntry(line string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 9 {
		return "", false
	}
	mode := fields[0]
	if len(mode) < 10 {
		return "", false
	}
	switch mode[0] {
	case '-', 'd', 'l', 'b', 'c', 'p', 's':
	default:
		return "", false
	}
	if !strings.ContainsAny(mode, "rwx-") {
		return "", false
	}
	entry := strings.Join(fields[8:], " ")
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", false
	}
	if idx := strings.Index(entry, " -> "); idx >= 0 {
		entry = strings.TrimSpace(entry[:idx])
	}
	return entry, entry != ""
}

func shouldDropWarpDirectoryLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	if strings.Contains(line, "/.git/") || strings.HasSuffix(line, "/.git") {
		return true
	}
	base := line
	if idx := strings.LastIndexAny(base, `/\`); idx >= 0 {
		base = base[idx+1:]
	}
	lowerBase := strings.ToLower(base)
	switch base {
	case ".DS_Store", "Thumbs.db", "desktop.ini":
		return true
	default:
		return looksLikeSensitiveWarpDirectoryBase(lowerBase)
	}
}

func looksLikeSensitiveWarpDirectoryBase(base string) bool {
	if base == "" {
		return false
	}
	switch {
	case base == ".env",
		strings.HasPrefix(base, ".env."),
		base == "secrets.json",
		base == "orchids_accounts.txt",
		strings.HasSuffix(base, ".pem"),
		strings.HasSuffix(base, ".key"):
		return true
	}
	for _, marker := range []string{"token", "cookie", "credential", "secret"} {
		if strings.Contains(base, marker) {
			return true
		}
	}
	return false
}

func sharedWarpDirectoryPrefix(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	pathLines, _ := splitWarpDirectoryListingLines(lines)
	if len(pathLines) == 0 {
		return ""
	}
	prefix := strings.TrimSpace(pathLines[0])
	for _, raw := range pathLines[1:] {
		line := strings.TrimSpace(raw)
		for prefix != "" && !strings.HasPrefix(line, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
		if prefix == "" {
			return ""
		}
	}
	idx := strings.LastIndex(prefix, "/")
	if idx < 0 {
		return ""
	}
	return prefix[:idx+1]
}

func shortenWarpDirectoryLine(line, prefix string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	if prefix != "" && strings.HasPrefix(line, prefix) {
		trimmed := strings.TrimPrefix(line, prefix)
		if trimmed == "" {
			return "./"
		}
		return "./" + trimmed
	}
	return line
}

func shouldSummarizeWarpDirectoryTopLevel(lines []string) bool {
	if len(lines) <= 10 {
		return false
	}
	nested := 0
	for _, line := range lines {
		trimmed := strings.TrimPrefix(strings.TrimSpace(line), "./")
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "/") {
			nested++
		}
	}
	return nested*100/len(lines) >= 60
}

func summarizeWarpDirectoryTopLevel(lines []string, historyMode bool) ([]string, int, int) {
	type rootSummary struct {
		label   string
		samples []string
	}

	maxRoots := 8
	maxSamples := 0
	if historyMode {
		maxRoots = 5
		maxSamples = 1
	}

	order := make([]string, 0, len(lines))
	roots := make(map[string]*rootSummary)
	for _, line := range lines {
		trimmed := strings.TrimPrefix(strings.TrimSpace(line), "./")
		if trimmed == "" {
			continue
		}
		parts := strings.Split(trimmed, "/")
		key := "./" + parts[0]
		sample := ""
		if len(parts) > 1 {
			key += "/"
			sample = strings.Join(parts[1:], "/")
		}
		summary, ok := roots[key]
		if !ok {
			summary = &rootSummary{label: key}
			roots[key] = summary
			order = append(order, key)
		}
		if sample != "" && len(summary.samples) < maxSamples && !containsWarpString(summary.samples, sample) {
			summary.samples = append(summary.samples, sample)
		}
	}

	out := make([]string, 0, minWarpInt(len(order), maxRoots))
	omitted := 0
	for idx, key := range order {
		if idx >= maxRoots {
			omitted++
			continue
		}
		summary := roots[key]
		if len(summary.samples) == 0 {
			out = append(out, summary.label)
			continue
		}
		out = append(out, fmt.Sprintf("%s (sample: %s)", summary.label, strings.Join(summary.samples, ", ")))
	}
	return out, minWarpInt(len(order), maxRoots), omitted
}

func compactCurrentWarpToolResult(text string) string {
	return compactWarpToolResultLines(text, warpCurrentToolResultLimit, 10, 3)
}

func compactHistoricalWarpToolResult(text string) string {
	return compactWarpToolResultLines(text, warpHistoryToolResultLimit, 6, 2)
}

func compactWarpToolResultLines(text string, maxChars int, head, tail int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := nonEmptyWarpLines(text)
	if len(lines) == 0 {
		return truncateWarpResultWithEllipsis(text, maxChars)
	}
	if len(lines) > head+tail+1 {
		compacted := append([]string{}, lines[:head]...)
		compacted = append(compacted, fmt.Sprintf("[tool_result summary: omitted %d middle lines]", len(lines)-head-tail))
		compacted = append(compacted, lines[len(lines)-tail:]...)
		return truncateWarpResultWithEllipsis(strings.Join(compacted, "\n"), maxChars)
	}
	if warpResultRuneLen(text) > maxChars {
		return truncateWarpResultWithEllipsis(compactWarpResultText(text, maxChars-32), maxChars)
	}
	return strings.Join(lines, "\n")
}

func looksLikeWarpNumberedReadOutput(text string) bool {
	lines := nonEmptyWarpLines(text)
	if len(lines) < 8 {
		return false
	}
	numbered := 0
	for _, line := range lines {
		if _, ok := splitWarpNumberedLine(line); ok {
			numbered++
		}
	}
	return numbered*100/len(lines) >= 70
}

func looksLikeWarpCodeReadOutput(text string) bool {
	lines := nonEmptyWarpLines(text)
	if len(lines) == 0 {
		return false
	}
	sample := minWarpInt(len(lines), 80)
	signals := 0
	for _, line := range lines[:sample] {
		if isWarpCodeStructureLine(warpLineContent(line)) {
			signals++
		}
	}
	return signals >= 4 || signals*100/sample >= 20
}

func compactWarpCodeReadOutput(text string, historyMode bool) string {
	lines := nonEmptyWarpLines(text)
	if len(lines) == 0 {
		return ""
	}

	maxChars := warpCurrentReadOutputLimit
	head := 18
	tail := 4
	maxStructure := 24
	if historyMode {
		maxChars = warpHistoryReadOutputLimit
		head = 10
		tail = 2
		maxStructure = 12
	}
	if warpResultRuneLen(text) <= maxChars {
		return strings.Join(lines, "\n")
	}

	indices := make([]int, 0, head+tail+maxStructure)
	seen := make(map[int]struct{}, head+tail+maxStructure)
	add := func(idx int) {
		if idx < 0 || idx >= len(lines) {
			return
		}
		if _, ok := seen[idx]; ok {
			return
		}
		seen[idx] = struct{}{}
		indices = append(indices, idx)
	}

	for i := 0; i < minWarpInt(head, len(lines)); i++ {
		add(i)
	}

	structural := 0
	for i := head; i < len(lines)-tail && structural < maxStructure; i++ {
		if !isWarpCodeStructureLine(warpLineContent(lines[i])) {
			continue
		}
		add(i)
		structural++
	}

	startTail := len(lines) - tail
	if startTail < 0 {
		startTail = 0
	}
	for i := startTail; i < len(lines); i++ {
		add(i)
	}

	if len(indices) <= head+tail {
		if historyMode {
			return compactHistoricalWarpToolResult(text)
		}
		return compactCurrentWarpToolResult(text)
	}

	sort.Ints(indices)

	compacted := make([]string, 0, len(indices)+4)
	prev := -1
	for _, idx := range indices {
		if prev >= 0 && idx > prev+1 {
			compacted = append(compacted, fmt.Sprintf("[read output summary: omitted %d lines]", idx-prev-1))
		}
		compacted = append(compacted, lines[idx])
		prev = idx
	}
	if omitted := len(lines) - len(indices); omitted > 0 {
		compacted = append(compacted, fmt.Sprintf("[read output summary: kept %d header lines, %d structural lines, and %d tail lines; omitted %d other lines]", minWarpInt(head, len(lines)), structural, minWarpInt(tail, len(lines)), omitted))
	}

	return truncateWarpResultWithEllipsis(strings.Join(compacted, "\n"), maxChars)
}

func splitWarpNumberedLine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}
	idx := strings.IndexRune(line, '→')
	if idx <= 0 {
		return line, false
	}
	prefix := strings.TrimSpace(line[:idx])
	if prefix == "" {
		return strings.TrimSpace(line[idx+len("→"):]), false
	}
	for _, r := range prefix {
		if r < '0' || r > '9' {
			return strings.TrimSpace(line[idx+len("→"):]), false
		}
	}
	return strings.TrimSpace(line[idx+len("→"):]), true
}

func warpLineContent(line string) string {
	content, ok := splitWarpNumberedLine(line)
	if !ok {
		return strings.TrimSpace(line)
	}
	return strings.TrimSpace(content)
}

func isWarpCodeStructureLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	for _, prefix := range []string{
		"import ", "from ", "package ", "func ", "type ", "interface ", "class ",
		"struct ", "enum ", "const ", "var ", "let ", "def ", "async def ", "fn ",
		"pub ", "impl ", "trait ", "export ", "@", "router.", "app.", "bp.",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	for _, marker := range []string{
		".get(", ".post(", ".put(", ".delete(", ".patch(", ".route(",
		"if __name__ == ", "todo", "fixme",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	trimmed := strings.TrimSpace(line)
	if strings.Contains(trimmed, "(") && strings.Contains(trimmed, ")") {
		if strings.HasSuffix(trimmed, ":") || strings.HasSuffix(trimmed, "{") {
			return true
		}
	}
	return false
}

func nonEmptyWarpLines(text string) []string {
	rawLines := strings.Split(text, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func containsWarpString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func minWarpInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func looksLikeWarpLargeReadOutput(text string) bool {
	if !looksLikeWarpNumberedReadOutput(text) {
		return false
	}
	lines := nonEmptyWarpLines(text)
	return len(lines) >= warpDedupLargeReadLineCount || warpResultRuneLen(text) >= warpDedupLargeReadRuneThreshold
}

func compactWarpResultText(text string, targetChars int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if targetChars <= 0 || warpResultRuneLen(text) <= targetChars {
		return text
	}

	lines := strings.Split(text, "\n")
	keywords := []string{
		"error", "failed", "todo", "fix", "bug", "constraint", "must", "important",
		"错误", "失败", "修复", "约束", "必须", "结论", "决定", "下一步", "风险",
		"tool", "read", "write", "edit", "bash", "path", "file",
	}

	selected := make([]string, 0, 6)
	seen := make(map[string]struct{})
	add := func(line string) {
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		line = strings.Join(strings.Fields(line), " ")
		line = truncateWarpResultWithEllipsis(line, 180)
		if _, ok := seen[line]; ok {
			return
		}
		seen[line] = struct{}{}
		selected = append(selected, line)
	}

	for _, line := range lines {
		lower := strings.ToLower(line)
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				add(line)
				break
			}
		}
		if len(selected) >= 6 {
			break
		}
	}
	for _, line := range lines {
		if len(selected) >= 6 {
			break
		}
		add(line)
	}
	if len(lines) > 0 {
		add(lines[len(lines)-1])
	}
	if len(selected) == 0 {
		return truncateWarpResultWithEllipsis(text, targetChars)
	}
	joined := strings.Join(selected, " | ")
	joined = truncateWarpResultWithEllipsis(joined, targetChars-32)
	return fmt.Sprintf("[compressed %d chars] %s", warpResultRuneLen(text), joined)
}

func warpResultRuneLen(text string) int {
	return len([]rune(text))
}

func truncateWarpResultWithEllipsis(text string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return string(runes[:maxLen]) + "…[truncated]"
}
