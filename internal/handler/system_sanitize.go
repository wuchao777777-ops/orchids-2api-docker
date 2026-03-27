package handler

import (
	"strings"

	"orchids-api/internal/config"
)

const (
	ccEntrypointModeAuto  = "auto"
	ccEntrypointModeKeep  = "keep"
	ccEntrypointModeStrip = "strip"
)

// sanitizeSystemItems 根据配置移除可能触发 coding agent 的 cc_entrypoint。
func sanitizeSystemItems(system SystemItems, isWarp bool, isPuter bool, cfg *config.Config) (SystemItems, bool) {
	if len(system) == 0 || cfg == nil {
		return system, false
	}

	mode := strings.ToLower(strings.TrimSpace(cfg.OrchidsCCEntrypointMode))
	if mode == "" {
		mode = ccEntrypointModeAuto
	}
	switch mode {
	case ccEntrypointModeKeep, ccEntrypointModeAuto, ccEntrypointModeStrip:
	default:
		mode = ccEntrypointModeAuto
	}

	stripAll := mode == ccEntrypointModeStrip
	changed := false
	out := make(SystemItems, 0, len(system))
	for _, item := range system {
		if item.Type != "text" || strings.TrimSpace(item.Text) == "" {
			out = append(out, item)
			continue
		}
		newText, textChanged := stripCCEntrypoint(item.Text, stripAll)
		if textChanged {
			changed = true
		}
		if strings.TrimSpace(newText) == "" {
			if strings.TrimSpace(item.Text) != "" {
				changed = true
			}
			continue
		}
		item.Text = newText
		out = append(out, item)
	}

	if isWarp {
		filtered, dropped := filterWarpSystemItems(out, false)
		if dropped {
			out = filtered
			changed = true
		}
	}

	if isPuter {
		filtered, dropped := filterWarpSystemItems(out, true)
		if dropped {
			out = filtered
			changed = true
		}
	}

	if !changed {
		return system, false
	}
	return out, true
}

func filterWarpSystemItems(system SystemItems, isPuter bool) (SystemItems, bool) {
	if len(system) == 0 {
		return system, false
	}
	dropped := false
	out := make(SystemItems, 0, len(system))
	for _, item := range system {
		if item.Type != "text" || strings.TrimSpace(item.Text) == "" {
			out = append(out, item)
			continue
		}
		if isClaudeCodeSystem(item.Text) || (isPuter && isClaudeCodeEnvironment(item.Text)) {
			dropped = true
			continue
		}
		out = append(out, item)
	}
	return out, dropped
}

func isClaudeCodeSystem(text string) bool {
	lower := strings.ToLower(text)
	// 强特征：单独出现即可判定
	strongSignals := []string{
		"claude code",
		"claude agent sdk",
		"you are an interactive cli tool",
		"todowrite tool",
		"skill tool",
	}
	for _, sig := range strongSignals {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	// 弱特征：需要至少命中 2 个才判定，避免误判正常内容
	weakSignals := []string{
		"task tool",
		"vscode",
		"system-reminder",
		"claude-sonnet",
		"claude-opus",
		"enterplanmode",
		"exitplanmode",
	}
	hits := 0
	for _, sig := range weakSignals {
		if strings.Contains(lower, sig) {
			hits++
			if hits >= 2 {
				return true
			}
		}
	}
	return false
}

func isClaudeCodeEnvironment(text string) bool {
	lower := strings.ToLower(text)
	signals := []string{
		"# environment",
		"primary working directory:",
		"gitstatus:",
		"recent commits:",
		"you are powered by the model named",
		"claude code is available as a cli",
	}
	hits := 0
	for _, sig := range signals {
		if strings.Contains(lower, sig) {
			hits++
		}
	}
	return hits >= 2
}

func stripCCEntrypoint(text string, stripAll bool) (string, bool) {
	if !strings.Contains(strings.ToLower(text), "cc_entrypoint=") {
		return text, false
	}

	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		newLine, removed := stripCCEntrypointFromLine(line, stripAll)
		if removed {
			changed = true
		}
		if strings.TrimSpace(newLine) == "" && strings.TrimSpace(line) != "" {
			continue
		}
		out = append(out, newLine)
	}

	if !changed {
		return text, false
	}
	return strings.Join(out, "\n"), true
}

func stripCCEntrypointFromLine(line string, stripAll bool) (string, bool) {
	lower := strings.ToLower(line)
	if !strings.Contains(lower, "cc_entrypoint=") {
		return line, false
	}

	prefix, rest, hasColon := strings.Cut(line, ":")
	if hasColon {
		newRest, removed := stripCCEntrypointFromAssignments(rest, stripAll)
		if !removed {
			return line, false
		}
		if strings.TrimSpace(newRest) == "" {
			return "", true
		}
		newLine := strings.TrimRight(prefix, " ") + ": " + newRest
		if strings.HasSuffix(strings.TrimSpace(line), ";") && !strings.HasSuffix(strings.TrimSpace(newLine), ";") {
			newLine += ";"
		}
		return newLine, true
	}

	newLine, removed := stripCCEntrypointFromAssignments(line, stripAll)
	if !removed {
		return line, false
	}
	return newLine, true
}

func stripCCEntrypointFromAssignments(input string, stripAll bool) (string, bool) {
	parts := strings.Split(input, ";")
	kept := make([]string, 0, len(parts))
	removed := false
	for _, part := range parts {
		seg := strings.TrimSpace(part)
		if seg == "" {
			continue
		}
		lower := strings.ToLower(seg)
		if strings.HasPrefix(lower, "cc_entrypoint=") {
			if stripAll || shouldStripCCEntrypointValue(seg) {
				removed = true
				continue
			}
		}
		kept = append(kept, seg)
	}
	if !removed {
		return input, false
	}
	if len(kept) == 0 {
		return "", true
	}
	return strings.Join(kept, "; "), true
}

func shouldStripCCEntrypointValue(seg string) bool {
	parts := strings.SplitN(seg, "=", 2)
	if len(parts) != 2 {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(parts[1]))
	switch value {
	case "claude-vscode", "claude-code":
		return true
	default:
		return false
	}
}
