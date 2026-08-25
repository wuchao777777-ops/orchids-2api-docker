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
	seen := make(map[string]struct{}, len(models))
	normalized := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, model)
	}
	if len(normalized) == 0 {
		return false
	}
	changed := !slices.EqualFunc(acc.GrokModels, normalized, strings.EqualFold)
	acc.GrokModels = normalized
	acc.GrokModelsSyncedAt = now.UTC()
	return changed
}
