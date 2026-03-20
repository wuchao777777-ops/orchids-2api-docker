package main

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/bolt"
	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/grok"
	"orchids-api/internal/orchids"
	"orchids-api/internal/prompt"
	"orchids-api/internal/puter"
	"orchids-api/internal/store"
	"orchids-api/internal/upstream"
	"orchids-api/internal/util"
	"orchids-api/internal/warp"
)

type modelRefreshRequest struct {
	Channel string `json:"channel"`
}

type modelRefreshResult struct {
	Channel         string   `json:"channel"`
	Source          string   `json:"source"`
	Discovered      int      `json:"discovered"`
	Verified        int      `json:"verified"`
	Added           int      `json:"added"`
	Updated         int      `json:"updated"`
	Offline         int      `json:"offline"`
	Deleted         int      `json:"deleted"`
	DefaultModelID  string   `json:"default_model_id,omitempty"`
	AddedModelIDs   []string `json:"added_model_ids,omitempty"`
	DeletedModelIDs []string `json:"deleted_model_ids,omitempty"`
	OfflineModelIDs []string `json:"offline_model_ids,omitempty"`
}

type discoveredModel struct {
	ID        string
	Name      string
	SortOrder int
}

type verifiedModel struct {
	discoveredModel
	Available bool
}

var runModelRefresh = syncModelsForChannel

func makeModelRefreshHandler(cfg *config.Config, s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		channel := strings.TrimSpace(r.URL.Query().Get("channel"))
		if r.Body != nil {
			defer r.Body.Close()
			var req modelRefreshRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err == nil && strings.TrimSpace(req.Channel) != "" {
				channel = strings.TrimSpace(req.Channel)
			}
		}

		result, err := runModelRefresh(r.Context(), cfg, s, channel)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func syncModelsForChannel(ctx context.Context, cfg *config.Config, s *store.Store, channel string) (*modelRefreshResult, error) {
	channel = normalizeAdminModelChannel(channel)
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if s == nil {
		return nil, fmt.Errorf("store not configured")
	}

	candidates, source, err := discoverModelsForChannel(ctx, cfg, s, channel)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%s has no discoverable models", channel)
	}

	verified, err := verifyModelsForChannel(ctx, cfg, s, channel, candidates)
	if err != nil {
		return nil, err
	}

	return applyModelRefresh(ctx, s, channel, source, candidates, verified)
}

func normalizeAdminModelChannel(channel string) string {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "orchids":
		return "Orchids"
	case "warp":
		return "Warp"
	case "bolt":
		return "Bolt"
	case "puter":
		return "Puter"
	case "grok":
		return "Grok"
	default:
		return ""
	}
}

func discoverModelsForChannel(ctx context.Context, cfg *config.Config, s *store.Store, channel string) ([]discoveredModel, string, error) {
	switch strings.ToLower(channel) {
	case "orchids":
		items, source, err := fetchOrchidsModelChoices(ctx, cfg, s)
		if err != nil && len(items) == 0 {
			return nil, "", err
		}
		out := make([]discoveredModel, 0, len(items))
		for i, item := range items {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				continue
			}
			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = id
			}
			out = append(out, discoveredModel{ID: id, Name: name, SortOrder: i})
		}
		return out, source, nil
	case "warp":
		return discoverWarpModels(ctx, cfg, s)
	case "bolt":
		items := store.BuildBoltSeedModels(ctx)
		out := make([]discoveredModel, 0, len(items))
		for i, item := range items {
			out = append(out, discoveredModel{
				ID:        strings.TrimSpace(item.ModelID),
				Name:      strings.TrimSpace(item.Name),
				SortOrder: i,
			})
		}
		return out, "bolt_bundle", nil
	case "puter":
		proxyFunc := http.ProxyFromEnvironment
		if cfg != nil {
			proxyFunc = util.ProxyFunc(cfg.ProxyHTTP, cfg.ProxyHTTPS, cfg.ProxyUser, cfg.ProxyPass, cfg.ProxyBypass)
		}
		items, err := fetchPuterPublicModelChoices(ctx, proxyFunc)
		if err != nil {
			return nil, "", err
		}
		out := make([]discoveredModel, 0, len(items))
		for i, item := range items {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				continue
			}
			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = id
			}
			out = append(out, discoveredModel{ID: id, Name: name, SortOrder: i})
		}
		return out, "puter_public_models", nil
	case "grok":
		existing, err := s.ListModels(ctx)
		if err != nil {
			return nil, "", err
		}
		seen := map[string]struct{}{}
		out := make([]discoveredModel, 0, len(grok.SupportedModels)+8)
		appendCandidate := func(id, name string) {
			id = strings.TrimSpace(id)
			if id == "" {
				return
			}
			if _, ok := seen[id]; ok {
				return
			}
			seen[id] = struct{}{}
			if strings.TrimSpace(name) == "" {
				name = id
			}
			out = append(out, discoveredModel{ID: id, Name: name, SortOrder: len(out)})
		}
		for _, spec := range grok.SupportedModels {
			if spec.IsImage || spec.IsVideo {
				continue
			}
			appendCandidate(spec.ID, spec.Name)
		}
		for _, model := range existing {
			if model == nil || !strings.EqualFold(strings.TrimSpace(model.Channel), "grok") {
				continue
			}
			appendCandidate(model.ModelID, model.Name)
		}
		for _, probe := range buildGrokVersionProbes(existing) {
			appendCandidate(probe, probe)
		}
		source := "grok_supported_models"
		if publicIDs, fetchErr := fetchPublicGrokModelIDs(ctx); fetchErr == nil {
			for _, id := range publicIDs {
				appendCandidate(id, id)
			}
			source = "grok_supported_models+public_docs"
		}
		return out, source, nil
	default:
		return nil, "", fmt.Errorf("unsupported channel: %s", channel)
	}
}

