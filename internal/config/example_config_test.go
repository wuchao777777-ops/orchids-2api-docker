package config

import (
	"encoding/json"
	"orchids-api/internal/testutil"
	"os"
	"path/filepath"
	"testing"
)

// TestExampleConfigLoads keeps config.example.json honest. The file is what an
// operator copies to config.json on a new host, so incompatible field types
// must be detected before deployment rather than at runtime.
func TestExampleConfigLoads(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config.example.json"))
	testutil.NoError(t, err, "read config.example.json: %v")
	var cfg Config
	testutil.NoError(t, json.Unmarshal(raw, &cfg), "config.example.json does not unmarshal into config.Config: %v")
	testutil.Falsef(t, cfg.Port == "" || cfg.RedisAddr == "", "config.example.json is missing required values: port=%q redis_addr=%q", cfg.Port, cfg.RedisAddr)
}

// TestExampleConfigUpstreamURLsMatchDefaults pins the example's upstream
// endpoints to the code defaults. These are the values that drift when a
// provider moves a route: xAI renamed the device-authorization endpoint, the
// Go default followed, and the example kept pointing at the retired
// /oauth2/device/auth path. An operator who copies the example then gets a
// Grok login that fails with an upstream 404, surfaced through Caddy and
// Cloudflare as an opaque 502. Each field is checked only when the example
// actually carries it, so a deliberate omission still passes.
func TestExampleConfigUpstreamURLsMatchDefaults(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config.example.json"))
	testutil.NoError(t, err, "read config.example.json: %v")
	var cfg Config
	testutil.NoError(t, json.Unmarshal(raw, &cfg), "config.example.json does not unmarshal into config.Config: %v")
	var empty Config
	for _, tc := range []struct {
		field string
		got   string
		want  string
	}{
		{"grok_cli_oauth_device_url", cfg.GrokCLIOAuthDeviceURL, empty.GrokCLIOAuthDeviceURLOrDefault()},
		{"grok_cli_oauth_token_url", cfg.GrokCLIOAuthTokenURL, empty.GrokCLIOAuthTokenURLOrDefault()},
		{"grok_cli_base_url", cfg.GrokCLIBaseURL, empty.GrokCLIBaseURLOrDefault()},
	} {
		if tc.got == "" {
			continue
		}
		if tc.got != tc.want {
			t.Errorf("config.example.json %s = %q, but the code default is %q; "+
				"the example must not pin a retired upstream route", tc.field, tc.got, tc.want)
		}
	}
}
