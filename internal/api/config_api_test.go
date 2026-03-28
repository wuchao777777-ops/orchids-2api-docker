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
		AdminPass:            "initial-secret",
		AdminToken:           "initial-token",
		EnableTokenCache:     true,
		TokenCacheTTL:        300,
		TokenCacheStrategy:   "1",
		ProxyURL:             "http://127.0.0.1:7890",
		ProxyBypass:          []string{"example.com"},
		WarpTransportProfile: "browser",
		WarpUseUTLS:          true,
		WarpClientOSCategory: "Linux",
		WarpClientOSName:     "Ubuntu",
		WarpClientOSVersion:  "24.04",
		WarpUserAgent:        "Warp/Test",
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
	if got := resp.Data["proxy_url"]; got != "http://127.0.0.1:7890" {
		t.Fatalf("proxy_url=%v want http://127.0.0.1:7890", got)
	}
	if got := resp.Data["warp_transport_profile"]; got != "browser" {
		t.Fatalf("warp_transport_profile=%v want browser", got)
	}
	if got := resp.Data["warp_use_utls"]; got != true {
		t.Fatalf("warp_use_utls=%v want true", got)
	}
	if got := resp.Data["warp_client_os_name"]; got != "Ubuntu" {
		t.Fatalf("warp_client_os_name=%v want Ubuntu", got)
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
		"proxy_url":"socks5://user:pass@127.0.0.1:1080",
		"proxy_bypass":"example.com, internal.local",
		"warp_transport_profile":"utls",
		"warp_use_utls":true,
		"warp_client_os_category":"Linux",
		"warp_client_os_name":"Ubuntu",
		"warp_client_os_version":"24.04",
		"warp_user_agent":"WarpDiag/Test"
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
	if cfg.ProxyURL != "socks5://user:pass@127.0.0.1:1080" {
		t.Fatalf("ProxyURL=%q want socks5://user:pass@127.0.0.1:1080", cfg.ProxyURL)
	}
	if len(cfg.ProxyBypass) != 2 || cfg.ProxyBypass[0] != "example.com" || cfg.ProxyBypass[1] != "internal.local" {
		t.Fatalf("ProxyBypass=%v want [example.com internal.local]", cfg.ProxyBypass)
	}
	if cfg.WarpTransportProfile != "utls" {
		t.Fatalf("WarpTransportProfile=%q want utls", cfg.WarpTransportProfile)
	}
	if !cfg.WarpUseUTLS {
		t.Fatalf("WarpUseUTLS=%v want true", cfg.WarpUseUTLS)
	}
	if cfg.WarpClientOSCategory != "Linux" {
		t.Fatalf("WarpClientOSCategory=%q want Linux", cfg.WarpClientOSCategory)
	}
	if cfg.WarpClientOSName != "Ubuntu" {
		t.Fatalf("WarpClientOSName=%q want Ubuntu", cfg.WarpClientOSName)
	}
	if cfg.WarpClientOSVersion != "24.04" {
		t.Fatalf("WarpClientOSVersion=%q want 24.04", cfg.WarpClientOSVersion)
	}
	if cfg.WarpUserAgent != "WarpDiag/Test" {
		t.Fatalf("WarpUserAgent=%q want WarpDiag/Test", cfg.WarpUserAgent)
	}

	saved, err := s.GetSetting(context.Background(), "config")
	if err != nil {
		t.Fatalf("GetSetting(config) error = %v", err)
	}
	if !strings.Contains(saved, `"admin_pass":"changed-secret"`) {
		t.Fatalf("saved config missing updated admin_pass: %s", saved)
	}
	if !strings.Contains(saved, `"warp_transport_profile":"utls"`) {
		t.Fatalf("saved config missing updated warp transport profile: %s", saved)
	}
}

func TestHandleConfigSaveUpdatesOriginalSharedConfigPointer(t *testing.T) {
	api, s, mini := setupConfigAPI(t)
	defer func() {
		_ = s.Close()
		mini.Close()
	}()

	original := api.config.Load()
	if original == nil {
		t.Fatal("expected initial config")
	}

	body := `{
		"proxy_url":"http://alice:secret@127.0.0.1:9090"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/config/save", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.HandleConfigSave(rec, req)

	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d body=%s", resp.Code, rec.Body.String())
	}

	if original.ProxyURL != "http://alice:secret@127.0.0.1:9090" {
		t.Fatalf("shared ProxyURL=%q want updated value", original.ProxyURL)
	}
	if api.config.Load() != original {
		t.Fatal("expected API config pointer to keep sharing the original config object")
	}
}
