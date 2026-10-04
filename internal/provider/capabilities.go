package provider

import (
	"strings"

	"orchids-api/internal/store"
)

// Capabilities is what the shared inference pipeline needs to know about a
// channel's catalog. It exists so internal/handler can ask a channel whether a
// model is supported or free without importing three provider packages to do it.
type Capabilities interface {
	// SupportsModel reports whether an account's catalog advertises the model.
	// An empty catalog means "unknown", which is treated as supported: refusing
	// an unseen model would break a channel whose snapshot has not synced yet.
	SupportsModel(acc *store.Account, model string) bool
	// IsFreeModel reports whether the model is currently free for the account.
	IsFreeModel(acc *store.Account, model string) bool
	// ContextWindows returns the input and output token budgets the channel
	// projects for an account's catalog. Either map may be nil.
	ContextWindows(acc *store.Account) (input, output map[string]int)
	// HonorsModelCooldown reports whether the channel's selection consults the
	// per-model cooldown its own verdicts write.
	HonorsModelCooldown() bool
}

// capabilities maps an account type to its channel capabilities. Grok is absent
// on purpose: it has its own handler and never goes through this seam.
var capabilities = map[string]Capabilities{}

// RegisterCapabilities publishes the capability set one provider implements.
// Each provider calls it from an init-like registration so this package — which
// every provider already feeds — stays the single place the mapping lives.
func RegisterCapabilities(accountType string, caps Capabilities) {
	if caps == nil {
		return
	}
	capabilities[strings.ToLower(strings.TrimSpace(accountType))] = caps
}

// CapabilitiesFor returns the capability set for an account type, or nil when
// the channel has not registered one.
func CapabilitiesFor(accountType string) Capabilities {
	return capabilities[strings.ToLower(strings.TrimSpace(accountType))]
}

// SupportsModel reports whether the channel advertises the model for the
// account. It is true when no capabilities are registered: an unknown channel
// must not be blocked by a lookup this seam cannot answer.
func SupportsModel(accountType string, acc *store.Account, model string) bool {
	caps := CapabilitiesFor(accountType)
	if caps == nil {
		return true
	}
	return caps.SupportsModel(acc, model)
}

// IsFreeModel reports whether the channel treats the model as free.
func IsFreeModel(accountType string, acc *store.Account, model string) bool {
	caps := CapabilitiesFor(accountType)
	if caps == nil {
		return false
	}
	return caps.IsFreeModel(acc, model)
}

// ContextWindows returns the channel's projected token budgets.
func ContextWindows(accountType string, acc *store.Account) (input, output map[string]int) {
	caps := CapabilitiesFor(accountType)
	if caps == nil {
		return nil, nil
	}
	return caps.ContextWindows(acc)
}

// HonorsModelCooldown reports whether the channel scopes cooldowns per model.
func HonorsModelCooldown(accountType string) bool {
	caps := CapabilitiesFor(accountType)
	if caps == nil {
		return false
	}
	return caps.HonorsModelCooldown()
}
