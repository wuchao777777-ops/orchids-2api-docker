package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"orchids-api/internal/cline"
	"orchids-api/internal/config"
	apperrors "orchids-api/internal/errors"
	"orchids-api/internal/grok"
	"orchids-api/internal/modelcatalog"
	"orchids-api/internal/qoder"
	"orchids-api/internal/refreshqueue"
	"orchids-api/internal/store"
	"orchids-api/internal/util"
)

func verifyGrokAccount(ctx context.Context, acc *store.Account, cfg *config.Config, accountStore *store.Store) error {
	if acc == nil {
		return fmt.Errorf("missing grok account")
	}
	// Build CLI OAuth accounts verify against the CLI proxy with a Bearer token.
	if grokAccountIsOAuth(acc) {
		if !grokAccountHasOAuthCredentials(acc) {
			return fmt.Errorf("missing oauth token")
		}
		cliClient := grok.NewCLIClient(cfg)
		cliClient.SetAccountStore(accountStore)
		result := grok.RefreshBuildAccount(ctx, cliClient, acc, grok.BuildRefreshOptions{
			Verify: true, VerifyTimeout: 20 * time.Second,
			Billing: true,
			Models:  true, ModelsTimeout: 15 * time.Second,
		})
		if result.VerifyErr != nil {
			if result.VerifyStatus != "" {
				return fmt.Errorf("%s: %w", result.VerifyStatus, result.VerifyErr)
			}
			return result.VerifyErr
		}
		if result.BillingErr != nil {
			slog.Warn("Grok CLI billing sync failed; leaving quota unavailable", "account_id", acc.ID, "error", result.BillingErr)
		}
		if result.ModelsErr != nil {
			slog.Warn("Grok CLI model catalog sync failed; keeping last catalog", "account_id", acc.ID, "error", result.ModelsErr)
		}
		return nil
	}

	// The website cookie plane was retired: only Build OAuth
	// credentials can be verified now.
	return fmt.Errorf("only Grok Build OAuth accounts are supported")
}

func httpStatusFromAccountStatus(status string) int {
	switch strings.TrimSpace(status) {
	case "401":
		return http.StatusUnauthorized
	case "402", store.AccountStatusQoderQuotaExhausted, store.AccountStatusWorkBuddyQuotaExhausted:
		return http.StatusPaymentRequired
	case "403":
		return http.StatusForbidden
	case "404":
		return http.StatusNotFound
	case "429":
		return http.StatusTooManyRequests
	default:
		return http.StatusBadGateway
	}
}

func normalizeGrokTokenInput(acc *store.Account) {
	if acc == nil || !strings.EqualFold(acc.AccountType, "grok") {
		return
	}
	acc.CredentialType = "oauth"
	acc.GrokProvider = grok.ProviderBuild
	acc.OAuthAccessToken = strings.TrimSpace(acc.OAuthAccessToken)
	acc.OAuthRefreshToken = strings.TrimSpace(acc.OAuthRefreshToken)
	// Non-Build Grok credentials must never survive an account write.
	acc.Token = ""
	acc.ClientCookie = ""
	acc.RefreshToken = ""
}

// grokAccountIsOAuth reports whether a Grok account is a Build CLI OAuth account.
func grokAccountIsOAuth(acc *store.Account) bool {
	return acc != nil && strings.EqualFold(strings.TrimSpace(acc.CredentialType), "oauth")
}

// grokAccountHasOAuthCredentials reports whether an OAuth account carries at
// least one usable token after normalization.
func grokAccountHasOAuthCredentials(acc *store.Account) bool {
	if !grokAccountIsOAuth(acc) {
		return false
	}
	return strings.TrimSpace(acc.OAuthAccessToken) != "" || strings.TrimSpace(acc.OAuthRefreshToken) != ""
}

// preserveGrokOAuthCredentials keeps existing OAuth secrets when the admin UI
// submits empty fields (secrets are redacted on read and therefore absent on
// ordinary edit/save).
func preserveGrokOAuthCredentials(acc, existing *store.Account) {
	if acc == nil || existing == nil || !grokAccountIsOAuth(acc) {
		return
	}
	if strings.TrimSpace(acc.OAuthAccessToken) == "" {
		acc.OAuthAccessToken = existing.OAuthAccessToken
	}
	if strings.TrimSpace(acc.OAuthRefreshToken) == "" {
		acc.OAuthRefreshToken = existing.OAuthRefreshToken
	}
	if acc.OAuthExpiresAt.IsZero() && !existing.OAuthExpiresAt.IsZero() {
		acc.OAuthExpiresAt = existing.OAuthExpiresAt
	}
	if strings.TrimSpace(acc.TeamID) == "" {
		acc.TeamID = existing.TeamID
	}
}

