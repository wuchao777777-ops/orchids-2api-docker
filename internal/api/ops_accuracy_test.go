package api

import (
	"orchids-api/internal/poolstate"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
	"testing"
	"time"
)

func TestPoolCountsLoginIndependentOfCooldown(t *testing.T) {
	now := time.Now()
	accounts := []*store.Account{
		{ID: 1, AccountType: "grok", Enabled: true, StatusCode: "401", LastAttempt: now, VerifiedAt: now, ModelCooldowns: map[string]time.Time{"model": now.Add(time.Minute)}},
		{ID: 2, AccountType: "grok", Enabled: true, StatusCode: "401", LastAttempt: now.Add(-time.Hour), VerifiedAt: now.Add(-time.Hour)},
		{ID: 3, AccountType: "grok", Enabled: true},
		{ID: 4, AccountType: "grok", Enabled: false, StatusCode: "401"},
		{ID: 5, AccountType: "grok", Enabled: true, StatusCode: "401"},
	}
	pool := poolstate.Counts(accounts, "grok", now, poolstate.NeedingLoginRefused)
	enabled, available, login, cooldowns := pool.Enabled, pool.Available, pool.NeedingLogin, pool.ModelCooldowns
	testutil.Falsef(t, enabled != 4 || available != 2 || login != 3 || cooldowns != 1, "enabled=%d available=%d login=%d cooldowns=%d", enabled, available, login, cooldowns)
	accounts[0].StatusCode = ""
	login = poolstate.Counts(accounts, "grok", now, poolstate.NeedingLoginRefused).NeedingLogin
	testutil.Equal(t, login, 2)
}
