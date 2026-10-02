package config

import (
	"encoding/json"
	"orchids-api/internal/testutil"
	"path/filepath"
	"testing"
)

func TestConfigDefaults(t *testing.T) {
	var cfg Config
	ApplyDefaults(&cfg)

	testutil.Equal(t, cfg.ChatDefaultStream(), true)
	testutil.Equal(t, cfg.ResponseStoreTTL, 720)
	testutil.Equal(t, cfg.MediaDir, "data"+string(filepath.Separator)+"tmp")
	testutil.Equal(t, cfg.GrokCLIClientVersionOrDefault(), "1.0.40")
	got := cfg.GrokCLIUserAgentOrDefault()
	testutil.Falsef(t, got != "grok-shell/1.0.40 (linux; x86_64)", "GrokCLIUserAgentOrDefault()=%q", got)
}

func TestCloneDeepCopiesReferenceFields(t *testing.T) {
	original := &Config{
		TrustedProxies:  []string{"10.0.0.1"},
		GrokEgressNodes: []EgressNodeConfig{{Name: "primary", URL: "http://proxy"}},
		ProxyBypass:     []string{"localhost"},
	}

	clone := original.Clone()
	clone.TrustedProxies[0] = "10.0.0.2"
	clone.GrokEgressNodes[0].Name = "changed"
	clone.ProxyBypass[0] = "example.com"

	if original.TrustedProxies[0] != "10.0.0.1" || original.GrokEgressNodes[0].Name != "primary" ||
		original.ProxyBypass[0] != "localhost" {
		t.Fatalf("Clone shares mutable fields with original: %#v", original)
	}
}

func TestApplyDefaultsGeneratesRandomPassword(t *testing.T) {
	var cfg Config
	ApplyDefaults(&cfg)

	testutil.NotEqual(t, cfg.AdminPass, "")
	testutil.NotEqual(t, cfg.AdminPass, "admin123")
	testutil.Falsef(t, len(cfg.AdminPass) < 16, "AdminPass too short: got %d chars, want at least 16", len(cfg.AdminPass))

	// Verify each call generates a different password.
	var cfg2 Config
	ApplyDefaults(&cfg2)
	testutil.NotEqual(t, cfg.AdminPass, cfg2.AdminPass)
}

func TestApplyHardcodedOverridesValues(t *testing.T) {
	cfg := Config{
		MaxRetries:     999,
		RequestTimeout: 999,
	}
	ApplyHardcoded(&cfg)

	testutil.Equal(t, cfg.MaxRetries, 20)
	testutil.Equal(t, cfg.RequestTimeout, 999)
	testutil.Equal(t, cfg.ConcurrencyTimeout, cfg.RequestTimeout)
}

func TestApplyDefaultsPreservesConfigurableFields(t *testing.T) {
	cfg := Config{
		Port:               "8080",
		AdminUser:          "myuser",
		AdminPass:          "mypass",
		AdminPath:          "/myadmin",
		RedisAddr:          "redis:6380",
		DeploymentInstance: "replica-a",
		MediaDir:           "/srv/orchids-media",
	}
	ApplyDefaults(&cfg)

	testutil.Equal(t, cfg.Port, "8080")
	testutil.Equal(t, cfg.AdminUser, "myuser")
	testutil.Equal(t, cfg.AdminPass, "mypass")
	testutil.Equal(t, cfg.AdminPath, "/myadmin")
	testutil.Equal(t, cfg.RedisAddr, "redis:6380")
	testutil.Equal(t, cfg.DeploymentInstance, "replica-a")
	testutil.Equal(t, cfg.MediaDir, "/srv/orchids-media")
}

// Legacy inference_auth_enabled values must not disable managed-key checks.
// Unknown JSON config keys are ignored, including this removed switch.
func TestLegacyInferenceAuthOptOutIsIgnored(t *testing.T) {
	var cfg Config
	testutil.NoError(t, json.Unmarshal([]byte(`{"inference_auth_enabled":false}`), &cfg))
	ApplyDefaults(&cfg)
	testutil.False(t, cfg.AnonymousAllowIPs != nil, "legacy opt-out must not introduce an anonymous allowlist")
}
