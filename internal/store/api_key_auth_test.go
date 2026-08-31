package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestValidateApiKey(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := New(Options{RedisAddr: mini.Addr(), RedisPrefix: "auth-test:"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	raw := "sk-test-secret"
	digest := sha256.Sum256([]byte(raw))
	key := &ApiKey{
		Name:      "test",
		KeyHash:   hex.EncodeToString(digest[:]),
		KeyPrefix: "sk-",
		KeySuffix: "cret",
		Enabled:   true,
	}
	if err := s.CreateApiKey(context.Background(), key); err != nil {
		t.Fatalf("CreateApiKey() error = %v", err)
	}

	valid, err := s.ValidateApiKey(context.Background(), raw)
	if err != nil || !valid {
		t.Fatalf("ValidateApiKey(valid) = %v, %v", valid, err)
	}
	valid, err = s.ValidateApiKey(context.Background(), "sk-wrong")
	if err != nil || valid {
		t.Fatalf("ValidateApiKey(wrong) = %v, %v", valid, err)
	}
	key.Enabled = false
	if err := s.UpdateApiKey(context.Background(), key); err != nil {
		t.Fatalf("UpdateApiKey() error = %v", err)
	}
	valid, err = s.ValidateApiKey(context.Background(), raw)
	if err != nil || valid {
		t.Fatalf("ValidateApiKey(disabled) = %v, %v", valid, err)
	}
}

func TestAuthorizeApiKeyPolicyAndRPM(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := New(Options{RedisAddr: mini.Addr(), RedisPrefix: "auth-policy-test:"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	raw := "sk-policy-secret"
	digest := sha256.Sum256([]byte(raw))
	key := &ApiKey{
		Name:          "policy",
		KeyHash:       hex.EncodeToString(digest[:]),
		KeyPrefix:     "sk-",
		KeySuffix:     "cret",
		Enabled:       true,
		AllowedModels: []string{"grok-4.6"},
		RPMLimit:      2,
	}
	if err := s.CreateApiKey(context.Background(), key); err != nil {
		t.Fatalf("CreateApiKey() error = %v", err)
	}

	for i := 0; i < 2; i++ {
		got, err := s.AuthorizeApiKey(context.Background(), raw)
		if err != nil || got == nil || len(got.AllowedModels) != 1 || got.AllowedModels[0] != "grok-4.6" {
			t.Fatalf("AuthorizeApiKey(%d) = %#v, %v", i, got, err)
		}
	}
	persisted, err := s.GetApiKeyByID(context.Background(), key.ID)
	if err != nil || persisted.LastUsedAt == nil {
		t.Fatalf("last_used_at was not persisted: key=%#v err=%v", persisted, err)
	}
	if _, err := s.AuthorizeApiKey(context.Background(), raw); err != ErrApiKeyRateLimited {
		t.Fatalf("third AuthorizeApiKey() error = %v, want %v", err, ErrApiKeyRateLimited)
	}
}

func TestAuthorizeApiKeyRejectsExpiredKey(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := New(Options{RedisAddr: mini.Addr(), RedisPrefix: "auth-expired-test:"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	raw := "sk-expired-secret"
	digest := sha256.Sum256([]byte(raw))
	expiresAt := time.Now().UTC().Add(-time.Minute)
	key := &ApiKey{
		Name:      "expired",
		KeyHash:   hex.EncodeToString(digest[:]),
		KeyPrefix: "sk-",
		KeySuffix: "cret",
		Enabled:   true,
		ExpiresAt: &expiresAt,
	}
	if err := s.CreateApiKey(context.Background(), key); err != nil {
		t.Fatalf("CreateApiKey() error = %v", err)
	}
	if _, err := s.AuthorizeApiKey(context.Background(), raw); err != ErrApiKeyExpired {
		t.Fatalf("AuthorizeApiKey() error = %v, want %v", err, ErrApiKeyExpired)
	}
}

func TestUpdateApiKeyPolicyPersists(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := New(Options{RedisAddr: mini.Addr(), RedisPrefix: "auth-update-test:"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	key := &ApiKey{Name: "update", KeyHash: "hash", KeyPrefix: "sk-", KeySuffix: "hash", Enabled: true}
	if err := s.CreateApiKey(context.Background(), key); err != nil {
		t.Fatalf("CreateApiKey() error = %v", err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	key.AllowedModels = []string{"grok-4.6", "grok-imagine-video"}
	key.RPMLimit = 17
	key.ExpiresAt = &expiresAt
	if err := s.UpdateApiKey(context.Background(), key); err != nil {
		t.Fatalf("UpdateApiKey() error = %v", err)
	}
	got, err := s.GetApiKeyByID(context.Background(), key.ID)
	if err != nil {
		t.Fatalf("GetApiKeyByID() error = %v", err)
	}
	if got.RPMLimit != 17 || got.ExpiresAt == nil || !got.ExpiresAt.Equal(expiresAt) || len(got.AllowedModels) != 2 {
		t.Fatalf("persisted key = %#v", got)
	}
}
