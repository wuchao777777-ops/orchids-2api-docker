package grok

import (
	"strings"
)

// ModelSpec defines one public model and how it maps to Grok upstream fields.
type ModelSpec struct {
	ID            string
	Name          string
	UpstreamModel string
	ModelMode     string
	IsImage       bool
	IsVideo       bool
}

// SupportedModels is the Go-native model table ported from grok2api behavior.
var SupportedModels = []ModelSpec{
	{ID: "grok-3", Name: "Grok 3", UpstreamModel: "grok-3"},
	{ID: "grok-3-mini", Name: "Grok 3 Mini", UpstreamModel: "grok-3-mini"},
	{ID: "grok-3-thinking", Name: "Grok 3 Thinking", UpstreamModel: "grok-3-thinking"},
	{ID: "grok-4", Name: "Grok 4", UpstreamModel: "grok-4"},
	{ID: "grok-4-mini", Name: "Grok 4 Mini", UpstreamModel: "grok-4-mini"},
	{ID: "grok-4-thinking", Name: "Grok 4 Thinking", UpstreamModel: "grok-4-thinking"},
	{ID: "grok-4-heavy", Name: "Grok 4 Heavy", UpstreamModel: "grok-4-heavy"},
	{ID: "grok-4.1-mini", Name: "Grok 4.1 Mini", UpstreamModel: "grok-4.1-mini"},
	{ID: "grok-4.1-fast", Name: "Grok 4.1 Fast", UpstreamModel: "grok-4.1-fast"},
	{ID: "grok-4.1-expert", Name: "Grok 4.1 Expert", UpstreamModel: "grok-4.1-expert"},
	{ID: "grok-4.1-thinking", Name: "Grok 4.1 Thinking", UpstreamModel: "grok-4.1-thinking"},
	{ID: "grok-imagine-1.0", Name: "Grok Imagine 1.0", UpstreamModel: "grok-imagine-1.0", IsImage: true},
	{ID: "grok-imagine-1.0-edit", Name: "Grok Imagine 1.0 Edit", UpstreamModel: "grok-imagine-1.0-edit", IsImage: true},
	{ID: "grok-imagine-1.0-video", Name: "Grok Imagine 1.0 Video", UpstreamModel: "grok-imagine-1.0-video", IsVideo: true},
}

var modelByID = func() map[string]ModelSpec {
	out := make(map[string]ModelSpec, len(SupportedModels))
	for _, m := range SupportedModels {
		out[strings.ToLower(strings.TrimSpace(m.ID))] = m
	}
	return out
}()

var deprecatedModelIDSet = map[string]struct{}{
	"grok-4.2": {},
}

func IsDeprecatedModelID(modelID string) bool {
	id := normalizeModelID(modelID)
	if id == "" {
		return false
	}
	_, deprecated := deprecatedModelIDSet[id]
	return deprecated
}

func normalizeModelID(modelID string) string {
	m := strings.ToLower(strings.TrimSpace(modelID))
	// Common typo compatibility: gork-* -> grok-*
	if strings.HasPrefix(m, "gork-") {
		m = "grok-" + strings.TrimPrefix(m, "gork-")
	}
	// Version alias compatibility: grok-4-2 -> grok-4.2, grok-4-1-thinking -> grok-4.1-thinking.
	if strings.HasPrefix(m, "grok-") {
		rest := strings.TrimPrefix(m, "grok-")
		parts := strings.SplitN(rest, "-", 3)
		if len(parts) >= 2 && isDigits(parts[0]) && isDigits(parts[1]) {
			if len(parts) == 2 {
				return "grok-" + parts[0] + "." + parts[1]
			}
			return "grok-" + parts[0] + "." + parts[1] + "-" + parts[2]
		}
	}
	return m
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func ResolveModel(modelID string) (ModelSpec, bool) {
	m, ok := modelByID[normalizeModelID(modelID)]
	return m, ok
}

func resolveDynamicTextModel(modelID string) (ModelSpec, bool) {
	id := normalizeModelID(modelID)
	if id == "" {
		return ModelSpec{}, false
	}
	if IsDeprecatedModelID(id) {
		return ModelSpec{}, false
	}
	// Keep image/video models explicit; only auto-resolve text models.
	if strings.HasPrefix(id, "grok-imagine-") {
		return ModelSpec{}, false
	}
	if !strings.HasPrefix(id, "grok-") {
		return ModelSpec{}, false
	}
	return ModelSpec{
		ID:            id,
		Name:          id,
		UpstreamModel: id,
	}, true
}

// ResolveModelOrDynamic first resolves built-in model specs, then falls back to
// dynamic text-model passthrough for newly introduced grok-* models.
func ResolveModelOrDynamic(modelID string) (ModelSpec, bool) {
	if spec, ok := ResolveModel(modelID); ok {
		return spec, true
	}
	return resolveDynamicTextModel(modelID)
}
