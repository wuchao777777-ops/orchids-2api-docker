package responses

import "strings"

// Warnings returns the compatibility warnings collected so far, in the order
// they were first recorded.
func (s *ToolNormalizationState) Warnings() []string {
	if s == nil {
		return nil
	}
	return s.warnings
}

// AliasFor returns the flat name already assigned to namespace/name without
// inventing one, which is what a caller echoing a tool back needs.
func (s *ToolNormalizationState) AliasFor(namespace, name string) string {
	if s == nil {
		return ""
	}
	return s.aliases[normalizedToolKey(namespace, name)]
}

// normalizedToolKey is the map key shared by Alias, Lookup and AliasFor.
func normalizedToolKey(namespace, name string) string {
	return strings.TrimSpace(namespace) + "\x00" + strings.TrimSpace(name)
}
