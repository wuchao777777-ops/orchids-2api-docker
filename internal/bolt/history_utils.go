package bolt

import (
	"regexp"
	"strings"
)

var (
	boltReadLineNumberPattern = regexp.MustCompile(`^\s*\d+\s*(?:\||│|:|>\s*|\t|→)\s?(.*)$`)
	boltBareLineNumberPattern = regexp.MustCompile(`^\s*\d+\s*$`)
)

func stripBoltReadLineNumbers(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	sawStructuredLineNumber := false
	for _, line := range lines {
		if boltReadLineNumberPattern.MatchString(strings.TrimRight(line, "\r")) {
			sawStructuredLineNumber = true
			break
		}
	}

	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if matches := boltReadLineNumberPattern.FindStringSubmatch(line); matches != nil {
			line = matches[1]
		} else if sawStructuredLineNumber && boltBareLineNumberPattern.MatchString(line) {
			line = ""
		}
		out = append(out, line)
	}

	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}

	return strings.Join(out, "\n")
}

func isBoltRootProjectProbeToolCall(toolName, toolPath string) bool {
	switch strings.TrimSpace(toolName) {
	case "Glob", "Grep":
	default:
		return false
	}

	path := strings.Trim(strings.TrimSpace(toolPath), "\"'`")
	if path == "" {
		return true
	}

	path = strings.ReplaceAll(path, "\\", "/")
	switch path {
	case ".", "./", "/":
		return true
	default:
		return false
	}
}
