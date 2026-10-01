package middleware

import (
	"net/http"
	"strings"
	"sync/atomic"
)

// Explicit channel entrances have independent capacity budgets. Unified model
// entrances remain protected by global and atomic per-account admission.
func ProviderAdmission(limits func() map[string]int) func(http.HandlerFunc) http.HandlerFunc {
	counts := map[string]*atomic.Int64{"grok": {}, "qoder": {}, "cline": {}, "workbuddy": {}}
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			name, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
			count := counts[name]
			if count == nil || limits == nil {
				next(w, r)
				return
			}
			limit := limits()[name]
			if limit <= 0 {
				next(w, r)
				return
			}
			for {
				n := count.Load()
				if n >= int64(limit) {
					w.Header().Set("Retry-After", "1")
					writeAPIKeyError(w, http.StatusServiceUnavailable, "provider is overloaded; retry later", "provider_overloaded")
					return
				}
				if count.CompareAndSwap(n, n+1) {
					break
				}
			}
			defer count.Add(-1)
			next(w, r)
		}
	}
}