func discoverWarpModels(ctx context.Context, cfg *config.Config, s *store.Store) ([]discoveredModel, string, error) {
	if s == nil {
		return warpSeedDiscoveredModels(), "warp_static_catalog", nil
	}

	accounts, err := enabledAccountsByType(ctx, s, "warp")
	if err != nil {
		return nil, "", err
	}

	seen := map[string]struct{}{}
	out := make([]discoveredModel, 0, 24)
	sourceSet := map[string]struct{}{}
	appendChoice := func(choice warp.ModelChoice) {
		id := strings.TrimSpace(choice.ID)
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		name := firstNonEmpty(choice.Name, id)
		out = append(out, discoveredModel{
			ID:        id,
			Name:      name,
			SortOrder: len(out),
		})
	}

	for _, acc := range accounts {
		client := warp.NewFromAccount(acc, cfg)
		choices, source, discoverErr := client.FetchDiscoveredModelChoices(ctx)
		client.Close()
		if discoverErr != nil {
			continue
		}
		for _, part := range strings.Split(source, "+") {
			part = strings.TrimSpace(part)
			if part != "" {
				sourceSet[part] = struct{}{}
			}
		}
		for _, choice := range choices {
			appendChoice(choice)
		}
	}

	if len(out) > 0 {
		return out, joinWarpDiscoverySources(sourceSet), nil
	}
	return warpSeedDiscoveredModels(), "warp_static_catalog_fallback", nil
}

func warpSeedDiscoveredModels() []discoveredModel {
	items := store.BuildWarpSeedModels()
	out := make([]discoveredModel, 0, len(items))
	for i, item := range items {
		out = append(out, discoveredModel{
			ID:        strings.TrimSpace(item.ModelID),
			Name:      strings.TrimSpace(item.Name),
			SortOrder: i,
		})
	}
	return out
}

func joinWarpDiscoverySources(sourceSet map[string]struct{}) string {
	if len(sourceSet) == 0 {
		return "warp_graphql"
	}
	ordered := make([]string, 0, 2)
	for _, part := range []string{"agent_mode_llms", "workspace_available_llms"} {
		if _, ok := sourceSet[part]; ok {
			ordered = append(ordered, part)
		}
	}
	if len(ordered) == 0 {
		for part := range sourceSet {
			ordered = append(ordered, part)
		}
		sort.Strings(ordered)
	}
	return "warp_graphql_" + strings.Join(ordered, "+")
}

