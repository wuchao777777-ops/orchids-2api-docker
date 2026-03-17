package provider

import (
	"orchids-api/internal/bolt"
	"orchids-api/internal/config"
	"orchids-api/internal/store"
)

type boltProvider struct{}

func NewBoltProvider() Provider { return boltProvider{} }

func (boltProvider) Name() string { return "bolt" }

func (boltProvider) NewClient(acc *store.Account, cfg *config.Config) interface{} {
	return bolt.NewFromAccount(acc, cfg)
}
