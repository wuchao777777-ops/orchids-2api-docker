// Package poolstate summarizes a channel's account pool.
//
// The console and the alert engine ask the same question with different
// criteria, so the summary takes an explicit mode rather than each caller
// growing its own copy of the loop.
package poolstate

import (
	"strings"
	"time"

	"orchids-api/internal/accountpolicy"
	"orchids-api/internal/store"
)

// Mode picks which definition of "needs attention" a summary uses.
type Mode int

const (
	// NeedingLoginRefused counts an account whose credential the upstream
	// refused. A refused credential needs attention even while it is cooling
	// down: it will not start working again on its own.
	//
	// This is the console's view — "this account was rejected".
	NeedingLoginRefused Mode = iota
	// NeedingLoginReverify counts an account whose reverify deadline has
	// passed. That deadline is a scheduler decision, not a login status, so an
	// account can be cooling down and still be due for a recheck.
	//
	// This is the alert engine's view — "this account is due for a review".
	NeedingLoginReverify
)

// Summary is one channel's pool counts.
type Summary struct {
	Enabled        int
	Available      int
	NeedingLogin   int
	ModelCooldowns int
}

// Counts summarizes the accounts of one channel.
//
// Only enabled accounts are counted: a disabled account is not part of the pool
// the load balancer can draw from, and counting it would understate how close
// the channel is to exhaustion.
func Counts(accounts []*store.Account, channel string, now time.Time, mode Mode) Summary {
	var out Summary
	for _, acc := range accounts {
		if acc == nil || !strings.EqualFold(strings.TrimSpace(acc.AccountType), channel) {
			continue
		}
		if !acc.Enabled {
			continue
		}
		out.Enabled++
		if mode == NeedingLoginRefused {
			if strings.TrimSpace(acc.StatusCode) == "401" {
				out.NeedingLogin++
			}
		} else if accountpolicy.NeedsReverify(acc, now) {
			out.NeedingLogin++
		}
		for model, until := range acc.ModelCooldowns {
			if strings.TrimSpace(model) != "" && until.After(now) {
				out.ModelCooldowns++
			}
		}
		if accountpolicy.AccountHeld(acc, now) {
			continue
		}
		out.Available++
	}
	return out
}
