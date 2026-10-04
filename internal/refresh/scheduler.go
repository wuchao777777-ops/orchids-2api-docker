package refresh

import (
	"context"
	"log/slog"
	"orchids-api/internal/modelrefresh"
	"strings"
	"time"

	"orchids-api/internal/accountevents"
	"orchids-api/internal/accountpolicy"
	"orchids-api/internal/cline"
	"orchids-api/internal/config"
	"orchids-api/internal/grok"
	"orchids-api/internal/loadbalancer"
	"orchids-api/internal/qoder"
	"orchids-api/internal/refreshqueue"
	"orchids-api/internal/store"
	"orchids-api/internal/workbuddy"
)

// ProviderHealthRefreshInterval is how often a channel catalog is re-read.
const ProviderHealthRefreshInterval = 30 * time.Minute

// Hub is shared by the admin check path and the refresh scheduler.
// Hub is shared by the admin check path and the refresh scheduler.
var Hub = refreshqueue.Default()

// refreshCLIAccount refreshes a Build CLI OAuth account access token before it
// expires. The CLIClient oauth layer handles the refresh_token grant and
// persists rotated tokens back to Redis.
func refreshCLIAccount(ctx context.Context, cfg *config.Config, s *store.Store, acc *store.Account) {
	if acc == nil || s == nil || cfg == nil {
		return
	}
	// Persist the compatibility inference for pre-provider OAuth rows while we
	// already have a safe background update. This makes the product boundary
	// visible to the admin API instead of leaving old records ambiguous.
	grok.NormalizeProvider(acc)
	cliClient := grok.NewCLIClient(cfg)
	cliClient.SetAccountStore(s)
	if strings.TrimSpace(acc.OAuthAccessToken) == "" || acc.OAuthExpiresAt.IsZero() || time.Until(acc.OAuthExpiresAt) <= 5*time.Minute {
		refreshCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		token, err := cliClient.OAuthAccessToken(refreshCtx, acc)
		cancel()
		if err != nil {
			if grok.IsCLIPermanentOAuthError(err) {
				acc.StatusCode = "401"
				acc.AuthStatus = store.AccountAuthStatusReauthRequired
				acc.StatusMessage = "上游已不接受该 OAuth 授权（refresh token 被拒绝），需要重新登录"
				acc.LastAttempt = time.Now()
				// Distinguish "the upstream invalidated this grant" from "our record
				// was wiped": both surface as 401, but only the former is expected
				// when a second login for the same xAI user rotates/retires the old
				// refresh token.
				slog.Warn("Grok CLI refresh token was rejected by the upstream; this account needs a new login",
					"account_id", acc.ID,
					"account_name", acc.Name,
					"email", acc.Email,
					"team_id", acc.TeamID,
					"has_refresh_token", strings.TrimSpace(acc.OAuthRefreshToken) != "",
					"token_fingerprint", grok.TokenFingerprint(acc.OAuthRefreshToken),
					"error", err)
			} else {
				slog.Warn("Auto refresh grok cli token failed", "account_id", acc.ID, "error", err)
			}
			if updateErr := s.UpdateAccount(ctx, acc); updateErr != nil {
				slog.Warn("Auto refresh grok cli: update account failed", "account_id", acc.ID, "error", updateErr)
			}
			return
		}
		if token != "" {
			acc.OAuthAccessToken = token
			acc.StatusCode = ""
			acc.AuthStatus = store.AccountAuthStatusActive
			acc.LastAttempt = time.Time{}
		}
	}

	now := time.Now()
	result := grok.RefreshBuildAccount(ctx, cliClient, acc, grok.BuildRefreshOptions{
		Billing: grokCLIBillingNeedsSync(acc, now), BillingTimeout: 20 * time.Second,
		Models: grok.CLIModelsNeedSync(acc, now), ModelsTimeout: 15 * time.Second,
		Now: now,
	})
	if result.BillingErr != nil {
		slog.Warn("Auto sync grok cli billing failed; leaving numeric quota unavailable", "account_id", acc.ID, "error", result.BillingErr)
	}
	if result.ModelsErr != nil {
		slog.Warn("Auto sync grok cli model catalog failed; keeping last catalog", "account_id", acc.ID, "error", result.ModelsErr)
	}
	if updateErr := s.UpdateAccount(ctx, acc); updateErr != nil {
		slog.Warn("Auto refresh grok cli: update account failed", "account_id", acc.ID, "error", updateErr)
	}
}

