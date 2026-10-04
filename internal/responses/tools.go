package responses

import (
	"fmt"
	"regexp"
	"strings"

	"orchids-api/internal/util"
)

// buildToolAliasInvalid matches every character a tool name may not carry once
// a namespace has been folded into it.
var buildToolAliasInvalid = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// CompatibilityWarningsKey is the private payload key the normalization state
// uses to hand its warnings to the caller that will log them.
const CompatibilityWarningsKey = "__orchids_build_compatibility_warnings"

// ToolNormalizationState carries the request-scoped bookkeeping of one tool
// rewrite: the alias assigned to every namespace/name pair, the collisions it
// had to resolve, and the compatibility warnings it produced.
type ToolNormalizationState struct {
	seen     map[string]int
	aliases  map[string]string
	warnings []string
	warning  map[string]struct{}
}

// NewToolNormalizationState returns an empty state.
func NewToolNormalizationState() *ToolNormalizationState {
	return &ToolNormalizationState{seen: map[string]int{}, aliases: map[string]string{}, warning: map[string]struct{}{}}
}

// AddWarning records a compatibility warning once, in the order first seen.
func (s *ToolNormalizationState) AddWarning(value string) {
	if s == nil || value == "" {
		return
	}
	if _, exists := s.warning[value]; exists {
		return
	}
	s.warning[value] = struct{}{}
	s.warnings = append(s.warnings, value)
}

// Lookup returns the alias already assigned to namespace/name, or "" when the
// rewrite never assigned one. Unlike Alias it never invents a name: a caller
// echoing a tool back must not gain an alias the declarations do not carry.
func (s *ToolNormalizationState) Lookup(namespace, name string) string {
	if s == nil {
		return ""
	}
	return s.aliases[strings.TrimSpace(namespace)+"\x00"+strings.TrimSpace(name)]
}

// Alias returns the flat name for namespace/name, assigning and remembering one
// on first use. Collisions are resolved with a numeric suffix.
func (s *ToolNormalizationState) Alias(namespace, name string) string {
	key := strings.TrimSpace(namespace) + "\x00" + strings.TrimSpace(name)
	if alias := s.aliases[key]; alias != "" {
		return alias
	}
	base := BuildToolAlias(namespace, name)
	alias := base
	for index := 2; s.seen[alias] > 0; index++ {
		suffix := fmt.Sprintf("_%d", index)
		limit := 128 - len(suffix)
		prefix := base
		if limit < len(base) {
			prefix = base[:limit]
		}
		alias = strings.TrimSuffix(prefix, "_") + suffix
		s.AddWarning("function_name_collision_renamed")
	}
	s.seen[alias] = 1
	s.aliases[key] = alias
	return alias
}

// TakeWarnings removes the warnings key from a payload and returns its value,
// so the private marker never reaches an upstream.
func TakeCompatibilityWarnings(payload map[string]interface{}) string {
	if payload == nil {
		return ""
	}
	raw := payload[CompatibilityWarningsKey]
	delete(payload, CompatibilityWarningsKey)
	values, _ := raw.([]string)
	return strings.Join(values, ",")
}

// BuildToolAlias folds namespace and name into the one flat name a Chat
// Completions tool can carry, sanitized and length-capped.
func BuildToolAlias(namespace, name string) string {
	value := name
	if strings.TrimSpace(namespace) != "" {
		value = namespace + "__" + name
	}
	value = strings.Trim(buildToolAliasInvalid.ReplaceAllString(value, "_"), "_")
	value = util.FirstNonEmptyUntrimmed(value, "tool")
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}

// ToolAliasIdentity is what the response side needs to undo a rewrite: the
// declared kind, the namespace the name belongs to, the short name, and the
// original declaration (which carries the parameter schema).
type ToolAliasIdentity struct {
	Kind        string
	Namespace   string
	Name        string
	Declaration map[string]interface{}
}

// webSearchCompatibilityFields are the search controls a client sends that the
// Build wire contract rejects. The gateway drops them (keeping only the native
// minimal search tool) instead of letting the whole request fail; the
// operator's intent — a web search — is preserved either way.
var webSearchCompatibilityFields = []string{
	"external_web_access",
	"indexed_web_access",
	"search_content_types",
	"search_context_size",
	"user_location",
	"max_search_results",
	"safe_search",
}

// StripWebSearchControlFields removes the controls above from a hosted search
// tool, returning true when anything was removed.
func StripWebSearchControlFields(tool map[string]interface{}) bool {
	changed := false
	for _, field := range webSearchCompatibilityFields {
		if _, exists := tool[field]; exists {
			delete(tool, field)
			changed = true
		}
	}
	return changed
}
