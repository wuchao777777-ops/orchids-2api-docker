package modelpolicy

import (
	"slices"
	"strings"
)

var stableGrokTextModelIDs = []string{
	"grok-420",
	"grok-3-mini",
	"grok-4-thinking",
	"grok-4.1-expert",
}

var stableGrokTextModelAllowlist = func() map[string]struct{} {
	out := make(map[string]struct{}, len(stableGrokTextModelIDs))
	for _, id := range stableGrokTextModelIDs {
		out[id] = struct{}{}
	}
	return out
}()

var publicGrokModelAllowlist = map[string]struct{}{
	"grok-3-mini":                  {},
	"grok-4-thinking":              {},
	"grok-4.1-expert":              {},
	"grok-420":                     {},
	"grok-4.20-0309-reasoning":     {},
	"grok-4.20-0309-non-reasoning": {},
	"grok-imagine-1.0":             {},
	"grok-imagine-1.0-fast":        {},
	"grok-imagine-1.0-edit":        {},
	"grok-imagine-1.0-video":       {},
}

func IsPublicGrokModelID(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return false
	}
	_, ok := publicGrokModelAllowlist[id]
	return ok
}

func IsStableGrokTextModelID(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return false
	}
	_, ok := stableGrokTextModelAllowlist[id]
	return ok
}

func StableGrokTextModelIDs() []string {
	return slices.Clone(stableGrokTextModelIDs)
}

func IsVisibleGrokModel(modelID string, verified bool) bool {
	return IsPublicGrokModelID(modelID) || verified
}
