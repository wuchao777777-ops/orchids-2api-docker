package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
)

func setupConfigAPI(t *testing.T) (*API, *store.Store, *miniredis.Miniredis) {
	t.Helper()

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

	cfg := &config.Config{
		AdminPass:          "initial-secret",
		AdminToken:         "initial-token",
		EnableTokenCache:   true,
		TokenCacheTTL:      300,
		TokenCacheStrategy: "1",
		ProxyHTTP:          "http://127.0.0.1:7890",
		ProxyBypass:        []string{"example.com"},
	}
	config.ApplyDefaults(cfg)

	return New(s, "admin", "pass", cfg), s, mini
}

func TestHandleConfigListReturnsCodeFreeMaxShape(t *testing.T) {
	api, s, mini := setupConfigAPI(t)
	defer func() {
		_ = s.Close()
		mini.Close()
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/config/list", nil)
	rec := httptest.NewRecorder()
	api.HandleConfigList(rec, req)

	var resp struct {
		Code int                    `json:"code"`
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d", resp.Code)
	}
	if got := resp.Data["admin_pass"]; got != "initial-secret" {
		t.Fatalf("admin_pass=%v want initial-secret", got)
	}
	if got := resp.Data["admin_password"]; got != "initial-secret" {
		t.Fatalf("admin_password=%v want initial-secret", got)
	}
	if got := resp.Data["admin_token"]; got != "initial-token" {
		t.Fatalf("admin_token=%v want initial-token", got)
	}
	if got := resp.Data["token_cache_strategy"]; got != "1" {
		t.Fatalf("token_cache_strategy=%v want 1", got)
	}
}

func TestHandleConfigSaveAcceptsCodeFreeMaxStylePayload(t *testing.T) {
	api, s, mini := setupConfigAPI(t)
	defer func() {
		_ = s.Close()
		mini.Close()
	}()

	body := `{
		"admin_password":"changed-secret",
		"enable_token_cache":"false",
		"token_cache_ttl":"900",
		"token_cache_strategy":"0",
		"proxy_bypass":"example.com, internal.local"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/config/save", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.HandleConfigSave(rec, req)

	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d body=%s", resp.Code, rec.Body.String())
	}
	if resp.Msg != "success" {
		t.Fatalf("msg=%q want success", resp.Msg)
	}

	cfg := api.config.Load()
	if cfg == nil {
		t.Fatal("config not stored")
	}
	if cfg.AdminPass != "changed-secret" {
		t.Fatalf("AdminPass=%q want changed-secret", cfg.AdminPass)
	}
	if cfg.EnableTokenCache {
		t.Fatalf("EnableTokenCache=%v want false", cfg.EnableTokenCache)
	}
	if cfg.TokenCacheTTL != 900 {
		t.Fatalf("TokenCacheTTL=%d want 900", cfg.TokenCacheTTL)
	}
	if cfg.TokenCacheStrategy != "0" {
		t.Fatalf("TokenCacheStrategy=%q want 0", cfg.TokenCacheStrategy)
	}
	if len(cfg.ProxyBypass) != 2 || cfg.ProxyBypass[0] != "example.com" || cfg.ProxyBypass[1] != "internal.local" {
		t.Fatalf("ProxyBypass=%v want [example.com internal.local]", cfg.ProxyBypass)
	}

	saved, err := s.GetSetting(context.Background(), "config")
	if err != nil {
		t.Fatalf("GetSetting(config) error = %v", err)
	}
	if !strings.Contains(saved, `"admin_pass":"changed-secret"`) {
		t.Fatalf("saved config missing updated admin_pass: %s", saved)
	}
}
