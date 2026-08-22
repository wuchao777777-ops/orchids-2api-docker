// Package provider defines a minimal registry for upstream providers.
// It replaces hardcoded type-switch logic with a table-driven dispatch.
package provider

import (
	"strings"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
)

// Provider abstracts the creation of upstream clients for a given account type.
type Provider interface {
	// Name returns the provider identifier (for example, "warp" or "puter").
	Name() string
	// NewClient creates an upstream client for the given account and config.
	// The returned value must satisfy the handler.UpstreamClient interface.
	NewClient(acc *store.Account, cfg *config.Config) interface{}
}

// Registry maps account types to provider implementations.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds a provider under the given name (case-insensitive).
func (r *Registry) Register(name string, p Provider) {
	r.providers[normalize(name)] = p
}

// Get retrieves a provider by name. Returns nil if not found.
func (r *Registry) Get(name string) Provider {
	return r.providers[normalize(name)]
}

func normalize(s string) string {
	return strings.ToLower(s)
}
