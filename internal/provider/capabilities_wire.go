package provider

import (
	"orchids-api/internal/cline"
	"orchids-api/internal/qoder"
	"orchids-api/internal/store"
	"orchids-api/internal/workbuddy"
)

// channelCapabilities adapts one channel's catalog functions to Capabilities.
//
// It lives here rather than in each provider package because this package is
// the one that imports every provider: a provider importing back to register
// itself would be a cycle. Registration therefore happens at the single place
// that already knows all of them, which is also the point of the seam — one
// table instead of a type switch in the shared pipeline.
type channelCapabilities struct {
	supportsModel       func(acc *store.Account, model string) bool
	isFreeModel         func(acc *store.Account, model string) bool
	contextWindows      func(acc *store.Account) (map[string]int, map[string]int)
	honorsModelCooldown bool
}

func (c channelCapabilities) SupportsModel(acc *store.Account, model string) bool {
	if c.supportsModel == nil {
		return true
	}
	return c.supportsModel(acc, model)
}

func (c channelCapabilities) IsFreeModel(acc *store.Account, model string) bool {
	if c.isFreeModel == nil {
		return false
	}
	return c.isFreeModel(acc, model)
}

func (c channelCapabilities) ContextWindows(acc *store.Account) (map[string]int, map[string]int) {
	if c.contextWindows == nil {
		return nil, nil
	}
	return c.contextWindows(acc)
}

func (c channelCapabilities) HonorsModelCooldown() bool { return c.honorsModelCooldown }

// registerCapabilities wires every channel this seam serves. Grok is absent on
// purpose: it has its own handler and is never built through this seam.
func registerCapabilities() {
	RegisterCapabilities("cline", channelCapabilities{
		// An empty snapshot means "unknown", and an unknown catalog must not
		// block a model: refusing it would break a channel whose snapshot has
		// not synced yet.
		supportsModel: func(acc *store.Account, model string) bool {
			if acc == nil || len(acc.ClineModelIDs) == 0 {
				return true
			}
			return cline.CatalogSupportsModel(acc.ClineModelIDs, model)
		},
		// Cline has no free tier: every model is billed against the account.
		isFreeModel: func(acc *store.Account, model string) bool { return false },
	})
	RegisterCapabilities("qoder", channelCapabilities{
		isFreeModel: func(acc *store.Account, model string) bool {
			if acc == nil {
				return false
			}
			return qoder.IsFreeModel(acc.QoderModelIDs, model)
		},
		contextWindows: func(acc *store.Account) (map[string]int, map[string]int) {
			if acc == nil {
				return nil, nil
			}
			return qoder.CatalogContextWindows(acc.QoderModelIDs), nil
		},
		honorsModelCooldown: true,
	})
	RegisterCapabilities("workbuddy", channelCapabilities{
		isFreeModel: func(acc *store.Account, model string) bool {
			if acc == nil {
				return false
			}
			return workbuddy.IsFreeModelInCatalog(acc.WorkBuddyModelIDs, model)
		},
		contextWindows: func(acc *store.Account) (map[string]int, map[string]int) {
			if acc == nil {
				return nil, nil
			}
			return workbuddy.CatalogContextWindows(acc.WorkBuddyModelIDs)
		},
		honorsModelCooldown: true,
	})
}

func init() { registerCapabilities() }
