package grok

import (
	"context"
	"fmt"
	"github.com/goccy/go-json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"orchids-api/internal/store"
)

type adminTokenRefreshRequest struct {
	Token       string   `json:"token"`
	Tokens      []string `json:"tokens"`
	Concurrency int      `json:"concurrency"`
	Model       string   `json:"model,omitempty"`
}

func normalizeTokenRefreshConcurrency(v int) int {
	return normalizeNSFWConcurrency(v)
}

func collectRefreshTokens(req adminTokenRefreshRequest) []string {
	dedup := map[string]struct{}{}
	add := func(raw string) {
		token := NormalizeSSOToken(raw)
		if strings.TrimSpace(token) == "" {
			return
		}
		dedup[token] = struct{}{}
	}

	add(req.Token)
	for _, raw := range req.Tokens {
		add(raw)
	}

	out := make([]string, 0, len(dedup))
	for token := range dedup {
		out = append(out, token)
	}
	sort.Strings(out)
	return out
}

func collectGrokAccountsByToken(accounts []*store.Account) map[string][]*store.Account {
	result := make(map[string][]*store.Account, len(accounts))
	for _, acc := range accounts {
		if !isGrokAccount(acc) {
			continue
		}
		token := grokAccountToken(acc)
		if token == "" {
			continue
		}
		result[token] = append(result[token], acc)
	}
	return result
}

func (h *Handler) resolveTokenRefreshRequest(r *http.Request) (adminTokenRefreshRequest, []string, error) {
	var req adminTokenRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		return req, nil, fmt.Errorf("invalid json")
	}
	req.Concurrency = normalizeTokenRefreshConcurrency(req.Concurrency)
	req.Model = normalizeModelID(strings.TrimSpace(req.Model))

		return req, nil, fmt.Errorf("no tokens provided")
}

func updateGrokUsageAccount(acc *store.Account, info *RateLimitInfo, status string) {
	if acc == nil {
		return
	}
	now := time.Now()
	if info != nil {
		limit := info.Limit
		remaining := info.Remaining
		if remaining < 0 {
			remaining = 0
		}
		if limit <= 0 && remaining > 0 {
			limit = remaining
		}
		if limit > 0 || remaining > 0 {
			acc.UsageLimit = float64(limit)
			acc.UsageCurrent = float64(remaining)
		}
		if !info.ResetAt.IsZero() {
			acc.QuotaResetAt = info.ResetAt
		}
	}
	acc.LastAttempt = now
	if strings.TrimSpace(status) != "" {
		acc.StatusCode = status
	}
}

func (h *Handler) runTokenRefreshBatch(
	ctx context.Context,
	tokens []string,
	model string,
	tokenAccounts map[string][]*store.Account,
	concurrency int,
	onItem func(token string, ok bool),
) (int, map[string]bool) {
	concurrency = normalizeTokenRefreshConcurrency(concurrency)

	var (
		mu      sync.Mutex
		okCount int
		results = make(map[string]bool, len(tokens))
	)
	sem := make(chan struct{}, concurrency)
	wg := sync.WaitGroup{}

	for _, raw := range tokens {
		token := raw
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				results[token] = false
				mu.Unlock()
				if onItem != nil {
					onItem(token, false)
				}
				return
			}
			defer func() { <-sem }()

			callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()

			info, err := h.client.GetUsage(callCtx, token, model)
			success := err == nil
			statusCode := "200"
			if !success {
				statusCode = classifyAccountStatusFromError(err.Error())
				if statusCode == "" {
					statusCode = "500"
				}
			}

			if accounts := tokenAccounts[token]; len(accounts) > 0 {
				for _, acc := range accounts {
					updateGrokUsageAccount(acc, info, statusCode)
					if updateErr := h.lb.Store.UpdateAccount(callCtx, acc); updateErr != nil {
						slog.Warn("update grok usage account failed", "account_id", acc.ID, "error", updateErr)
					}
				}
			}

			if !success {
				slog.Warn("grok token usage refresh failed", "token", maskToken(token), "error", err)
			}

			mu.Lock()
			results[token] = success
			if success {
				okCount++
			}
			mu.Unlock()

			if onItem != nil {
				onItem(token, success)
			}
		}()
	}

	wg.Wait()
	return okCount, results
}

func (h *Handler) HandleAdminTokensRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h == nil || h.lb == nil || h.lb.Store == nil {
		http.Error(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	req, tokens, err := h.resolveTokenRefreshRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	accounts, err := h.lb.Store.ListAccounts(r.Context())
	if err != nil {
		http.Error(w, "failed to list accounts", http.StatusInternalServerError)
		return
	}
	tokenAccounts := collectGrokAccountsByToken(accounts)

	_, results := h.runTokenRefreshBatch(r.Context(), tokens, req.Model, tokenAccounts, req.Concurrency, nil)
	out := map[string]interface{}{
		"status":  "success",
		"results": results,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *Handler) HandleAdminTokensRefreshAsync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h == nil || h.lb == nil || h.lb.Store == nil {
		http.Error(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	req, tokens, err := h.resolveTokenRefreshRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	accounts, err := h.lb.Store.ListAccounts(r.Context())
	if err != nil {
		http.Error(w, "failed to list accounts", http.StatusInternalServerError)
		return
	}
	tokenAccounts := collectGrokAccountsByToken(accounts)

	ctx, cancel := context.WithCancel(context.Background())
	task := newNSFWBatchTask(len(tokens), cancel)

	go func() {
		defer scheduleDeleteNSFWBatchTask(task.ID, nsfwBatchTaskTTL)
		_, _ = h.runTokenRefreshBatch(ctx, tokens, req.Model, tokenAccounts, req.Concurrency, func(token string, ok bool) {
			task.record(token, ok, ok)
		})
		if ctx.Err() != nil {
			task.finish("cancelled", "")
			return
		}
		task.finish("done", "")
	}()

	out := map[string]interface{}{
		"status":  "success",
		"task_id": task.ID,
		"total":   len(tokens),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
