package audit

import (
	"orchids-api/internal/testutil"
	"strings"
	"testing"
)

// TestSummarizeChange_MasksCredentialsKeepsShape pins the operation journal's
// contract: a reader learns WHICH fields changed without ever seeing a live
// credential.
func TestSummarizeChange_MasksCredentialsKeepsShape(t *testing.T) {
	body := []byte(`{"name":"legacy-account","client_cookie":"session=super-secret","enabled":true,"weight":3,"oauth_refresh_token":"rt-123"}`)
	summary, redacted := SummarizeChange(body)

	for _, secret := range []string{"super-secret", "rt-123"} {
		testutil.MustNotContain(t, summary, secret)
	}
	for _, key := range []string{"client_cookie", "oauth_refresh_token"} {
		testutil.MustContain(t, summary, key)
	}
	testutil.MustContainAll(t, summary, "legacy-account", "weight")
	testutil.Equal(t, len(redacted), 2)
}

// TestSummarizeChange_NestedAndArraySecrets covers the real account payload
// shape, where credentials sit inside nested objects.
func TestSummarizeChange_NestedAndArraySecrets(t *testing.T) {
	body := []byte(`{"account":{"token":"tok-1","nested":{"api_key":"k-1"}},"keys":[{"refresh_token":"r-1"}]}`)
	summary, redacted := SummarizeChange(body)
	for _, secret := range []string{"tok-1", "k-1", "r-1"} {
		testutil.MustNotContain(t, summary, secret)
	}
	testutil.Equal(t, len(redacted), 3)
	// The masked marker is written through encoding/json, which escapes "<" as
	// \u003c; only the field names are asserted here.
	testutil.MustContainAll(t, summary, "account", "api_key")
	testutil.Falsef(t, strings.Contains(summary, `"api_key":"`) && !strings.Contains(summary, "u003credacted"), "api_key was not masked: %s", summary)
}

// TestSummarizeChange_NonJSONBodyNeverLeaksContent keeps a form or text body
// from being copied into the journal.
func TestSummarizeChange_NonJSONBodyNeverLeaksContent(t *testing.T) {
	summary, redacted := SummarizeChange([]byte("admin_pass=secret&x=1"))
	testutil.MustNotContain(t, summary, "secret")
	testutil.Falsef(t, summary == "" || len(redacted) != 0, "summary = %q redacted = %v", summary, redacted)
	testutil.MustContain(t, summary, "form")
}

// TestSummarizeChange_EmptyBodyIsEmpty keeps noise out of the journal.
func TestSummarizeChange_EmptyBodyIsEmpty(t *testing.T) {
	summary, redacted := SummarizeChange([]byte("   "))
	testutil.Equal(t, summary, "")
	testutil.True(t, redacted == nil, "redacted must be nil")
}

// TestEventKindsAreDeclared guards the three journals the log centre filters on.
func TestEventKindsAreDeclared(t *testing.T) {
	for _, kind := range []Kind{KindRequest, KindOperation, KindSystem} {
		testutil.NotEqual(t, strings.TrimSpace(string(kind)), "")
	}
}

// TestSummarizeChange_MasksConfigSecrets is the reported leak: a config save with
// redis_password, proxy_pass and a password embedded in a URL reached the journal
// in clear text, because the redaction matched exact key names only.
func TestSummarizeChange_MasksConfigSecrets(t *testing.T) {
	body := []byte(`{
		"redis_password": "redis-secret-1",
		"proxy_pass": "proxy-secret-2",
		"proxy_url": "http://proxyuser:proxy-secret-3@proxy.internal:8080",
		"public_api_key": "public-secret-4",
		"admin_pass": "admin-secret-5",
		"upstream_token": "upstream-secret-6",
		"port": "3002",
		"debug_enabled": true
	}`)
	summary, redacted := SummarizeChange(body)

	for _, secret := range []string{
		"redis-secret-1", "proxy-secret-2", "proxy-secret-3",
		"public-secret-4", "admin-secret-5", "upstream-secret-6",
	} {
		testutil.MustNotContain(t, summary, secret)
	}
	// The non-secret settings still describe the change.
	testutil.MustContainAll(t, summary, "3002", "debug_enabled")
	testutil.Falsef(t, len(redacted) < 5, "redacted = %v, want every secret field named", redacted)
	// The URL keeps its shape so the reader sees that a proxied URL was set.
	testutil.MustContain(t, summary, "proxy.internal")
}

// TestSummarizeChange_MasksNestedURLPassword covers the same leak one level down.
func TestSummarizeChange_MasksNestedURLPassword(t *testing.T) {
	body := []byte(`{"databases":{"primary":{"dsn":"postgres://user:nested-secret@db:5432/app"}}}`)
	summary, redacted := SummarizeChange(body)
	testutil.MustNotContain(t, summary, "nested-secret")
	testutil.NotEqual(t, len(redacted), 0)
}
