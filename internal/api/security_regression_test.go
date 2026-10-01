package api

import (
	"encoding/json"
	"net/http/httptest"
	"orchids-api/internal/config"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
	"strings"
	"testing"
)

// TestAccountRedactionDoesNotMutateStoredAccount keeps the one property the
// account leak guard does not cover: normalizeAccountOutput projects credentials
// away for the wire, but the stored row must keep them.
//
// That every credential field is absent from the rendered account — for all four
// channels, including values echoed into StatusMessage — is asserted, more
// strongly, by TestAccountResponsesNeverCarryCredentials and
// TestAccountResponsesHideCredentialKeys in account_leak_guard_test.go.
func TestAccountRedactionDoesNotMutateStoredAccount(t *testing.T) {
	acc := &store.Account{
		ID: 9, AccountType: "qoder", Token: "private-token",
		RefreshToken: "private-refresh", OAuthAccessToken: "private-oauth",
		QoderAccessToken: "private-qoder", ClineAccessToken: "private-cline",
		StatusMessage: "upstream rejected private-refresh",
	}
	if _, err := json.Marshal(normalizeAccountOutput(acc)); err != nil {
		t.Fatal(err)
	}
	if acc.Token != "private-token" || acc.RefreshToken != "private-refresh" ||
		acc.OAuthAccessToken != "private-oauth" || acc.QoderAccessToken != "private-qoder" ||
		acc.ClineAccessToken != "private-cline" ||
		acc.StatusMessage != "upstream rejected private-refresh" {
		t.Fatal("redaction mutated the stored account")
	}
}

func TestDiagnosticTogglePersistsAndFailsSafely(t *testing.T) {
	s, mini := newTestStore(t, "diagnostic-toggle:")
	cfg := &config.Config{AdminPass: "test-admin-secret"}
	a := New(s, "admin", cfg.AdminPass, cfg)
	for _, enabled := range []string{"true", "false"} {
		rec := httptest.NewRecorder()
		a.HandleDiagnosticSettings(rec, httptest.NewRequest("PUT", "/api/journal/diagnostics/settings", strings.NewReader(`{"enabled":`+enabled+`}`)))
		if rec.Code != 200 || a.DiagnosticsEnabled() != (enabled == "true") {
			t.Fatalf("toggle failed: %d", rec.Code)
		}
		stored, err := s.GetSetting(t.Context(), "config")
		if err != nil || !strings.Contains(stored, `"debug_enabled":`+enabled) {
			t.Fatal("toggle was not persisted")
		}
	}
	mini.SetError("storage unavailable")
	rec := httptest.NewRecorder()
	a.HandleDiagnosticSettings(rec, httptest.NewRequest("PUT", "/api/journal/diagnostics/settings", strings.NewReader(`{"enabled":true}`)))
	if rec.Code != 503 || a.DiagnosticsEnabled() {
		t.Fatal("failed save changed runtime setting")
	}
	rec = httptest.NewRecorder()
	a.HandleDiagnosticSettings(rec, httptest.NewRequest("GET", "/api/journal/diagnostics/settings", nil))
	testutil.MustNotContainAny(t, rec.Body.String(), "secret", "admin")
}