func grokCLIBillingNeedsSync(acc *store.Account, now time.Time) bool {
	if acc == nil || acc.GrokBilling.SyncedAt.IsZero() {
		return true
	}
	return now.Sub(acc.GrokBilling.SyncedAt) >= ProviderHealthRefreshInterval
}

func qoderQuotaRefreshDue(acc *store.Account, now time.Time) bool {
	if acc == nil || acc.QoderQuota.SyncedAt.IsZero() {
		return true
	}
	return now.Sub(acc.QoderQuota.SyncedAt) >= ProviderHealthRefreshInterval
}

func qoderCatalogRefreshDue(acc *store.Account, now time.Time) bool {
	if acc == nil || len(acc.QoderModelIDs) == 0 || acc.QoderModelsSyncedAt.IsZero() {
		return true
	}
	return now.Sub(acc.QoderModelsSyncedAt) >= ProviderHealthRefreshInterval
}

func refreshQoderCatalog(ctx context.Context, cfg *config.Config, s *store.Store, acc *store.Account) {
	if acc == nil || s == nil || !qoderCatalogRefreshDue(acc, time.Now()) {
		return
	}
	client := qoder.NewFromAccount(acc, cfg)
	defer client.Close()
	client.SetAccountStore(s)
	catalogCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	catalog, err := client.FetchUpstreamModels(catalogCtx)
	cancel()
	if err != nil {
		slog.Warn("Auto refresh qoder catalog failed; keeping the last snapshot", "account_id", acc.ID, "error", err)
		return
	}
	modelrefresh.PersistAccountCatalogSnapshot(ctx, s, acc, "qoder", qoder.CatalogSnapshot(catalog), "Auto refresh qoder catalog: update account failed")
}

// refreshQoderQuota reads the same authoritative allowance endpoint used by a
// manual account refresh. Inference-agent limit payloads are intentionally not
// used here: they can be model-scoped while the account still has credits.
func refreshQoderQuota(ctx context.Context, cfg *config.Config, s *store.Store, acc *store.Account) {
	if acc == nil || s == nil || !qoderQuotaRefreshDue(acc, time.Now()) {
		return
	}
	if !refreshqueue.WithLease(acc.ID, func() {
		client := qoder.NewFromAccount(acc, cfg)
		defer client.Close()
		client.SetAccountStore(s)
		quotaCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		quota, err := client.FetchQuota(quotaCtx)
		cancel()
		if err != nil {
			slog.Warn("Auto refresh qoder quota failed; keeping the last account verdict", "account_id", acc.ID, "error", err)
			return
		}
		qoder.ApplyQuota(acc, quota)
		if quota.Exhausted {
			accountpolicy.Verdict{
				Status:  store.AccountStatusQoderQuotaExhausted,
				Message: "Qoder allowance exhausted; free catalog models remain eligible",
				Scope:   accountpolicy.ScopeAccount,
				At:      time.Now(),
			}.Apply(acc)
		} else {
			accountpolicy.Success(time.Now()).Apply(acc)
		}
		if err := s.UpdateAccount(ctx, acc); err != nil {
			slog.Warn("Auto refresh qoder quota: update account failed", "account_id", acc.ID, "error", err)
		}
	}) {
		slog.Debug("Auto refresh qoder quota: account already refreshing", "account_id", acc.ID)
	}
}

func workBuddyCatalogRefreshDue(acc *store.Account, now time.Time) bool {
	if acc == nil {
		return false
	}
	if len(acc.WorkBuddyModelIDs) == 0 || acc.WorkBuddyModelsSyncedAt.IsZero() {
		return true
	}
	return now.Sub(acc.WorkBuddyModelsSyncedAt) >= ProviderHealthRefreshInterval
}

