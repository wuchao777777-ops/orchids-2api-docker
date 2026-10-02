package config

import (
	"bytes"
	"encoding/base64"
	"orchids-api/internal/testutil"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateCredentialEncryptionKey(t *testing.T) {
	t.Setenv(credentialKeyEnv, "")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cfg := &Config{CredentialKeyFile: "secrets/credential.key"}

	first, source, err := LoadOrCreateCredentialEncryptionKey(configPath, cfg)
	testutil.NoError(t, err, "first load error = %v")
	testutil.Falsef(t, len(first) != 32 || source != filepath.Join(dir, "secrets", "credential.key"), "key length/source = %d, %q", len(first), source)
	second, _, err := LoadOrCreateCredentialEncryptionKey(configPath, cfg)
	testutil.NoError(t, err, "second load error = %v")
	testutil.False(t, !bytes.Equal(first, second), "persisted key changed between loads")
}

func TestLoadCredentialEncryptionKeyFromEnvironment(t *testing.T) {
	want := bytes.Repeat([]byte{0x42}, 32)
	t.Setenv(credentialKeyEnv, base64.StdEncoding.EncodeToString(want))
	got, source, err := LoadOrCreateCredentialEncryptionKey(filepath.Join(t.TempDir(), "config.json"), &Config{})
	testutil.NoError(t, err, "load error = %v")
	testutil.Falsef(t, source != "environment" || !bytes.Equal(got, want), "source/key mismatch: %q", source)
}

func TestLoadCredentialEncryptionKeyRejectsInvalidEnvironment(t *testing.T) {
	t.Setenv(credentialKeyEnv, "too-short")
	_, _, err := LoadOrCreateCredentialEncryptionKey(filepath.Join(t.TempDir(), "config.json"), &Config{})
	testutil.Error(t, err)
}
