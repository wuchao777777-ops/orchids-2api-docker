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
