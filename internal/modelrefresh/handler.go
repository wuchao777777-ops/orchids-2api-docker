package modelrefresh

import (
	"encoding/json"
	"net/http"
	"strings"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
)

type refreshRequest struct {
	Channel     string `json:"channel"`
	Concurrency int    `json:"concurrency,omitempty"`
}

// NewRefreshHandler serves POST /api/models/refresh. A refresh is serialized
// per channel: a second request for a channel that is already refreshing is
// rejected instead of racing it.
// NewRefreshHandler serves POST /api/models/refresh with its own coordinator.
func NewRefreshHandler(configSnapshot func() *config.Config, s *store.Store) http.HandlerFunc {
	return NewRefreshHandlerWithCoordinator(configSnapshot, s, NewCoordinator())
}

// NewRefreshHandlerWithCoordinator serves the same endpoint against a caller
// owned coordinator, so one process can share a single serialization point
// across handlers and tests.
func NewRefreshHandlerWithCoordinator(configSnapshot func() *config.Config, s *store.Store, coordinator *Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		channel := strings.TrimSpace(r.URL.Query().Get("channel"))
		concurrency := defaultModelRefreshConcurrency
		if parsed, ok := parseModelRefreshConcurrency(r.URL.Query().Get("concurrency")); ok {
			concurrency = parsed
		}
		if r.Body != nil {
			defer r.Body.Close()
			var req refreshRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err == nil && strings.TrimSpace(req.Channel) != "" {
				channel = strings.TrimSpace(req.Channel)
			}
			if req.Concurrency != 0 {
				concurrency = normalizeModelRefreshConcurrency(req.Concurrency)
			}
		}

		release, acquired := coordinator.tryAcquire(channel)
		if !acquired {
			http.Error(w, "model refresh already running for this channel", http.StatusConflict)
			return
		}
		defer release()
		distributedRelease, distributedAcquired := acquireDistributedModelRefresh(r.Context(), s, channel)
		if !distributedAcquired {
			http.Error(w, "model refresh already running for this channel", http.StatusConflict)
			return
		}
		defer distributedRelease()
		cfg := configSnapshot()
		result, err := runModelRefresh(r.Context(), cfg, s, channel, concurrency)
		if err != nil {
			// No active account is a legitimate state, not a failure: nothing was
			// fetched, so nothing is published. It is reported as a skipped
			// refresh so the admin page can distinguish it from an upstream
			// outage and from a successful empty refresh.
			if isNoActiveAccounts(err) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&Result{
					Channel:     normalizeAdminModelChannel(channel),
					Source:      "no_active_account",
					Concurrency: concurrency,
					Skipped:     true,
				})
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if result != nil && result.Concurrency == 0 {
			result.Concurrency = concurrency
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
