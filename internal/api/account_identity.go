package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"orchids-api/internal/channel"
	"orchids-api/internal/store"
	"orchids-api/internal/util"
)

// Recognising an account this gateway already has, so one upstream login is one
// row. Deduplication runs on a stable provider identity as well as the
// credential, because a fresh login rotates the durable token.

func normalizedAccountCredentialKey(acc *store.Account) string {
	if acc == nil {
		return ""
	}

	accountType := strings.ToLower(strings.TrimSpace(acc.AccountType))
	var token string

	switch accountType {
	case "grok":
		token = strings.TrimSpace(util.FirstNonEmpty(acc.OAuthRefreshToken, acc.OAuthAccessToken))
	case "workbuddy":
		return WorkBuddyCredentialKey(acc)
	case "qoder":
		return QoderCredentialKey(acc)
	case "cline":
		return ClineCredentialKey(acc)
	default:
		token = strings.TrimSpace(util.FirstNonEmpty(acc.RefreshToken, acc.ClientCookie, acc.Token))
	}

	if token == "" || accountType == "" {
		return ""
	}
	return accountType + ":" + token
}

func isSupportedAccountType(accountType string) bool { return channel.IsSupported(accountType) }

// validateAccountType rejects an account whose type is missing or unknown.
//
// The create and update surfaces both take an account type from the request
// body, and both have to answer the same two questions before touching the
// store: is a type present, and is it one this gateway serves. Reporting the
// error and writing the response belongs here so the two surfaces cannot drift
// into giving different answers about the same input.
func validateAccountType(w http.ResponseWriter, accountType string) bool {
	if strings.TrimSpace(accountType) == "" {
		http.Error(w, "account_type is required", http.StatusBadRequest)
		return false
	}
	if !isSupportedAccountType(accountType) {
		http.Error(w, "unsupported account type", http.StatusBadRequest)
		return false
	}
	return true
}

func (a *API) findDuplicateAccountByCredential(ctx context.Context, acc *store.Account, excludeID int64) (*store.Account, error) {
	if a == nil || a.store == nil || acc == nil {
		return nil, nil
	}

	key := normalizedAccountCredentialKey(acc)
	identityKey := stableProviderIdentityKey(acc)
	if key == "" && identityKey == "" {
		return nil, nil
	}

	accounts, err := a.store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, existing := range accounts {
		if existing == nil || existing.ID == excludeID {
			continue
		}
		if identityKey != "" && stableProviderIdentityKey(existing) == identityKey {
			return existing, nil
		}
		if key != "" && normalizedAccountCredentialKey(existing) == key {
			return existing, nil
		}
	}
	return nil, nil
}

// saveNewAccountUnlessDuplicate stores a freshly authenticated account, or
// returns the row that already carries its credential.
//
// A completed device login and a completed browser login reach the same
// decision — an upstream may hand out a second grant for an account this
// gateway already has, and inserting it would give the scheduler two rows for
// one allowance. The duplicate check and the insert share a single deadline
// because they are one step: leaving it to the caller's context would let a
// login hold a store round-trip open for the whole poll lifetime.
func (a *API) saveNewAccountUnlessDuplicate(ctx context.Context, acc *store.Account) (*store.Account, error) {
	storeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	existing, err := a.findDuplicateAccountByCredential(storeCtx, acc, 0)
	if err == nil && existing == nil {
		err = a.store.CreateAccount(storeCtx, acc)
	}
	return existing, err
}

// stableProviderIdentityKey survives OAuth token rotation. WorkBuddy and Qoder
// issue a new durable token during a fresh login, so token-only deduplication
// would create a second row for the same upstream user and leave the old row
// holding a consumed refresh token.
func stableProviderIdentityKey(acc *store.Account) string {
	if acc == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(acc.AccountType)) {
	case "grok":
		if grokAccountIsOAuth(acc) {
			if userID := strings.TrimSpace(acc.UserID); userID != "" {
				return "grok:oauth:user:" + userID
			}
			if email := strings.ToLower(strings.TrimSpace(acc.Email)); email != "" {
				return "grok:oauth:email:" + email
			}
		}
	case "workbuddy":
		if uid := strings.TrimSpace(acc.WorkBuddyUID); uid != "" {
			return "workbuddy:uid:" + uid
		}
	case "qoder":
		if uid := strings.TrimSpace(acc.QoderUserID); uid != "" {
			return "qoder:uid:" + uid
		}
		if machineID := strings.TrimSpace(acc.QoderMachineID); machineID != "" {
			return "qoder:machine:" + machineID
		}
	case "cline":
		if email := strings.ToLower(strings.TrimSpace(acc.ClineEmail)); email != "" {
			return "cline:email:" + email
		}
	}
	return ""
}

func duplicateAccountError(existing *store.Account) error {
	if existing == nil {
		return fmt.Errorf("duplicate account token")
	}
	accountType := strings.TrimSpace(existing.AccountType)
	accountType = util.FirstNonEmptyUntrimmed(accountType, "account")
	return fmt.Errorf("duplicate %s token already exists on account #%d", accountType, existing.ID)
}
