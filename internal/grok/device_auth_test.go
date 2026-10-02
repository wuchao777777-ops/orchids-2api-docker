package grok

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orchids-api/internal/config"
	"orchids-api/internal/testutil"
)

func TestDeviceAuthenticatorStartAndExchange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.NoError(t, r.ParseForm())
		testutil.Equal(t, r.Header.Get("x-grok-client-surface"), "ui")
		testutil.Equal(t, r.Header.Get("x-grok-client-version"), "test-version")
		switch r.URL.Path {
		case "/device":
			testutil.Falsef(t, r.Form.Get("client_id") != "test-client" || r.Form.Get("referrer") != "grok-build" || r.Form.Get("scope") != grokDeviceAuthorizationScope, "unexpected device form: %s", r.Form.Encode())
			_, _ = io.WriteString(w, `{"device_code":"device-secret","user_code":"ABCD-EFGH","verification_uri":"https://auth.x.ai/device","expires_in":120,"interval":3}`)
		case "/token":
			testutil.Falsef(t, r.Form.Get("device_code") != "device-secret" || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code", "unexpected token form: %s", r.Form.Encode())
			_, _ = io.WriteString(w, `{"access_token":"access-secret","refresh_token":"refresh-secret","id_token":"header.eyJzdWIiOiJ1c2VyLTEiLCJlbWFpbCI6Im9hdXRoQGV4YW1wbGUuY29tIn0.signature","expires_in":3600}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cfg := &config.Config{GrokCLIOAuthClientID: "test-client", GrokCLIClientVersion: "test-version", GrokCLIOAuthDeviceURL: server.URL + "/device", GrokCLIOAuthTokenURL: server.URL + "/token"}
	authenticator := NewDeviceAuthenticator(cfg)
	authenticator.httpClient = server.Client()
	details, err := authenticator.Start(context.Background())
	testutil.NoError(t, err, "Start() error = %v")
	testutil.Falsef(t, details.DeviceCode != "device-secret" || details.UserCode != "ABCD-EFGH" || details.Interval != 3, "details=%+v", details)
	access, refresh, identity, expiresAt, err := authenticator.Exchange(context.Background(), details.DeviceCode)
	testutil.NoError(t, err, "Exchange() error = %v")
	testutil.Falsef(t, access != "access-secret" || refresh != "refresh-secret" || time.Until(expiresAt) < 59*time.Minute, "unexpected exchange result access=%q refresh=%q expires=%s", access, refresh, expiresAt)
	testutil.Falsef(t, identity == "" || strings.Contains(access, identity) || strings.Contains(refresh, identity), "identity token was not returned independently: %q", identity)
}

func TestDeviceAuthenticatorPendingAndSanitizedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"authorization_pending","error_description":"Bearer very-secret-token"}`)
	}))
	defer server.Close()
	authenticator := NewDeviceAuthenticator(&config.Config{GrokCLIOAuthTokenURL: server.URL})
	authenticator.httpClient = server.Client()
	_, _, _, _, err := authenticator.Exchange(context.Background(), "device-secret")
	slowDown, pending := IsDeviceAuthorizationPending(err)
	testutil.Falsef(t, !pending || slowDown, "error=%v pending=%t slowDown=%t", err, pending, slowDown)
	testutil.MustNotContainAny(t, err.Error(), "very-secret-token", "device-secret")
}

func TestParseGrokDeviceOAuthErrorSlowDown(t *testing.T) {
	err := parseGrokDeviceOAuthError([]byte(`{"error":"slow_down"}`), http.StatusBadRequest)
	slowDown, pending := IsDeviceAuthorizationPending(err)
	testutil.Falsef(t, !pending || !slowDown, "error=%v pending=%t slowDown=%t", err, pending, slowDown)
}