func verifyModelsForChannel(ctx context.Context, cfg *config.Config, s *store.Store, channel string, candidates []discoveredModel) (map[string]verifiedModel, error) {
	verified := make(map[string]verifiedModel, len(candidates))
	accountType := strings.ToLower(channel)
	accounts, err := enabledAccountsByType(ctx, s, accountType)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("%s has no enabled accounts for verification", channel)
	}

	workers := refreshWorkersForChannel(channel, len(candidates))
	type jobResult struct {
		model discoveredModel
		err   error
	}

	jobs := make(chan discoveredModel)
	results := make(chan jobResult, len(candidates))
	var wg sync.WaitGroup

	verifyOne := func(ctx context.Context, model discoveredModel) error {
		switch accountType {
		case "orchids":
			for _, acc := range accounts {
				client := orchids.NewFromAccount(acc, cfg)
				err := verifyTextModel(ctx, client, model.ID)
				client.Close()
				if err == nil {
					return nil
				}
			}
			return fmt.Errorf("all orchids accounts rejected model %s", model.ID)
		case "warp":
			for _, acc := range accounts {
				client := warp.NewFromAccount(acc, cfg)
				err := verifyTextModel(ctx, client, model.ID)
				client.Close()
				if err == nil {
					return nil
				}
			}
			return fmt.Errorf("all warp accounts rejected model %s", model.ID)
		case "bolt":
			for _, acc := range accounts {
				client := bolt.NewFromAccount(acc, cfg)
				err := verifyTextModel(ctx, client, model.ID)
				client.Close()
				if err == nil {
					return nil
				}
			}
			return fmt.Errorf("all bolt accounts rejected model %s", model.ID)
		case "puter":
			for _, acc := range accounts {
				client := puter.NewFromAccount(acc, cfg)
				err := verifyTextModel(ctx, client, model.ID)
				client.Close()
				if err == nil {
					return nil
				}
			}
			return fmt.Errorf("all puter accounts rejected model %s", model.ID)
		case "grok":
			if isGrokModelInstantlyVerifiable(model.ID) {
				return nil
			}
			client := grok.New(cfg)
			for _, acc := range accounts {
				token := grok.NormalizeSSOToken(acc.ClientCookie)
				if token == "" {
					token = grok.NormalizeSSOToken(acc.RefreshToken)
				}
				if token == "" {
					continue
				}
				if _, err := client.VerifyToken(ctx, token, model.ID); err == nil {
					return nil
				}
			}
			return fmt.Errorf("all grok accounts rejected model %s", model.ID)
		default:
			return fmt.Errorf("unsupported channel: %s", channel)
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for model := range jobs {
				verifyCtx, cancel := context.WithTimeout(ctx, refreshTimeoutForChannel(channel))
				err := verifyOne(verifyCtx, model)
				cancel()
				results <- jobResult{model: model, err: err}
				if strings.EqualFold(channel, "grok") {
					_ = util.SleepWithContext(ctx, 75*time.Millisecond)
				}
			}
		}()
	}

	go func() {
		for _, model := range candidates {
			select {
			case <-ctx.Done():
				close(jobs)
				return
			case jobs <- model:
			}
		}
		close(jobs)
	}()

	wg.Wait()
	close(results)

	for result := range results {
		verified[result.model.ID] = verifiedModel{
			discoveredModel: result.model,
			Available:       result.err == nil,
		}
	}
	if len(verified) != len(candidates) {
		return nil, fmt.Errorf("model verification interrupted")
	}

	return verified, nil
}

func enabledAccountsByType(ctx context.Context, s *store.Store, accountType string) ([]*store.Account, error) {
	accounts, err := s.GetEnabledAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*store.Account, 0, len(accounts))
	for _, acc := range accounts {
		if acc == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(acc.AccountType), accountType) {
			out = append(out, acc)
		}
	}
	return out, nil
}

func refreshWorkersForChannel(channel string, count int) int {
	if count <= 1 {
		return 1
	}
	switch strings.ToLower(channel) {
	case "puter":
		if count < 6 {
			return count
		}
		return 6
	case "grok":
		if count < 4 {
			return count
		}
		return 4
	default:
		if count < 4 {
			return count
		}
		return 4
	}
}

func refreshTimeoutForChannel(channel string) time.Duration {
	switch strings.ToLower(channel) {
	case "puter":
		return 25 * time.Second
	case "grok":
		return 6 * time.Second
	default:
		return 30 * time.Second
	}
}

func isGrokModelInstantlyVerifiable(modelID string) bool {
	spec, ok := grok.ResolveModel(modelID)
	if !ok {
		return false
	}
	return spec.IsImage || spec.IsVideo
}

func verifyTextModel(ctx context.Context, client interface {
	SendRequestWithPayload(context.Context, upstream.UpstreamRequest, func(upstream.SSEMessage), *debug.Logger) error
}, modelID string) error {
	// The concrete logger type is irrelevant here, so pass nil.
	return client.SendRequestWithPayload(ctx, upstream.UpstreamRequest{
		Model: modelID,
		Messages: []prompt.Message{
			{
				Role: "user",
				Content: prompt.MessageContent{
					Text: "Reply with exactly OK.",
				},
			},
		},
		NoTools: true,
	}, nil, nil)
}

