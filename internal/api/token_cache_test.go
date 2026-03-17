package api

import (
	"net/http/httptest"
	"testing"

	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/tokencache"
)

func TestHandleTokenCacheStatsDisabledReturnsCodeFreeMaxShape(t *testing.T) {
	api := New(nil, "", "", &config.Config{EnableTokenCache: false})
	cache := tokencache.NewMemoryPromptCache(0)
	cache.CheckPromptCache("1", 10, 20, "system", "tools")
	api.SetPromptCache(cache)

	req := httptest.NewRequest("GET", "/api/token-cache/stats", nil)
	rec := httptest.NewRecorder()
	api.HandleTokenCacheStats(rec, req)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			KeyCount      int64  `json:"key_count"`
			MemoryUsed    int64  `json:"memory_used"`
			MemoryUsedStr string `json:"memory_used_str"`
			Connected     bool   `json:"connected"`
			Count         int64  `json:"count"`
			Size          int64  `json:"size"`
			Status        string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d", resp.Code)
	}
	if resp.Data.Connected {
		t.Fatalf("expected disconnected stats when feature disabled")
	}
	if resp.Data.KeyCount != 0 || resp.Data.Count != 0 || resp.Data.MemoryUsed != 0 || resp.Data.Size != 0 {
		t.Fatalf("expected zero stats when disabled, got %+v", resp.Data)
	}
	if resp.Data.MemoryUsedStr != "0 B" {
		t.Fatalf("expected 0 B, got %q", resp.Data.MemoryUsedStr)
	}
	if resp.Data.Status != "disabled" {
		t.Fatalf("expected disabled status, got %q", resp.Data.Status)
	}
}

func TestHandleTokenCacheStatsEnabledReturnsCountsAndSize(t *testing.T) {
	api := New(nil, "", "", &config.Config{EnableTokenCache: true})
	cache := tokencache.NewMemoryPromptCache(0)
	cache.CheckPromptCache("1", 10, 20, "system", "tools")
	api.SetPromptCache(cache)

	req := httptest.NewRequest("GET", "/api/token-cache/stats", nil)
	rec := httptest.NewRecorder()
	api.HandleTokenCacheStats(rec, req)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			KeyCount      int64  `json:"key_count"`
			MemoryUsed    int64  `json:"memory_used"`
			MemoryUsedStr string `json:"memory_used_str"`
			Connected     bool   `json:"connected"`
			Count         int64  `json:"count"`
			Size          int64  `json:"size"`
			Status        string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d", resp.Code)
	}
	if !resp.Data.Connected {
		t.Fatalf("expected connected stats when feature enabled")
	}
	if resp.Data.KeyCount != 2 || resp.Data.Count != 2 {
		t.Fatalf("expected two cache entries, got %+v", resp.Data)
	}
	if resp.Data.MemoryUsed <= 0 || resp.Data.Size <= 0 {
		t.Fatalf("expected positive memory usage, got %+v", resp.Data)
	}
	if resp.Data.MemoryUsed != resp.Data.Size {
		t.Fatalf("expected size aliases to match, got %+v", resp.Data)
	}
	if resp.Data.MemoryUsedStr == "" || resp.Data.MemoryUsedStr == "0 B" {
		t.Fatalf("expected formatted size string, got %q", resp.Data.MemoryUsedStr)
	}
	if resp.Data.Status != "enabled" {
		t.Fatalf("expected enabled status, got %q", resp.Data.Status)
	}
}

func TestHandleTokenCacheClearReturnsDeletedCount(t *testing.T) {
	api := New(nil, "", "", &config.Config{EnableTokenCache: true})
	cache := tokencache.NewMemoryPromptCache(0)
	cache.CheckPromptCache("0", 30, 0, "system", "")
	api.SetPromptCache(cache)

	req := httptest.NewRequest("POST", "/api/token-cache/clear", nil)
	rec := httptest.NewRecorder()
	api.HandleTokenCacheClear(rec, req)

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Deleted int64 `json:"deleted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d", resp.Code)
	}
	if resp.Message != "清除成功" {
		t.Fatalf("expected clear success message, got %q", resp.Message)
	}
	if resp.Data.Deleted != 1 {
		t.Fatalf("expected deleted count 1, got %d", resp.Data.Deleted)
	}

	count, size, err := cache.GetStats(req.Context())
	if err != nil {
		t.Fatalf("stats after clear: %v", err)
	}
	if count != 0 || size != 0 {
		t.Fatalf("expected cache to be empty after clear, got count=%d size=%d", count, size)
	}
}
