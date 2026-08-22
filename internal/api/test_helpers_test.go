package api

import (
	"testing"

	"github.com/alicebob/miniredis/v2"

	"orchids-api/internal/store"
)

func newTestStore(t *testing.T, prefix string) (*store.Store, *miniredis.Miniredis) {
	t.Helper()
	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{RedisAddr: mini.Addr(), RedisPrefix: prefix})
	if err != nil {
		mini.Close()
		t.Fatalf("store.New() error = %v", err)
	}
	return s, mini
}
