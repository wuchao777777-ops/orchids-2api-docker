package api

import (
	"fmt"
	"net/http"

	"github.com/goccy/go-json"
)

func (a *API) tokenCacheFeatureEnabled() bool {
	cfg := a.config.Load()
	return cfg != nil && cfg.EnableTokenCache
}

func formatTokenCacheBytes(size int64) string {
	switch {
	case size >= 1024*1024:
		return fmt.Sprintf("%.2f MB", float64(size)/1024/1024)
	case size >= 1024:
		return fmt.Sprintf("%.2f KB", float64(size)/1024)
	default:
		return fmt.Sprintf("%d B", size)
	}
}

func writeTokenCacheStats(w http.ResponseWriter, connected bool, count, size int64) {
	status := "disabled"
	if connected {
		status = "enabled"
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code": 0,
		"data": map[string]interface{}{
			"key_count":       count,
			"memory_used":     size,
			"memory_used_str": formatTokenCacheBytes(size),
			"connected":       connected,
			"count":           count,
			"size":            size,
			"status":          status,
		},
	})
}

// HandleTokenCacheStats handles GET /api/token-cache/stats
func (a *API) HandleTokenCacheStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !a.tokenCacheFeatureEnabled() || a.promptCache == nil {
		writeTokenCacheStats(w, false, 0, 0)
		return
	}

	count, memBytes, _ := a.promptCache.GetStats(r.Context())
	writeTokenCacheStats(w, true, count, memBytes)
}

// HandleTokenCacheClear handles POST /api/token-cache/clear
func (a *API) HandleTokenCacheClear(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !a.tokenCacheFeatureEnabled() || a.promptCache == nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code":    0,
			"message": "清除成功",
			"data": map[string]interface{}{
				"deleted": 0,
			},
		})
		return
	}

	count, _, _ := a.promptCache.GetStats(r.Context())
	if err := a.promptCache.Clear(r.Context()); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code":    1,
			"message": "Failed to clear token cache: " + err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code":    0,
		"message": "清除成功",
		"data": map[string]interface{}{
			"deleted": count,
		},
	})
}
