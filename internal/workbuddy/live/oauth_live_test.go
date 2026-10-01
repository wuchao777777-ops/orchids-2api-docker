//go:build live

package live

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"orchids-api/internal/testutil"
	"orchids-api/internal/workbuddy"
)

// TestLive_StartAuthLogin exercises the real authorization bootstrap: it must
// return the official login page for a fresh state and report the transaction as
// pending until a browser completes it (which this test never does). The
// bootstrap endpoint is unauthenticated and creates only a short-lived state, so
// unlike the catalog/chat checks it needs no credential — only outbound network.
// It requires both -tags live and WB_LIVE=1, and still skips in short mode.
func TestLive_StartAuthLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("live check skipped in short mode")
	}
	if os.Getenv("WB_LIVE") != "1" {
		t.Skip("set WB_LIVE=1 to talk to the real Workbuddy authorization service")
	}
	client := workbuddy.NewFromAccount(nil, nil)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	state, authURL, err := client.StartAuthLogin(ctx, "5.5.2")
	if err != nil {
		t.Fatalf("StartAuthLogin() error = %v", err)
	}
	testutil.NotEqual(t, state, "")
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("login URL is not parseable: %v", err)
	}
	if parsed.Host != "www.workbuddy.ai" || parsed.Path != "/login" {
		t.Fatalf("login URL = %q, want the official workbuddy.ai login page", authURL)
	}
	if parsed.Query().Get("state") != state || parsed.Query().Get("platform") != "workbuddy-ai" {
		t.Fatalf("login URL query = %q", parsed.RawQuery)
	}
	testutil.Equal(t, parsed.Query().Get("version"), "5.5.2")
	t.Logf("state=%s url=%s", state, authURL)

	creds, err := client.PollAuthLogin(ctx, state)
	if err == nil {
		t.Fatalf("PollAuthLogin() = %+v, want a pending result", creds)
	}
	if !errors.Is(err, workbuddy.ErrAuthPending) {
		t.Fatalf("PollAuthLogin() error = %v, want ErrAuthPending", err)
	}
}
