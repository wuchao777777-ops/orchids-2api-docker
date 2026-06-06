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

func writeTokenCacheStats(w http.ResponseWriter, promptConnected bool, promptCount, promptSize int64, estimateConnected bool, estimateCount, estimateSize int64) {
	status := "disabled"
	if promptConnected || estimateConnected {
		status = "enabled"
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code": 0,
		"data": map[string]interface{}{
			"key_count":       promptCount,
			"memory_used":     promptSize,
			"memory_used_str": formatTokenCacheBytes(promptSize),
			"connected":       promptConnected,
			"count":           promptCount,
			"size":            promptSize,
			"status":          status,
			"prompt_cache": map[string]interface{}{
				"connected":       promptConnected,
				"key_count":       promptCount,
				"memory_used":     promptSize,
				"memory_used_str": formatTokenCacheBytes(promptSize),
			},
			"estimate_cache": map[string]interface{}{
				"connected":       estimateConnected,
				"key_count":       estimateCount,
				"memory_used":     estimateSize,
				"memory_used_str": formatTokenCacheBytes(estimateSize),
			},
		},
	})
}

// HandleTokenCacheStats handles GET /api/token-cache/stats
func (a *API) HandleTokenCacheStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cfg := a.config.Load()
	promptConnected := cfg != nil && cfg.EnableTokenCache && a.promptCache != nil
	estimateConnected := cfg != nil && cfg.CacheTokenCount && a.tokenCache != nil

	var promptCount, promptBytes int64
	if promptConnected {
		promptCount, promptBytes, _ = a.promptCache.GetStats(r.Context())
	}

	var estimateCount, estimateBytes int64
	if estimateConnected {
		estimateCount, estimateBytes, _ = a.tokenCache.GetStats(r.Context())
	}

	writeTokenCacheStats(w, promptConnected, promptCount, promptBytes, estimateConnected, estimateCount, estimateBytes)
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