// preserveGrokRuntimeStateOnAdminEdit keeps provider-observed Build state out of
// the generic account edit surface. OAuth secrets are preserved separately.
func preserveGrokRuntimeStateOnAdminEdit(acc, existing *store.Account) {
	if acc == nil || existing == nil || !strings.EqualFold(acc.AccountType, "grok") {
		return
	}
	acc.Token = existing.Token
	acc.Subscription = existing.Subscription
	acc.UsageCurrent = existing.UsageCurrent
	acc.UsageTotal = existing.UsageTotal
	acc.UsageLimit = existing.UsageLimit
	acc.StatusCode = existing.StatusCode
	acc.StatusMessage = existing.StatusMessage
	acc.LastAttempt = existing.LastAttempt
	acc.VerifiedAt = existing.VerifiedAt
	acc.QuotaResetAt = existing.QuotaResetAt
	acc.GrokModels = append([]string(nil), existing.GrokModels...)
	acc.GrokModelCatalog = modelcatalog.CloneProfiles(existing.GrokModelCatalog)
	acc.GrokModelsSyncedAt = existing.GrokModelsSyncedAt
	acc.GrokBilling = existing.GrokBilling
	acc.GrokRateLimits = existing.GrokRateLimits
}

func buildQuotaResponseFields(acc *store.Account) map[string]interface{} {
	return buildQuotaResponseFieldsWithUsage(acc, 0, false)
}

// applyQuotaProvenance records where a quota number came from and how far it should
// be trusted, next to the number itself.
//
// Without it a table can only say "unknown", which conflates three different facts — a
// paid account whose numeric window upstream does not publish, a Free account whose
// window has to be estimated, and an account that was never synced. quota_type is
// paid/free/unknown, quota_source names the signal, quota_confidence is
// confirmed/observed/estimated, and quota_limit_known is false whenever the limit is
// an estimate that the upstream has not confirmed.
func applyQuotaProvenance(fields map[string]interface{}, quotaType, source, confidence, note string, limitKnown, observed bool) {
	fields["quota_type"] = quotaType
	fields["quota_source"] = source
	fields["quota_confidence"] = confidence
	fields["quota_limit_known"] = limitKnown
	fields["quota_observed"] = observed
	if note != "" {
		fields["quota_note"] = note
	}
}

// buildQuotaResponseFieldsWithUsage projects an account's allowance. observedTokens
// is the usage this gateway measured inside grok.FreeBuildUsageWindow and
// usageObserved says whether that measurement actually ran; both are used only by the
// Free estimate, which must never present unmeasured usage as if it were measured.
func buildQuotaResponseFieldsWithUsage(acc *store.Account, observedTokens int64, usageObserved bool) map[string]interface{} {
	fields := map[string]interface{}{
		"quota_limit":     0.0,
		"quota_used":      0.0,
		"quota_remaining": 0.0,
		"quota_mode":      "remaining",
		"quota_unit":      "credits",
		"quota_supported": true,
	}
	applyQuotaProvenance(fields, "unknown", "unknown", "", "", false, false)
	if acc == nil {
		return fields
	}

	limit := acc.UsageLimit
	current := acc.UsageCurrent
	if limit < 0 {
		limit = 0
	}
	if current < 0 {
		current = 0
	}

	projectQuotaFields(fields, acc, limit, current, observedTokens, usageObserved)
	return fields
}