// refreshWorkBuddyCatalog keeps the account-scoped CLI whitelist current without
// replacing a usable last-known-good snapshot when the control plane is down.
func refreshWorkBuddyCatalog(ctx context.Context, cfg *config.Config, s *store.Store, acc *store.Account) {
	if acc == nil || s == nil || !workBuddyCatalogRefreshDue(acc, time.Now()) {
		return
	}
	if !refreshqueue.WithLease(acc.ID, func() {
		client := workbuddy.NewFromAccount(acc, cfg)
		defer client.Close()
		client.SetAccountStore(s)
		catalogCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		models, err := client.FetchModels(catalogCtx)
		cancel()
		if err != nil {
			slog.Warn("Auto refresh workbuddy catalog failed; keeping the last snapshot", "account_id", acc.ID, "error", err)
			return
		}
		modelrefresh.PersistAccountCatalogSnapshot(ctx, s, acc, "workbuddy", workbuddy.CatalogSnapshot(models), "Auto refresh workbuddy catalog: update account failed")
	}) {
		slog.Debug("Auto refresh workbuddy catalog: account already refreshing", "account_id", acc.ID)
	}
}

// workBuddyQuotaRefreshDue reports whether the account's credit-meter reading
// should be re-read on this tick.
func workBuddyQuotaRefreshDue(acc *store.Account, now time.Time) bool {
	if acc == nil {
		return false
	}
	if acc.WorkBuddyQuota.SyncedAt.IsZero() {
		return true
	}
	return now.Sub(acc.WorkBuddyQuota.SyncedAt) >= ProviderHealthRefreshInterval
}

// refreshWorkBuddyQuota re-reads the credit meter the account table, the
// exhausted/free-only verdict and the selection filter are all built from.
//
// Before this existed the reading was only written at login and on a manual
// check, so it aged without bound: production held a 36-hour-old snapshot for an
// account whose live balance had already moved, and an operator comparing the
// table with the upstream was comparing two different moments.
//
// The verdict follows the same rule as the manual check: a spent metered package
// narrows the account to its confirmed free catalog instead of parking it, and a
// reading that shows credits again restores full capability.
func refreshWorkBuddyQuota(ctx context.Context, cfg *config.Config, s *store.Store, acc *store.Account) {
	if acc == nil || s == nil || !workBuddyQuotaRefreshDue(acc, time.Now()) {
		return
	}
	if !refreshqueue.WithLease(acc.ID, func() {
		client := workbuddy.NewFromAccount(acc, cfg)
		defer client.Close()
		quotaCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		quota, err := client.FetchQuota(quotaCtx)
		cancel()
		if err != nil {
			slog.Warn("Auto refresh workbuddy quota failed; keeping the last reading", "account_id", acc.ID, "error", err)
			return
		}
		workbuddy.ApplyQuota(acc, quota)
		switch {
		case acc.UsageLimit > 0 && acc.UsageCurrent <= 0:
			accountpolicy.Verdict{
				Status:  store.AccountStatusWorkBuddyQuotaExhausted,
				Message: "WorkBuddy allowance exhausted; free catalog models remain eligible",
				Scope:   accountpolicy.ScopeAccount,
				At:      time.Now(),
			}.Apply(acc)
		case acc.StatusCode == store.AccountStatusWorkBuddyQuotaExhausted:
			// The meter shows credits again, so the free-only narrowing is over.
			// Only that verdict is cleared: another status (a refused credential, a
			// throttle) is not something a credit reading can speak to.
			acc.StatusCode = ""
			acc.StatusMessage = ""
			acc.LastAttempt = time.Time{}
			acc.VerifiedAt = time.Now()
		}
		if err := s.UpdateAccount(ctx, acc); err != nil {
			slog.Warn("Auto refresh workbuddy quota: update account failed", "account_id", acc.ID, "error", err)
		}
	}) {
		slog.Debug("Auto refresh workbuddy quota: account already refreshing", "account_id", acc.ID)
	}
}

// clineCatalogRefreshDue reports whether the account's catalog snapshot should
// be re-read. The snapshot is an observation, and the free feed changes without
// notice, so it is refreshed on the same cadence as the other providers.
func clineCatalogRefreshDue(acc *store.Account, now time.Time) bool {
	if acc == nil {
		return false
	}
	if len(acc.ClineModelIDs) == 0 || acc.ClineModelsSyncedAt.IsZero() {
		return true
	}
	return now.Sub(acc.ClineModelsSyncedAt) >= ProviderHealthRefreshInterval
}

