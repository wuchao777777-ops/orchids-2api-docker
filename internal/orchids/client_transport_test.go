package orchids

import (
	"testing"

	"orchids-api/internal/config"
)

func TestUpstreamURLDefaultsToCodeFreeMaxRunAppEndpoint(t *testing.T) {
	t.Parallel()

	client := &Client{}
	if got := client.upstreamURL(); got != "https://orchids-v2-alpha-108292236521.europe-west1.run.app/agent/coding-agent" {
		t.Fatalf("upstreamURL()=%q want CodeFreeMax run.app endpoint", got)
	}
}

func TestUpstreamURLUsesExplicitOverrideWhenProvided(t *testing.T) {
	t.Parallel()

	client := &Client{
		config: &config.Config{
			UpstreamURL: "https://example.com/custom",
		},
	}
	if got := client.upstreamURL(); got != "https://example.com/custom" {
		t.Fatalf("upstreamURL()=%q want explicit override", got)
	}
}
