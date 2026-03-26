package provider

import (
	"orchids-api/internal/config"
	"orchids-api/internal/store"
	"orchids-api/internal/v0"
)

type v0Provider struct{}

func NewV0Provider() Provider { return v0Provider{} }

func (v0Provider) Name() string { return "v0" }

func (v0Provider) NewClient(acc *store.Account, cfg *config.Config) interface{} {
	return v0.NewFromAccount(acc, cfg)
}
