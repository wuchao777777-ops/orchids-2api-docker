package config

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateCredentialEncryptionKey(t *testing.T) {
	t.Setenv(credentialKeyEnv, "")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cfg := &Config{CredentialKeyFile: "secrets/credential.key"}

	first, source, err := LoadOrCreateCredentialEncryptionKey(configPath, cfg)
	if err != nil {
		t.Fatalf("first load error = %v", err)
	}
	if len(first) != 32 || source != filepath.Join(dir, "secrets", "credential.key") {
		t.Fatalf("key length/source = %d, %q", len(first), source)
	}
	second, _, err := LoadOrCreateCredentialEncryptionKey(configPath, cfg)
	if err != nil {
		t.Fatalf("second load error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("persisted key changed between loads")
	}
}

func TestLoadCredentialEncryptionKeyFromEnvironment(t *testing.T) {
	want := bytes.Repeat([]byte{0x42}, 32)
	t.Setenv(credentialKeyEnv, base64.StdEncoding.EncodeToString(want))
	got, source, err := LoadOrCreateCredentialEncryptionKey(filepath.Join(t.TempDir(), "config.json"), &Config{})
	if err != nil {
		t.Fatalf("load error = %v", err)
	}
	if source != "environment" || !bytes.Equal(got, want) {
		t.Fatalf("source/key mismatch: %q", source)
	}
}

func TestLoadCredentialEncryptionKeyRejectsInvalidEnvironment(t *testing.T) {
	t.Setenv(credentialKeyEnv, "too-short")
	if _, _, err := LoadOrCreateCredentialEncryptionKey(filepath.Join(t.TempDir(), "config.json"), &Config{}); err == nil {
		t.Fatal("expected invalid environment key error")
	}
}