func applyModelRefresh(ctx context.Context, s *store.Store, channel string, source string, candidates []discoveredModel, verified map[string]verifiedModel) (*modelRefreshResult, error) {
	existingModels, err := s.ListModels(ctx)
	if err != nil {
		return nil, err
	}

	result := &modelRefreshResult{
		Channel:    channel,
		Source:     source,
		Discovered: len(candidates),
	}

	existingByID := make(map[string]*store.Model)
	fetchedSet := make(map[string]discoveredModel, len(candidates))
	verifiedIDs := make([]string, 0, len(candidates))
	for _, model := range candidates {
		fetchedSet[model.ID] = model
		if verified[model.ID].Available {
			verifiedIDs = append(verifiedIDs, model.ID)
		}
	}
	result.Verified = len(verifiedIDs)

	for _, model := range existingModels {
		if model == nil || !strings.EqualFold(strings.TrimSpace(model.Channel), channel) {
			continue
		}
		existingByID[model.ModelID] = model
	}

	defaultModelID := chooseRefreshedDefaultModel(existingByID, verified, candidates)
	result.DefaultModelID = defaultModelID

	for _, model := range candidates {
		entry := verified[model.ID]
		existing := existingByID[model.ID]
		if existing == nil {
			if !entry.Available {
				continue
			}
			record := &store.Model{
				Channel:   channel,
				ModelID:   model.ID,
				Name:      firstNonEmpty(model.Name, model.ID),
				Status:    store.ModelStatusAvailable,
				Verified:  true,
				IsDefault: model.ID == defaultModelID,
				SortOrder: model.SortOrder,
			}
			if err := s.CreateModel(ctx, record); err != nil {
				return nil, err
			}
			result.Added++
			result.AddedModelIDs = append(result.AddedModelIDs, model.ID)
			continue
		}

		if shouldDeleteUnavailableVerifiedModel(channel, entry) {
			if err := s.DeleteModel(ctx, existing.ID); err != nil {
				return nil, err
			}
			delete(existingByID, model.ID)
			result.Deleted++
			result.DeletedModelIDs = append(result.DeletedModelIDs, model.ID)
			continue
		}

		desiredStatus := store.ModelStatusOffline
		desiredVerified := false
		if entry.Available {
			desiredStatus = store.ModelStatusAvailable
			desiredVerified = true
		}

		needsUpdate := false
		if !strings.EqualFold(existing.Channel, channel) {
			existing.Channel = channel
			needsUpdate = true
		}
		if existing.Name != firstNonEmpty(model.Name, model.ID) {
			existing.Name = firstNonEmpty(model.Name, model.ID)
			needsUpdate = true
		}
		if existing.SortOrder != model.SortOrder {
			existing.SortOrder = model.SortOrder
			needsUpdate = true
		}
		if existing.Status != desiredStatus {
			existing.Status = desiredStatus
			needsUpdate = true
			if desiredStatus == store.ModelStatusOffline {
				result.Offline++
				result.OfflineModelIDs = append(result.OfflineModelIDs, model.ID)
			}
		}
		if existing.Verified != desiredVerified {
			existing.Verified = desiredVerified
			needsUpdate = true
		}
		desiredDefault := entry.Available && model.ID == defaultModelID
		if existing.IsDefault != desiredDefault {
			existing.IsDefault = desiredDefault
			needsUpdate = true
		}
		if needsUpdate {
			if err := s.UpdateModel(ctx, existing); err != nil {
				return nil, err
			}
			result.Updated++
		}
	}

	for modelID, existing := range existingByID {
		if _, ok := fetchedSet[modelID]; ok {
			continue
		}
		if err := s.DeleteModel(ctx, existing.ID); err != nil {
			return nil, err
		}
		result.Deleted++
		result.DeletedModelIDs = append(result.DeletedModelIDs, modelID)
	}

	sort.Strings(result.AddedModelIDs)
	sort.Strings(result.DeletedModelIDs)
	sort.Strings(result.OfflineModelIDs)
	return result, nil
}

func shouldDeleteUnavailableVerifiedModel(channel string, entry verifiedModel) bool {
	if entry.Available {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "puter":
		return true
	default:
		return false
	}
}

func chooseRefreshedDefaultModel(existing map[string]*store.Model, verified map[string]verifiedModel, ordered []discoveredModel) string {
	for _, model := range ordered {
		entry := verified[model.ID]
		if !entry.Available {
			continue
		}
		if current := existing[model.ID]; current != nil && current.IsDefault {
			return model.ID
		}
	}
	for _, model := range ordered {
		if verified[model.ID].Available {
			return model.ID
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
