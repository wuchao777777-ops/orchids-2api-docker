package qoder

import (
	"github.com/goccy/go-json"
	"orchids-api/internal/util"
	"strings"
)

// normalizeCommandEscalation removes an orphan escalation reason from known
// command tools. It never invents a permission request or changes the command.
func normalizeCommandEscalation(name, arguments string) string {
	input := util.NormalizeToolInput(arguments)
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndex(name, "__"); i >= 0 {
		name = name[i+2:]
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	switch name {
	case "bash", "exec_command", "run_command", "execute_command", "run_terminal_command", "run_shell_command":
	default:
		return input
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(input), &fields) != nil {
		return input
	}
	if _, exists := fields["justification"]; !exists {
		return input
	}
	var command string
	if json.Unmarshal(fields["command"], &command) != nil || strings.TrimSpace(command) == "" {
		if json.Unmarshal(fields["cmd"], &command) != nil || strings.TrimSpace(command) == "" {
			return input
		}
	}
	if permission, exists := fields["sandbox_permissions"]; exists {
		var value string
		if string(permission) != "null" && (json.Unmarshal(permission, &value) != nil || strings.TrimSpace(value) != "") {
			return input
		}
	}
	delete(fields, "justification")
	encoded, err := json.Marshal(fields)
	if err != nil {
		return input
	}
	return string(encoded)
}
