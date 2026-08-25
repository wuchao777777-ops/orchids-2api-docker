package grok

import (
	"testing"

	"orchids-api/internal/config"
)

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
	if got := headers.Get("x-grok-client-version"); got != "1.0.4" {
		t.Fatalf("x-grok-client-version=%q", got)
	}
	if got := headers.Get("User-Agent"); got != "grok-shell/1.0.4 (linux; x86_64)" {
		t.Fatalf("User-Agent=%q", got)
	}
}
