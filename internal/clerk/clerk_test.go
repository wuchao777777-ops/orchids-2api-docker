package clerk

import (
	"encoding/base64"
	"github.com/goccy/go-json"
	"testing"
)

func TestParseClientCookies_FullCookie_AllowsOpaqueClientValue(t *testing.T) {
	t.Parallel()

	opaque := "v2_eyJhbGciOiJkaXIifQ.abc.def.ghi.jkl"
	in := "__client=" + opaque + "; __client_uat=1739251200"

	client, session, err := ParseClientCookies(in)
	if err != nil {
		t.Fatalf("ParseClientCookies returned error: %v", err)
	}
	if client != opaque {
		t.Fatalf("client = %q, want %q", client, opaque)
	}
	if session != "" {
		t.Fatalf("session = %q, want empty", session)
	}
}

func TestParseClientCookies_FullCookie_ParsesSessionWhenPresent(t *testing.T) {
	t.Parallel()

	sessionJWT := fakeJWT(map[string]interface{}{
		"sid": "sess_123",
		"sub": "user_456",
	})
	in := "__client=opaque-client-value; __session=" + sessionJWT

	client, session, err := ParseClientCookies(in)
	if err != nil {
		t.Fatalf("ParseClientCookies returned error: %v", err)
	}
	if client != "opaque-client-value" {
		t.Fatalf("client = %q, want %q", client, "opaque-client-value")
	}
	if session != sessionJWT {
		t.Fatalf("session = %q, want %q", session, sessionJWT)
	}
}

func TestParseClientCookies_FullCookie_DecodesPercentEncoding(t *testing.T) {
	t.Parallel()

	in := "__client=abc%2Edef%2Eghi; __session=s1%2Es2%2Es3"
	client, session, err := ParseClientCookies(in)
	if err != nil {
		t.Fatalf("ParseClientCookies returned error: %v", err)
	}
	if client != "abc.def.ghi" {
		t.Fatalf("client = %q, want %q", client, "abc.def.ghi")
	}
	if session != "s1.s2.s3" {
		t.Fatalf("session = %q, want %q", session, "s1.s2.s3")
	}
}

func TestParseClientCookies_PlainJWTStillWorks(t *testing.T) {
	t.Parallel()

	jwt := fakeJWT(map[string]interface{}{"sub": "user_1"})
	client, session, err := ParseClientCookies(jwt)
	if err != nil {
		t.Fatalf("ParseClientCookies returned error: %v", err)
	}
	if client != jwt {
		t.Fatalf("client = %q, want %q", client, jwt)
	}
	if session != "" {
		t.Fatalf("session = %q, want empty", session)
	}
}

func TestParseClientCookies_PlainOpaqueTokenAccepted(t *testing.T) {
	t.Parallel()

	opaque := "v2_eyJhbGciOiJkaXIifQ.abc.def.ghi.jkl"
	client, session, err := ParseClientCookies(opaque)
	if err != nil {
		t.Fatalf("ParseClientCookies returned error: %v", err)
	}
	if client != opaque {
		t.Fatalf("client = %q, want %q", client, opaque)
	}
	if session != "" {
		t.Fatalf("session = %q, want empty", session)
	}
}

func TestParseClientCookies_InvalidPlainInputRejected(t *testing.T) {
	t.Parallel()

	if _, _, err := ParseClientCookies("short-token"); err == nil {
		t.Fatalf("expected error for invalid plain token input")
	}
}

func TestParseOrchidsCookies_JSONExport_SessionOnly(t *testing.T) {
	t.Parallel()

	sessionJWT := fakeJWT(map[string]interface{}{
		"sid": "sess_json",
		"sub": "user_json",
	})
	input := `[
		{"name":"__session_zF1LqDSA","value":"` + sessionJWT + `"},
		{"name":"__client_uat_zF1LqDSA","value":"1773712060"}
	]`

	parsed, ok, err := ParseOrchidsCookies(input)
	if err != nil {
		t.Fatalf("ParseOrchidsCookies returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected Orchids cookies to be detected")
	}
	if parsed.ClientCookie != "" {
		t.Fatalf("client cookie = %q, want empty", parsed.ClientCookie)
	}
	if parsed.SessionCookie != sessionJWT {
		t.Fatalf("session cookie = %q, want %q", parsed.SessionCookie, sessionJWT)
	}
	if parsed.ClientUat != "1773712060" {
		t.Fatalf("client uat = %q, want %q", parsed.ClientUat, "1773712060")
	}
}

func TestParseOrchidsCookies_CookieHeader_PrefersExactNames(t *testing.T) {
	t.Parallel()

	sessionJWT := fakeJWT(map[string]interface{}{
		"sid": "sess_exact",
		"sub": "user_exact",
	})
	in := "__session_zF1LqDSA=old; __session=" + sessionJWT + "; __client_uat=1773712060"

	parsed, ok, err := ParseOrchidsCookies(in)
	if err != nil {
		t.Fatalf("ParseOrchidsCookies returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected Orchids cookies to be detected")
	}
	if parsed.SessionCookie != sessionJWT {
		t.Fatalf("session cookie = %q, want %q", parsed.SessionCookie, sessionJWT)
	}
	if parsed.ClientUat != "1773712060" {
		t.Fatalf("client uat = %q, want %q", parsed.ClientUat, "1773712060")
	}
}

func TestBuildClerkCookieHeader_IncludesClientUatAndActiveContext(t *testing.T) {
	t.Parallel()

	got := buildClerkCookieHeader("client_cookie", "session_cookie", "1773712060", "sess_ctx")
	want := "__client=client_cookie; __session=session_cookie; __client_uat=1773712060; clerk_active_context=sess_ctx:"
	if got != want {
		t.Fatalf("buildClerkCookieHeader()=%q want %q", got, want)
	}
}

func TestParseSessionInfoFromJWT(t *testing.T) {
	t.Parallel()

	jwt := fakeJWT(map[string]interface{}{
		"sid": "sess_abc",
		"sub": "user_xyz",
	})
	sid, sub := ParseSessionInfoFromJWT(jwt)
	if sid != "sess_abc" {
		t.Fatalf("sid = %q, want %q", sid, "sess_abc")
	}
	if sub != "user_xyz" {
		t.Fatalf("sub = %q, want %q", sub, "user_xyz")
	}
}

func fakeJWT(payload map[string]interface{}) string {
	header := map[string]interface{}{
		"alg": "none",
		"typ": "JWT",
	}
	h, _ := json.Marshal(header)
	p, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p) + ".sig"
}
