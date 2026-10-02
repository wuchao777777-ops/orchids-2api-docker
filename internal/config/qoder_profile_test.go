package config

import (
	"encoding/json"
	"orchids-api/internal/testutil"
	"os"
	"path/filepath"
	"testing"
)

func TestQoderProtocolProfileConfigRoundTrip(t *testing.T) {
	for _, ext := range []string{"json", "yaml"} {
		t.Run(ext, func(t *testing.T) {
			raw := []byte(`{"qoder_protocol_profile":"skill-cli","qoder_client_id":"override"}`)
			if ext == "yaml" {
				raw = []byte("qoder_protocol_profile: skill-cli\nqoder_client_id: override\n")
			}
			path := filepath.Join(t.TempDir(), "config."+ext)
			testutil.NoError(t, os.WriteFile(path, raw, 0600))
			cfg, _, err := Load(path)
			testutil.NoError(t, err)
			testutil.False(t, cfg.QoderProtocolProfile != "skill-cli" || cfg.QoderClientID != "override", "profile or override lost loading")
			encoded, err := json.Marshal(cfg)
			testutil.NoError(t, err)
			var round Config
			err = json.Unmarshal(encoded, &round)
			testutil.NoError(t, err)
			testutil.Equal(t, round.QoderProtocolProfile, "skill-cli")
		})
	}
}
