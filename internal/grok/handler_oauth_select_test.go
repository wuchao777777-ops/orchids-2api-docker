package grok

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"orchids-api/internal/config"
	"orchids-api/internal/loadbalancer"
	"orchids-api/internal/store"
)

func TestOpenChatAccountSessionSkipsOAuthAccounts(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{
		StoreMode:   "redis",
		RedisAddr:   mini.Addr(),
		RedisDB:     0,
		RedisPrefix: "test:",
	})
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		mini.Close()
	})

	ctx := context.Background()
	for _, acc := range []*store.Account{
		{
			AccountType:       "grok",
			CredentialType:    "oauth",
			OAuthAccessToken:  "oauth-access",
			OAuthRefreshToken: "oauth-refresh",
			Enabled:           true,
			Subscription:      "super",
			Weight:            100,
		},
		{
			AccountType:  "grok",
			ClientCookie: "sso=sso-token",
			Enabled:      true,
			Subscription: "super",
			Weight:       1,
		},
	} {
		if err := s.CreateAccount(ctx, acc); err != nil {
			t.Fatalf("CreateAccount() error = %v", err)
		}
	}

	h := NewHandler(&config.Config{}, loadbalancer.NewWithCacheTTL(s, 0))
	spec := ModelSpec{ID: "grok-4.5", ConsoleModel: "grok-4.5", Tier: grokTierSuper}
	sess, err := h.openChatAccountSessionForModel(ctx, spec)
	if err != nil {
		t.Fatalf("openChatAccountSessionForModel() error = %v", err)
	}
	defer sess.Close()
	if NormalizeSSOToken(sess.token) != "sso-token" {
		t.Fatalf("selected token=%q want sso-token", sess.token)
	}
	if sess.acc != nil && strings.EqualFold(sess.acc.CredentialType, "oauth") {
		t.Fatal("SSO path selected an OAuth account")
	}
}

func TestSelectAccountSkipsOAuthAccounts(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{
		StoreMode:   "redis",
		RedisAddr:   mini.Addr(),
		RedisDB:     0,
		RedisPrefix: "test:",
	})
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		mini.Close()
	})

	ctx := context.Background()
	if err := s.CreateAccount(ctx, &store.Account{
		AccountType:       "grok",
		CredentialType:    "oauth",
		OAuthAccessToken:  "oauth-access",
		OAuthRefreshToken: "oauth-refresh",
		Enabled:           true,
		Subscription:      "super",
		Weight:            1,
	}); err != nil {
		t.Fatalf("CreateAccount oauth error = %v", err)
	}
	if err := s.CreateAccount(ctx, &store.Account{
		AccountType:  "grok",
		ClientCookie: "sso=admin-sso",
		Enabled:      true,
		Subscription: "super",
		Weight:       1,
	}); err != nil {
		t.Fatalf("CreateAccount sso error = %v", err)
	}

	h := NewHandler(&config.Config{}, loadbalancer.NewWithCacheTTL(s, 0))
	acc, token, err := h.selectAccount(ctx)
	if err != nil {
		t.Fatalf("selectAccount() error = %v", err)
	}
	if NormalizeSSOToken(token) != "admin-sso" {
		t.Fatalf("token=%q want admin-sso", token)
	}
	if acc != nil && strings.EqualFold(acc.CredentialType, "oauth") {
		t.Fatal("selectAccount returned OAuth account")
	}
}
