package grok

import (
	"strings"

	"orchids-api/internal/config"
	"orchids-api/internal/modelpolicy"
)

// UpstreamKind selects which Grok upstream protocol serves a model.
type UpstreamKind int

const (
	// UpstreamAuto derives the upstream from ModelSpec fields and config.
	UpstreamAuto UpstreamKind = iota
	// UpstreamAppChat is the grok.com/rest/app-chat/... website protocol.
	UpstreamAppChat
	// UpstreamConsole is console.x.ai/v1/responses + DPoP.
	UpstreamConsole
	// UpstreamCLI is cli-chat-proxy.grok.com/v1 + OAuth Bearer.
	UpstreamCLI
)

// ModelSpec defines one public model and how it maps to Grok upstream fields.
type ModelSpec struct {
	ID            string
	Name          string
	UpstreamModel string
	ModelMode     string
	ModeID        string
	ConsoleModel  string
	Tier          int
	PreferBest    bool
	IsImage       bool
	IsVideo       bool
	// Upstream explicitly routes the model; UpstreamAuto derives from fields.
	Upstream UpstreamKind
}

const (
	grokTierBasic = iota
	grokTierLite
	grokTierSuper
	grokTierHeavy
)

// SupportedModels is the deliberately small public compatibility table. Build
// OAuth capability snapshots remain authoritative for text models; this table
// only describes currently supported routes, not a historical model archive.
var SupportedModels = []ModelSpec{
	{ID: "grok-4.5", Name: "Grok 4.5", UpstreamModel: "grok-4.5", Tier: grokTierSuper, Upstream: UpstreamCLI},
	{ID: "grok-4.6", Name: "Grok 4.6", UpstreamModel: "grok-4.6", Tier: grokTierSuper, Upstream: UpstreamCLI},
	{ID: "grok-imagine-image-lite", Name: "Grok Imagine Image Lite", UpstreamModel: "grok-imagine-image-lite", ModelMode: "MODEL_MODE_FAST", ModeID: "fast", Tier: grokTierBasic, IsImage: true},
	{ID: "grok-imagine-image", Name: "Grok Imagine Image", UpstreamModel: "grok-imagine-image", ModelMode: "MODEL_MODE_AUTO", ModeID: "auto", Tier: grokTierSuper, IsImage: true},
	{ID: "grok-imagine-image-quality", Name: "Grok Imagine Image Quality", UpstreamModel: "grok-imagine-image-quality-lite", ModelMode: "MODEL_MODE_AUTO", ModeID: "auto", Tier: grokTierSuper, IsImage: true},
	{ID: "grok-imagine-image-pro", Name: "Grok Imagine Image Pro", UpstreamModel: "grok-imagine-image-pro", ModelMode: "MODEL_MODE_AUTO", ModeID: "auto", Tier: grokTierSuper, IsImage: true},
	{ID: "grok-imagine-image-edit", Name: "Grok Imagine Image Edit", UpstreamModel: "imagine-image-edit", ModelMode: "MODEL_MODE_AUTO", ModeID: "auto", Tier: grokTierSuper, IsImage: true},
	{ID: "grok-imagine-video", Name: "Grok Imagine Video", UpstreamModel: "imagine-video-gen", ModelMode: "MODEL_MODE_AUTO", ModeID: "auto", Tier: grokTierSuper, IsVideo: true},
}

var modelByID = func() map[string]ModelSpec {
	out := make(map[string]ModelSpec, len(SupportedModels))
	for _, m := range SupportedModels {
		out[strings.ToLower(strings.TrimSpace(m.ID))] = m
	}
	return out
}()

func IsDeprecatedModelID(modelID string) bool {
	return modelpolicy.IsDeprecatedGrokModelID(normalizeModelID(modelID))
}

func normalizeModelID(modelID string) string {
	return strings.ToLower(strings.TrimSpace(modelID))
}

func ResolveModel(modelID string) (ModelSpec, bool) {
	m, ok := modelByID[normalizeModelID(modelID)]
	return m, ok
}

func (m ModelSpec) PoolCandidates() []string {
	switch {
	case m.IsImage && normalizeModelID(m.ID) == "grok-imagine-image-lite" && m.Tier == grokTierBasic:
		return []string{"lite", "super", "heavy"}
	case m.PreferBest && m.Tier == grokTierHeavy:
		return []string{"heavy", "basic"}
	case m.PreferBest:
		return []string{"heavy", "super", "lite", "basic"}
	case m.Tier == grokTierHeavy:
		return []string{"heavy", "basic"}
	case m.Tier == grokTierSuper:
		return []string{"super", "lite", "heavy", "basic"}
	case m.Tier == grokTierLite:
		return []string{"lite", "super", "heavy", "basic"}
	default:
		return []string{"basic", "lite", "super", "heavy"}
	}
}

// modelRoutedToCLI reports whether a model should be served via the Build CLI
// upstream (explicit marker or config list).
func modelRoutedToCLI(spec ModelSpec, cfg *config.Config) bool {
	if spec.Upstream != UpstreamAuto {
		return spec.Upstream == UpstreamCLI
	}
	return cfg != nil && cfg.GrokModelIsCLI(spec.ID)
}
