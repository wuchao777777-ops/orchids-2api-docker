package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const credentialKeyEnv = "ORCHIDS_CREDENTIAL_ENCRYPTION_KEY"

// LoadOrCreateCredentialEncryptionKey returns the 32-byte key used to encrypt
// persisted account credentials. The environment variable wins; otherwise a
// key file beside the configured data directory is created with mode 0600.
func LoadOrCreateCredentialEncryptionKey(configPath string, cfg *Config) ([]byte, string, error) {
	if raw := strings.TrimSpace(os.Getenv(credentialKeyEnv)); raw != "" {
		key, err := decodeCredentialKey(raw)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", credentialKeyEnv, err)
		}
		return key, "environment", nil
	}

	configured := ""
	if cfg != nil {
		configured = strings.TrimSpace(cfg.CredentialKeyFile)
	}
	if configured == "" {
		configured = filepath.Join("data", "credential.key")
	}
	path := configured
	if !filepath.IsAbs(path) {
		base := filepath.Dir(configPath)
		if strings.TrimSpace(configPath) == "" {
			base = "."
		}
		path = filepath.Join(base, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, "", fmt.Errorf("resolve credential key path: %w", err)
	}

	if data, readErr := os.ReadFile(path); readErr == nil {
		key, decodeErr := decodeCredentialKey(strings.TrimSpace(string(data)))
		if decodeErr != nil {
			return nil, path, fmt.Errorf("decode credential key file: %w", decodeErr)
		}
		return key, path, nil
	} else if !os.IsNotExist(readErr) {
		return nil, path, fmt.Errorf("read credential key file: %w", readErr)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, path, fmt.Errorf("create credential key directory: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, path, fmt.Errorf("generate credential key: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key) + "\n"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, path, fmt.Errorf("read concurrently-created credential key: %w", readErr)
		}
		loaded, decodeErr := decodeCredentialKey(strings.TrimSpace(string(data)))
		return loaded, path, decodeErr
	}
	if err != nil {
		return nil, path, fmt.Errorf("create credential key file: %w", err)
	}
	if _, err := file.WriteString(encoded); err != nil {
		_ = file.Close()
		return nil, path, fmt.Errorf("write credential key file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, path, fmt.Errorf("close credential key file: %w", err)
	}
	return key, path, nil
}

func decodeCredentialKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	decoders := []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		hex.DecodeString,
	}
	for _, decode := range decoders {
		if value, err := decode(raw); err == nil && len(value) == 32 {
			return value, nil
		}
	}
	if len([]byte(raw)) == 32 {
		return []byte(raw), nil
	}
	return nil, fmt.Errorf("key must decode to exactly 32 bytes")
}
