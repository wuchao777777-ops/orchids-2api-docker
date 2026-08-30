package grok

import (
	"slices"
	"strings"
	"time"

	"orchids-api/internal/store"
)

const (
	ProviderBuild   = "build"
	ProviderWeb     = "web"
	ProviderConsole = "console"
)

const modelSnapshotTTL = 6 * time.Hour

// ProviderForAccount is the sole compatibility bridge for legacy Grok rows.
// New accounts always persist a provider; old OAuth rows are Build and old SSO
// rows remain Web until the administrator explicitly creates a Console account.
func ProviderForAccount(acc *store.Account) string {
	if acc == nil || !strings.EqualFold(strings.TrimSpace(acc.AccountType), "grok") {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(acc.GrokProvider)) {
	case ProviderBuild, ProviderWeb, ProviderConsole:
		return strings.ToLower(strings.TrimSpace(acc.GrokProvider))
	}
	if strings.EqualFold(strings.TrimSpace(acc.CredentialType), "oauth") {
		return ProviderBuild
	}
	return ProviderWeb
}

func NormalizeProvider(acc *store.Account) bool {
	if acc == nil || !strings.EqualFold(strings.TrimSpace(acc.AccountType), "grok") {
		return false
	}
	provider := ProviderForAccount(acc)
	if provider == "" || acc.GrokProvider == provider {
		return false
	}
	acc.GrokProvider = provider
	return true
}

func AccountSupportsModel(acc *store.Account, modelID string) bool {
	if acc == nil {
		return false
	}
	modelID = strings.TrimSpace(modelID)
	// Do not block a just-authorized account before its first non-billable
	// catalog sync. Once observed, the catalog is authoritative.
	if len(acc.GrokModels) == 0 {
		return true
	}
	for _, model := range acc.GrokModels {
		if strings.EqualFold(strings.TrimSpace(model), modelID) {
			return true
		}
	}
	return false
}

func CLIModelsNeedSync(acc *store.Account, now time.Time) bool {
	if ProviderForAccount(acc) != ProviderBuild || len(acc.GrokModels) == 0 || acc.GrokModelsSyncedAt.IsZero() {
		return true
	}
	return !now.Before(acc.GrokModelsSyncedAt.Add(modelSnapshotTTL))
}

func ApplyCLIModels(acc *store.Account, models []string, now time.Time) bool {
	if acc == nil {
		return false
	}
	seen := make(map[string]struct{}, len(models)+3)
	normalized := make([]string, 0, len(models)+3)
	appendModel := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		normalized = append(normalized, model)
	}
	for _, model := range models {
		appendModel(model)
	}

	// Build's /models response is intentionally sparse. grok2api treats the
	// composer as a stable OAuth capability and exposes the 4.5 compatibility
	// alias whenever the account advertises 4.6.
	if ProviderForAccount(acc) == ProviderBuild {
		appendModel("grok-composer-2.5-fast")
		if _, ok := seen["grok-4.6"]; ok {
			appendModel("grok-4.5")
		}
		// Video 1.5 is a Super-only Build capability and is not reliable in the
		// catalog response. Do not retain an advertised value on lower tiers.
		videoID := "grok-imagine-video-1.5"
		if strings.Contains(strings.ToLower(strings.TrimSpace(acc.Subscription)), "super") {
			appendModel(videoID)
		} else if _, ok := seen[videoID]; ok {
			delete(seen, videoID)
			filtered := normalized[:0]
			for _, model := range normalized {
				if !strings.EqualFold(model, videoID) {
					filtered = append(filtered, model)
				}
			}
			normalized = filtered
		}
	}
	if len(normalized) == 0 {
		return false
	}
	changed := !slices.EqualFunc(acc.GrokModels, normalized, strings.EqualFold)
	acc.GrokModels = normalized
	acc.GrokModelsSyncedAt = now.UTC()
	return changed
}