func (a *API) HandleAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		accounts, err := a.store.ListAccounts(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if accounts == nil {
			accounts = []*store.Account{}
		}
		// The Free estimate needs the usage this gateway observed; the scan is one
		// bounded read shared by the whole page, and it is skipped entirely when there
		// is nothing to describe.
		var observed map[int64]int64
		if len(accounts) > 0 {
			if measured, ok := a.observedTokensByAccount(r.Context(), time.Now().Add(-grok.FreeBuildUsageWindow)); ok {
				observed = measured
			}
		}
		normalized := make([]*accountOutput, 0, len(accounts))
		for _, acc := range accounts {
			if acc == nil {
				continue
			}
			normalized = append(normalized, normalizeAccountOutputWithUsage(acc, observed))
		}
		util.WriteJSON(w, normalized)

	case http.MethodPost:
		var acc store.Account
		if err := json.NewDecoder(r.Body).Decode(&acc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		acc.AccountType = strings.ToLower(strings.TrimSpace(acc.AccountType))
		if !validateAccountType(w, acc.AccountType) {
			return
		}
		if strings.EqualFold(acc.AccountType, "grok") {
			normalizeGrokTokenInput(&acc)
			if !grokAccountIsOAuth(&acc) {
				http.Error(w, "Grok accounts must be added through the Build OAuth device login (/api/grok/device-auth)", http.StatusBadRequest)
				return
			}
			if grokAccountIsOAuth(&acc) && !grokAccountHasOAuthCredentials(&acc) {
				http.Error(w, "missing oauth token", http.StatusBadRequest)
				return
			}
		} else if strings.EqualFold(acc.AccountType, "workbuddy") {
			if !NormalizeWorkBuddyCredentials(&acc) {
				http.Error(w, "missing WorkBuddy credential: paste the access token or refresh token from the WorkBuddy desktop session", http.StatusBadRequest)
				return
			}
		} else if strings.EqualFold(acc.AccountType, "qoder") {
			// Qoder is OAuth-only: an account is created by the browser device
			// flow (/api/qoder/login), never by pasting a personal access token.
			http.Error(w, "Qoder accounts must be added using the official browser login (/api/qoder/login)", http.StatusBadRequest)
			return
		} else if strings.EqualFold(acc.AccountType, "cline") {
			// Cline is OAuth-only for the same reason: the WorkOS device grant is
			// the only way to obtain the credential.
			http.Error(w, "Cline accounts must be added using the official browser login (/api/cline/login)", http.StatusBadRequest)
			return
		}
		if existing, err := a.findDuplicateAccountByCredential(r.Context(), &acc, 0); err != nil {
			slog.Error("Failed to detect duplicate account token", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if existing != nil {
			http.Error(w, duplicateAccountError(existing).Error(), http.StatusConflict)
			return
		}

		if err := a.store.CreateAccount(r.Context(), &acc); err != nil {
			slog.Error("Failed to create account", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if acc.Enabled {
			if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Account-Sync")), "async") {
				a.syncAccountAfterCreate(acc)
			} else {
				syncCtx, syncCancel := context.WithTimeout(r.Context(), 25*time.Second)
				accountStatus, _, syncErr := a.refreshAccountState(syncCtx, &acc)
				syncCancel()
				if syncErr != nil {
					slog.Warn("Initial account sync failed", "account_id", acc.ID, "type", acc.AccountType, "error", syncErr)
					if accountStatus != "" {
						acc.StatusCode = accountStatus
						acc.StatusMessage = strings.TrimSpace(syncErr.Error())
						acc.LastAttempt = time.Now()
						acc.VerifiedAt = acc.LastAttempt
					}
				} else {
					applySuccessfulAccountRefreshStatus(&acc, accountStatus)
				}
				// A credential the upstream definitively rejects must not be
				// persisted as a healthy account: it would sit in the pool looking
				// healthy while every request routed to it fails.
				if acc.StatusCode == "401" {
					if deleteErr := a.store.DeleteAccount(r.Context(), acc.ID); deleteErr != nil {
						slog.Error("Failed to roll back rejected account", "account_id", acc.ID, "type", acc.AccountType, "error", deleteErr)
					} else {
						slog.Warn("Rejected account was not saved (upstream refused the credential)",
							"account_id", acc.ID, "type", acc.AccountType, "reason", acc.StatusMessage)
					}
					apperrors.New("authentication_error",
						"account was rejected by the upstream and was not saved: "+strings.TrimSpace(acc.StatusMessage),
						http.StatusUnauthorized).WriteResponse(w)
					return
				}
				if updateErr := a.store.UpdateAccount(r.Context(), &acc); updateErr != nil {
					slog.Warn("Failed to persist initial account sync", "account_id", acc.ID, "type", acc.AccountType, "error", updateErr)
				}
			}
		}

		util.WriteJSONStatus(w, http.StatusCreated, normalizeAccountOutput(&acc))

	default:
		writeMethodNotAllowed(w)
	}
}

func (a *API) HandleAccountByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
	parts := strings.Split(path, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}
	account, err := a.store.GetAccount(r.Context(), id)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	isCheck := len(parts) > 1 && parts[1] == "check"
	isUsage := len(parts) > 1 && parts[1] == "usage"
	if len(parts) > 2 || (len(parts) > 1 && !(isCheck || isUsage)) {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		if isUsage {
			resp := map[string]interface{}{
				"account_id":     account.ID,
				"name":           account.Name,
				"account_type":   account.AccountType,
				"subscription":   account.Subscription,
				"usage_current":  account.UsageCurrent,
				"usage_limit":    account.UsageLimit,
				"usage_total":    account.UsageTotal,
				"quota_reset_at": account.QuotaResetAt,
			}
			for k, v := range buildQuotaResponseFields(account) {
				resp[k] = v
			}
			util.WriteJSON(w, resp)
			return
		}
		if isCheck {
			// Storm control / backoff: only allow a small number of concurrent checks,
			// and apply exponential backoff per account on failures.
			now := time.Now()
			a.checkMu.Lock()
			if a.checkInFlight[id] {
				a.checkMu.Unlock()
				http.Error(w, "account check already in progress", http.StatusTooManyRequests)
				return
			}
			if next, ok := a.checkNextAllowed[id]; ok && !next.IsZero() && now.Before(next) {
				retryAfter := int(next.Sub(now).Seconds())
				if retryAfter < 1 {
					retryAfter = 1
				}
				a.checkMu.Unlock()
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				http.Error(w, "account check backoff", http.StatusTooManyRequests)
				return
			}
			a.checkInFlight[id] = true
			a.checkMu.Unlock()
			defer func() {
				a.checkMu.Lock()
				delete(a.checkInFlight, id)
				a.checkMu.Unlock()
			}()

			// global concurrency limit
			a.checkSem <- struct{}{}
			defer func() { <-a.checkSem }()

			acc := account
			checkOK := false
			checkErrStatus := ""
			defer func() {
				a.checkMu.Lock()
				defer a.checkMu.Unlock()
				if checkOK {
					a.checkFailCount[id] = 0
					a.checkNextAllowed[id] = time.Now().Add(3 * time.Second)
					return
				}
				fails := a.checkFailCount[id] + 1
				a.checkFailCount[id] = fails
				d := time.Duration(1<<min(fails, 8)) * time.Second
				// For CF/rate-limit style failures, start with a bigger cooldown.
				if checkErrStatus == "403" || checkErrStatus == "429" {
					if d < 60*time.Second {
						d = 60 * time.Second
					}
				}
				if d > 10*time.Minute {
					d = 10 * time.Minute
				}
				a.checkNextAllowed[id] = time.Now().Add(d)
			}()

			// The manual check takes the same process-wide lease as the background
			// scheduler: two refreshes of one account must never run at once, or the
			// slower writer would persist an older snapshot over a newer verdict.
			var accountStatus string
			var httpStatus int
			var refreshErr error
			if !refreshqueue.WithLease(acc.ID, func() {
				accountStatus, httpStatus, refreshErr = a.refreshAccountState(r.Context(), acc)
			}) {
				slog.Info("Account check skipped: a refresh of this account is already running", "account_id", acc.ID)
				checkErrStatus = ""
				a.checkMu.Lock()
				a.checkInFlight[id] = false
				a.checkMu.Unlock()
				writeAccountCheckBusy(w)
				return
			}
			if refreshErr != nil {
				checkErrStatus = accountStatus
				if accountStatus != "" {
					acc.StatusCode = accountStatus
					// The reason matters: a bare "401" cannot tell an operator
					// whether the credential was retired upstream or the record
					// lost it.
					acc.StatusMessage = strings.TrimSpace(refreshErr.Error())
					acc.LastAttempt = time.Now()
					acc.VerifiedAt = acc.LastAttempt
					if updateErr := a.store.UpdateAccount(r.Context(), acc); updateErr != nil {
						slog.Warn("Failed to persist account refresh status", "account_id", acc.ID, "error", updateErr)
					}
				}
				if httpStatus == 0 {
					httpStatus = http.StatusBadRequest
				}
				http.Error(w, refreshErr.Error(), httpStatus)
				return
			}

			// Clear the account status after a successful refresh/verification
			applySuccessfulAccountRefreshStatus(acc, accountStatus)
			checkOK = true

			if err := a.store.UpdateAccount(r.Context(), acc); err != nil {
				http.Error(w, "Failed to save checked account: "+err.Error(), http.StatusInternalServerError)
				return
			}
			util.WriteJSON(w, a.normalizeAccountOutputObserved(r.Context(), acc))
			return
		}
		util.WriteJSON(w, a.normalizeAccountOutputObserved(r.Context(), account))

	case http.MethodPut:
		existing := account

		var acc store.Account
		if err := json.NewDecoder(r.Body).Decode(&acc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		acc.ID = id
		if strings.TrimSpace(acc.AccountType) == "" {
			acc.AccountType = existing.AccountType
		}
		acc.AccountType = strings.ToLower(strings.TrimSpace(acc.AccountType))
		if !validateAccountType(w, acc.AccountType) {
			return
		}
		if strings.EqualFold(acc.AccountType, "grok") {
			normalizeGrokTokenInput(&acc)
			// Admin UI redacts OAuth secrets on read; empty inbound fields mean
			// "keep existing", not "clear credentials".
			preserveGrokOAuthCredentials(&acc, existing)
			preserveGrokRuntimeStateOnAdminEdit(&acc, existing)
			if grokAccountIsOAuth(&acc) && !grokAccountHasOAuthCredentials(&acc) {
				http.Error(w, "missing oauth token", http.StatusBadRequest)
				return
			}
		} else if strings.EqualFold(acc.AccountType, "workbuddy") {
			// The read path redacts the refresh token, so an ordinary edit
			// arrives without it; keep the stored credential unless a new one
			// was actually submitted.
			submitted := resolveWorkBuddyCredentials(&acc)
			PreserveWorkBuddyCredentialsOnEdit(&acc, existing)
			if resolveWorkBuddyCredentials(&acc).RefreshToken == "" && resolveWorkBuddyCredentials(&acc).AccessToken == "" {
				http.Error(w, "missing WorkBuddy credential", http.StatusBadRequest)
				return
			}
			NormalizeWorkBuddyCredentials(&acc)
			existingCreds := resolveWorkBuddyCredentials(existing)
			acc.ReplaceWorkBuddyCredentials = submitted.HasCredential() &&
				(submitted.AccessToken != existingCreds.AccessToken || submitted.RefreshToken != existingCreds.RefreshToken)
			if acc.ReplaceWorkBuddyCredentials {
				acc.ClearVerifiedAt = true
			}
		} else if strings.EqualFold(acc.AccountType, "qoder") {
			submitted := qoder.ResolveCredentials(&acc)
			submittedMachineID := strings.TrimSpace(acc.QoderMachineID)
			PreserveQoderCredentialsOnEdit(&acc, existing)
			if !NormalizeQoderCredentials(&acc) {
				http.Error(w, "missing Qoder credential: sign in again with the browser login", http.StatusBadRequest)
				return
			}
			if strings.TrimSpace(acc.QoderMachineID) == "" {
				http.Error(w, "missing Qoder device identity: sign in again", http.StatusBadRequest)
				return
			}
			existingCreds := qoder.ResolveCredentials(existing)
			acc.ReplaceQoderCredentials = submitted.HasCredential() &&
				(submitted.AccessToken != existingCreds.AccessToken ||
					submitted.RefreshToken != existingCreds.RefreshToken ||
					(submittedMachineID != "" && submittedMachineID != strings.TrimSpace(existing.QoderMachineID)))
			if acc.ReplaceQoderCredentials {
				acc.ClearVerifiedAt = true
			}
		} else if strings.EqualFold(acc.AccountType, "cline") {
			// The read path redacts the refresh token, so an ordinary edit
			// arrives without it; keep the stored credential unless a new one
			// was actually submitted.
			submitted := cline.ResolveCredentials(&acc)
			PreserveClineCredentialsOnEdit(&acc, existing)
			if !NormalizeClineCredentials(&acc) {
				http.Error(w, "missing Cline credential: sign in again with the browser login", http.StatusBadRequest)
				return
			}
			existingCreds := cline.ResolveCredentials(existing)
			acc.ReplaceClineCredentials = submitted.HasCredential() &&
				(submitted.AccessToken != existingCreds.AccessToken ||
					submitted.RefreshToken != existingCreds.RefreshToken)
			if acc.ReplaceClineCredentials {
				acc.ClearVerifiedAt = true
			}
		}

		if acc.UserID == "" {
			acc.UserID = existing.UserID
		}
		if acc.Email == "" {
			acc.Email = existing.Email
		}
		if duplicate, err := a.findDuplicateAccountByCredential(r.Context(), &acc, id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if duplicate != nil {
			http.Error(w, duplicateAccountError(duplicate).Error(), http.StatusConflict)
			return
		}

		if err := a.store.UpdateAccount(r.Context(), &acc); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		util.WriteJSON(w, normalizeAccountOutput(&acc))

	case http.MethodDelete:
		if err := a.store.DeleteAccount(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		writeMethodNotAllowed(w)
	}
}
