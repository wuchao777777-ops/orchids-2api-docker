package grok

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
	"orchids-api/internal/util"
)

// traceparentRE is the W3C Trace Context shape for version 00.
var traceparentRE = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)

// TestTraceparentIsWellFormedForTheRequestIdTheClientMints pins the shape of the
// trace context the Build path sends. The header is derived from the request id,
// so it stays valid only while that id is 32 hex characters: a UUID would carry
// dashes into the header and the trace-context grammar rejects the value. This
// asserts the pair the client actually sends, not just the shared builder.
func TestTraceparentIsWellFormedForTheRequestIdTheClientMints(t *testing.T) {
	t.Parallel()

	requestID := randomHex(16)
	if len(requestID) != 32 {
		t.Fatalf("randomHex(16) = %q (length %d), want 32 hex characters", requestID, len(requestID))
	}
	trace := util.Traceparent(requestID)
	if !traceparentRE.MatchString(trace) {
		t.Fatalf("traceparent = %q, want a version 00 trace context", trace)
	}
	// The trace id is the same request id the client sends as x-grok-req-id, so
	// the two correlate in the upstream's logs.
	if !strings.Contains(trace, requestID) {
		t.Fatalf("traceparent %q does not carry the request id %q", trace, requestID)
	}
}

func jwtWithClaims(t *testing.T, claims string) string {
	t.Helper()
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".signature"
}

func TestCLIHeadersUseOfficialBuildIdentity(t *testing.T) {
	client := NewCLIClient(&config.Config{})
	headers := client.cliHeaders(nil, "test-access-token")
	if got := headers.Get("Authorization"); got != "Bearer test-access-token" {
		t.Fatalf("Authorization=%q", got)
	}
	if got := headers.Get("X-XAI-Token-Auth"); got != "xai-grok-cli" {
		t.Fatalf("X-XAI-Token-Auth=%q", got)
	}
	if got := headers.Get("x-grok-client-identifier"); got != "grok-shell" {
		t.Fatalf("x-grok-client-identifier=%q", got)
	}
	if got := headers.Get("x-grok-client-version"); got != "1.0.40" {
		t.Fatalf("x-grok-client-version=%q", got)
	}
	if got := headers.Get("User-Agent"); got != "grok-shell/1.0.40 (linux; x86_64)" {
		t.Fatalf("User-Agent=%q", got)
	}
}

func TestApplyCLIOAuthIdentity(t *testing.T) {
	acc := &store.Account{OAuthAccessToken: jwtWithClaims(t, `{"sub":"user-1","email":"user@example.com","team_id":"team-1"}`)}
	if !ApplyCLIOAuthIdentity(acc) {
		t.Fatal("expected identity fields to be applied")
	}
	if acc.UserID != "user-1" || acc.Email != "user@example.com" || acc.TeamID != "team-1" {
		t.Fatalf("account=%+v", acc)
	}
}

func TestApplyCLIOAuthIdentityTokenUsesIDTokenEmailForGenericLogin(t *testing.T) {
	acc := &store.Account{Name: "grok-device-login", OAuthAccessToken: jwtWithClaims(t, `{"sub":"user-1","team_id":"team-1"}`)}
	ApplyCLIOAuthIdentity(acc)
	if !ApplyCLIOAuthIdentityToken(acc, jwtWithClaims(t, `{"email":"oauth@example.com","preferred_username":"ignored@example.com"}`)) {
		t.Fatal("expected id_token identity fields to be applied")
	}
	if acc.Email != "oauth@example.com" || acc.Name != "oauth@example.com" || acc.UserID != "user-1" {
		t.Fatalf("account=%+v", acc)
	}
}
