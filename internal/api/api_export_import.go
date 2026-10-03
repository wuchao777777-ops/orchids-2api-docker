package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"orchids-api/internal/store"
	"orchids-api/internal/util"
)

func (a *API) HandleExport(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	accounts, err := a.store.ListAccounts(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	exportData := ExportData{
		Version:  1,
		ExportAt: time.Now(),
		Accounts: make([]store.Account, 0, len(accounts)),
	}
	for _, acc := range accounts {
		normalized := *normalizeAccountOutput(acc).Account
		// Restore the durable credential the read path hides, then drop anything
		// that belongs to another channel. An export that drops a channel's
		// durable credential is unusable on re-import: both WorkBuddy and Qoder
		// rotate a refresh token that is the only way to renew, so an account
		// restored from such a file works until its access token expires and then
		// cannot recover.
		restoreExportCredentials(&normalized, acc)
		redactForeignCredentials(&normalized)
		normalized.ID = 0
		normalized.RequestCount = 0
		exportData.Accounts = append(exportData.Accounts, normalized)
	}

	w.Header().Set("Content-Disposition", "attachment; filename=accounts_export.json")
	util.WriteJSON(w, exportData)
}

// restoreExportCredentials puts back the credential a channel needs to be usable
// after import.
//
// normalizeAccountOutput hides these for list and query responses, so the export
// has to restore them explicitly. Everything restored here is the channel's own
// credential; a value that belongs to a different channel is cleared right after
// by redactForeignCredentials, so the two steps compose to "this row exports
// exactly the credential it can legitimately hold".
func restoreExportCredentials(out, acc *store.Account) {
	if out == nil || acc == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(acc.AccountType)) {
	case "grok":
		if grokAccountIsOAuth(acc) {
			out.OAuthAccessToken = acc.OAuthAccessToken
			out.OAuthRefreshToken = acc.OAuthRefreshToken
			out.OAuthExpiresAt = acc.OAuthExpiresAt
		}
	case "workbuddy":
		// The access token is short-lived; the refresh token is the durable
		// credential Keycloak rotates.
		out.WorkBuddyAccessToken = acc.WorkBuddyAccessToken
		out.WorkBuddyRefreshToken = acc.WorkBuddyRefreshToken
		out.WorkBuddyExpiresAt = acc.WorkBuddyExpiresAt
	case "qoder":
		// The refresh token is the durable credential, and the runtime pair is
		// derived from it at use time but is what the gateway requires on every
		// request, so both travel with the account.
		out.QoderAccessToken = acc.QoderAccessToken
		out.QoderRefreshToken = acc.QoderRefreshToken
		out.QoderExpiresAt = acc.QoderExpiresAt
		out.QoderRuntimeInfo = acc.QoderRuntimeInfo
		out.QoderRuntimeKey = acc.QoderRuntimeKey
	case "cline":
		// The refresh token is the durable credential; the access token is what
		// the chat endpoint spends.
		out.ClineAccessToken = acc.ClineAccessToken
		out.ClineRefreshToken = acc.ClineRefreshToken
		out.ClineExpiresAt = acc.ClineExpiresAt
	}
}

// redactForeignCredentials clears the credential fields of channels other than
// the account's own.
//
// The account read path gets this for free: accountOutput.MarshalJSON deletes the
// credential keys from the response object, whichever channel they belong to. The
// export marshals the stored record instead of going through that marshaler, so
// it needs the same guarantee expressed as data. It matters because a legacy row
// can hold a value in a slot its own channel never writes — the reason
// RedactQoderOutput clears the generic slots at all — and without this the export
// would publish it.
//
// The generic Token/RefreshToken/ClientCookie
// slots are deliberately left alone. They are not "foreign" for WorkBuddy and
// Qoder: both resolvers fall back to them to parse a credential document written
// before the channel had fields of its own, so clearing them here would drop a
// legacy credential from the export instead of protecting it.
func redactForeignCredentials(acc *store.Account) {
	if acc == nil {
		return
	}
	channel := strings.ToLower(strings.TrimSpace(acc.AccountType))
	// Grok's OAuth pair is restored above for an OAuth account specifically, so
	// an SSO row is treated like any other row that has no claim to it.
	if !(channel == "grok" && grokAccountIsOAuth(acc)) {
		acc.OAuthAccessToken = ""
		acc.OAuthRefreshToken = ""
		acc.OAuthExpiresAt = time.Time{}
	}
	if channel != "workbuddy" {
		acc.WorkBuddyAccessToken = ""
		acc.WorkBuddyRefreshToken = ""
		acc.WorkBuddyExpiresAt = time.Time{}
	}
	if channel != "qoder" {
		acc.QoderAccessToken = ""
		acc.QoderRefreshToken = ""
		acc.QoderExpiresAt = time.Time{}
		acc.QoderRuntimeInfo = ""
		acc.QoderRuntimeKey = ""
	}
	if channel != "cline" {
		acc.ClineAccessToken = ""
		acc.ClineRefreshToken = ""
		acc.ClineExpiresAt = time.Time{}
	}
}

func (a *API) HandleImport(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var exportData ExportData
	if err := json.NewDecoder(r.Body).Decode(&exportData); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	result := ImportResult{Total: len(exportData.Accounts)}

	for _, acc := range exportData.Accounts {
		acc.ID = 0
		acc.RequestCount = 0
		acc.AccountType = strings.ToLower(strings.TrimSpace(acc.AccountType))
		if strings.TrimSpace(acc.AccountType) == "" {
			result.Skipped++
			continue
		}
		if !isSupportedAccountType(acc.AccountType) {
			result.Skipped++
			continue
		}
		if strings.EqualFold(acc.AccountType, "grok") {
			normalizeGrokTokenInput(&acc)
			if !grokAccountIsOAuth(&acc) || !grokAccountHasOAuthCredentials(&acc) {
				slog.Warn("Skipped grok import without Build OAuth credentials", "name", acc.Name)
				result.Skipped++
				continue
			}
		}
		if err := a.store.CreateAccount(r.Context(), &acc); err != nil {
			slog.Warn("Failed to import account", "name", acc.Name, "error", err)
			result.Skipped++
		} else {
			result.Imported++
		}
	}

	util.WriteJSON(w, result)
}