// refreshClineCatalog re-reads the account's model feed.
//
// It never publishes a compiled-in list: a failed read leaves the snapshot
// untouched, so an account that stopped being able to read the feed keeps the
// last observation instead of being silently widened.
func refreshClineCatalog(ctx context.Context, cfg *config.Config, s *store.Store, acc *store.Account) {
	if acc == nil || s == nil || !clineCatalogRefreshDue(acc, time.Now()) {
		return
	}
	if !refreshqueue.WithLease(acc.ID, func() {
		client := cline.NewFromAccount(acc, cfg)
		defer client.Close()
		client.SetAccountStore(s)
		catalogCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		models, err := client.FetchUpstreamModels(catalogCtx)
		cancel()
		if err != nil {
			slog.Warn("Auto refresh cline catalog failed; keeping the last snapshot", "account_id", acc.ID, "error", err)
			return
		}
		modelrefresh.PersistAccountCatalogSnapshot(ctx, s, acc, "cline", cline.CatalogSnapshot(models), "Auto refresh cline catalog: update account failed")
	}) {
		slog.Debug("Auto refresh cline catalog: account already refreshing", "account_id", acc.ID)
	}
}

// StartTokenRefreshLoop refreshes account credentials on their own schedule.
func StartTokenRefreshLoop(ctx context.Context, configSnapshot func() *config.Config, s *store.Store, lb *loadbalancer.LoadBalancer) {
	cfg := configSnapshot()
	if cfg == nil || !cfg.AutoRefreshToken {
		return
	}
	interval := time.Duration(cfg.TokenRefreshInterval) * time.Minute
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	slog.Debug("Auto refresh token enabled", "interval", interval.String())

	refreshAccounts := func() {
		cfg := configSnapshot()
		if cfg == nil {
			return
		}
		refreshCtx := store.WithAccountChangeOrigin(context.Background(), store.AccountChangeOriginScheduler)
		accounts, err := s.GetEnabledAccounts(refreshCtx)
		if err != nil {
			slog.Error("Auto refresh token: list accounts failed", "error", err)
			return
		}
		// Refresh enabled accounts by provider; each provider checks its own
		// catalog, quota, or credential cadence before contacting upstream.
		for _, acc := range accounts {
			if strings.EqualFold(acc.AccountType, "qoder") {
				refreshQoderCatalog(refreshCtx, cfg, s, acc)
				refreshQoderQuota(refreshCtx, cfg, s, acc)
				continue
			}
			if strings.EqualFold(acc.AccountType, "workbuddy") {
				refreshWorkBuddyCatalog(refreshCtx, cfg, s, acc)
				refreshWorkBuddyQuota(refreshCtx, cfg, s, acc)
				continue
			}
			if strings.EqualFold(acc.AccountType, "cline") {
				// Cline has no credit meter to poll, but its catalog must stay an
				// observation: a feed that changed after login otherwise leaves the
				// account resolving against a stale list forever.
				refreshClineCatalog(refreshCtx, cfg, s, acc)
				continue
			}
			// Grok accounts: the Build CLI OAuth plane refreshes through its own
			// token lifecycle.
			if strings.EqualFold(acc.AccountType, "grok") {
				// A credential the upstream already refused needs a human, not
				// another refresh. Skipping it here is the convergence step: it
				// stops consuming refresh cycles and writing the same warning
				// every interval until an operator logs in again (which restores
				// the active state).
				if !strings.EqualFold(strings.TrimSpace(acc.CredentialType), "oauth") {
					continue
				}
				if !store.AccountAuthActive(acc) || !acc.Enabled {
					continue
				}
				refreshCLIAccount(refreshCtx, cfg, s, acc)
				continue
			}
			// Other account types are not auto-refreshed here.
			continue
		}
	}

	go func() {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("Panic in token refresh loop", "error", err)
			}
		}()
		refreshAccounts()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refreshAccounts()
			case <-Kick.Channel():
				// An account was created, edited, deleted or rotated: the due set
				// changed, so waiting out the rest of the tick would be wrong. The
				// subscriber coalesces bursts, so a multi-field update wakes the
				// loop once.
				slog.Debug("Auto refresh token: account change detected; re-evaluating the due set")
				refreshAccounts()
			}
		}
	}()
}

// Kick wakes the refresh loop when an account changes, so a new account is
// picked up immediately instead of after up to one full interval.
// Kick wakes the refresh loop when an account changes, so a new account is
// picked up without waiting for the next tick.
var Kick = accountevents.NewFilteredKick(
	[]accountevents.Kind{accountevents.KindCreated, accountevents.KindCredential},
	store.AccountChangeOriginScheduler,
)
