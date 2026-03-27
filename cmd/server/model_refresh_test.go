package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/modelpolicy"
	"orchids-api/internal/store"
)

func TestMakeModelRefreshHandler_UsesBodyChannel(t *testing.T) {
	prev := runModelRefresh
	defer func() { runModelRefresh = prev }()

	runModelRefresh = func(ctx context.Context, cfg *config.Config, s *store.Store, channel string) (*modelRefreshResult, error) {
		return &modelRefreshResult{Channel: channel, Source: "stub", Discovered: 3, Verified: 2}, nil
	}

	handler := makeModelRefreshHandler(&config.Config{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/models/refresh", strings.NewReader(`{"channel":"puter"}`))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}

	var resp modelRefreshResult
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Channel != "puter" {
		t.Fatalf("channel=%q want %q", resp.Channel, "puter")
	}
	if resp.Verified != 2 {
		t.Fatalf("verified=%d want 2", resp.Verified)
	}
}

func TestSyncModelsForChannel_SkipsVerificationAndUsesDiscoveryList(t *testing.T) {
	s, cleanup := setupModelRefreshStore(t)
	defer cleanup()

	ctx := context.Background()
	clearModelsForChannel(t, ctx, s, "Bolt")

	result, err := syncModelsForChannel(ctx, &config.Config{}, s, "Bolt")
	if err != nil {
		t.Fatalf("syncModelsForChannel() error = %v", err)
	}
	if result.Discovered == 0 {
		t.Fatal("expected discovered bolt models to be non-empty")
	}
	if result.Verified != result.Discovered {
		t.Fatalf("verified=%d want discovered=%d", result.Verified, result.Discovered)
	}
}

func TestChooseRefreshedDefaultModel_PrefersExistingDefault(t *testing.T) {
	existing := map[string]*store.Model{
		"a": {ModelID: "a", IsDefault: true},
		"b": {ModelID: "b", IsDefault: false},
	}
	ordered := []discoveredModel{{ID: "b"}, {ID: "a"}}

	got := chooseRefreshedDefaultModel(existing, ordered)
	if got != "a" {
		t.Fatalf("default=%q want %q", got, "a")
	}
}

func TestDiscoverModelsForChannel_OrchidsUsesUpstreamAPI(t *testing.T) {
	s, cleanup := setupModelRefreshStore(t)
	defer cleanup()

	ctx := context.Background()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization=%q want Bearer test-token", got)
		}
		if got := r.URL.Path; got != "/v1/models" {
			t.Fatalf("path=%q want /v1/models", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"claude-sonnet-4-6"},{"id":"claude-opus-4.6"},{"id":"claude-opus-4-6"},{"id":"gpt-5.4"}]}`))
	}))
	defer upstream.Close()

	if err := s.CreateAccount(ctx, &store.Account{
		AccountType: "orchids",
		Enabled:     true,
		Token:       "test-token",
	}); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	items, source, err := discoverModelsForChannel(ctx, &config.Config{
		OrchidsAPIBaseURL: upstream.URL,
		RequestTimeout:    5,
	}, s, "Orchids")
	if err != nil {
		t.Fatalf("discoverModelsForChannel() error = %v", err)
	}
	if source != "upstream_api" {
		t.Fatalf("source=%q want %q", source, "upstream_api")
	}
	if len(items) != 3 {
		t.Fatalf("len(items)=%d want 3", len(items))
	}
	if items[0].ID != "claude-sonnet-4-6" || items[1].ID != "claude-opus-4-6" || items[2].ID != "gpt-5.4" {
		t.Fatalf("items=%+v want claude-sonnet-4-6,claude-opus-4-6,gpt-5.4", items)
	}
}

func TestDiscoverModelsForChannel_BoltReturnsSeedCatalog(t *testing.T) {
	items, source, err := discoverModelsForChannel(context.Background(), nil, nil, "Bolt")
	if err != nil {
		t.Fatalf("discoverModelsForChannel() error = %v", err)
	}
	if source != "bolt_bundle" {
		t.Fatalf("source=%q want %q", source, "bolt_bundle")
	}
	if len(items) == 0 {
		t.Fatal("expected bolt seed catalog to contain models")
	}
}

func TestDiscoverModelsForChannel_GrokUsesStableAllowlistAndVerifiedExisting(t *testing.T) {
	s, cleanup := setupModelRefreshStore(t)
	defer cleanup()

	ctx := context.Background()
	if err := s.CreateModel(ctx, &store.Model{
		Channel:   "Grok",
		ModelID:   "grok-5",
		Name:      "grok-5",
		Status:    store.ModelStatusAvailable,
		Verified:  true,
		IsDefault: false,
		SortOrder: 99,
	}); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}
	if err := s.CreateModel(ctx, &store.Model{
		Channel:   "Grok",
		ModelID:   "grok-4",
		Name:      "grok-4",
		Status:    store.ModelStatusAvailable,
		Verified:  false,
		IsDefault: false,
		SortOrder: 100,
	}); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}

	items, source, err := discoverModelsForChannel(context.Background(), nil, s, "Grok")
	if err != nil {
		t.Fatalf("discoverModelsForChannel() error = %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected grok discovery to return stable models")
	}
	if source != "grok_stable_allowlist+verified_existing" {
		t.Fatalf("source=%q want %q", source, "grok_stable_allowlist+verified_existing")
	}

	gotIDs := make([]string, 0, len(items))
	gotSet := make(map[string]struct{}, len(items))
	for _, item := range items {
		gotIDs = append(gotIDs, item.ID)
		gotSet[item.ID] = struct{}{}
	}
	for _, stableID := range modelpolicy.StableGrokTextModelIDs() {
		if _, ok := gotSet[stableID]; !ok {
			t.Fatalf("expected grok discovery to include stable model %q, got %+v", stableID, items)
		}
	}
	if _, ok := gotSet["grok-5"]; !ok {
		t.Fatalf("expected grok discovery to include verified dynamic model grok-5, got %+v", items)
	}
	if _, ok := gotSet["grok-4"]; ok {
		t.Fatalf("expected grok discovery to exclude unverified model grok-4, got %+v", items)
	}
}

func TestApplyModelRefresh_DeletesModelsMissingFromDiscoveredList(t *testing.T) {
	testCases := []struct {
		channel string
		modelID string
	}{
		{channel: "Puter", modelID: "puter-unavailable-model"},
		{channel: "Orchids", modelID: "orchids-unavailable-model"},
		{channel: "Warp", modelID: "warp-unavailable-model"},
		{channel: "Bolt", modelID: "bolt-unavailable-model"},
	}

	for _, tc := range testCases {
		t.Run(tc.channel, func(t *testing.T) {
			s, cleanup := setupModelRefreshStore(t)
			defer cleanup()

			ctx := context.Background()
			clearModelsForChannel(t, ctx, s, tc.channel)
			record := &store.Model{
				Channel:   tc.channel,
				ModelID:   tc.modelID,
				Name:      tc.modelID,
				Status:    store.ModelStatusAvailable,
				Verified:  true,
				IsDefault: true,
				SortOrder: 999,
			}
			if err := s.CreateModel(ctx, record); err != nil {
				t.Fatalf("CreateModel() error = %v", err)
			}

			result, err := applyModelRefresh(ctx, s, tc.channel, "test", nil)
			if err != nil {
				t.Fatalf("applyModelRefresh() error = %v", err)
			}
			if result.Deleted != 1 {
				t.Fatalf("Deleted=%d want 1", result.Deleted)
			}
			if len(result.DeletedModelIDs) != 1 || result.DeletedModelIDs[0] != tc.modelID {
				t.Fatalf("DeletedModelIDs=%v want [%s]", result.DeletedModelIDs, tc.modelID)
			}

			models, err := s.ListModels(ctx)
			if err != nil {
				t.Fatalf("ListModels() error = %v", err)
			}
			for _, model := range models {
				if model != nil && strings.EqualFold(model.Channel, tc.channel) && model.ModelID == tc.modelID {
					t.Fatalf("expected %s to be deleted, got %+v", tc.modelID, model)
				}
			}
		})
	}
}

func TestApplyModelRefresh_UpdatesExistingModelFromDiscoveredList(t *testing.T) {
	s, cleanup := setupModelRefreshStore(t)
	defer cleanup()

	ctx := context.Background()
	clearModelsForChannel(t, ctx, s, "Warp")
	record := &store.Model{
		Channel:   "Warp",
		ModelID:   "claude-4-5-sonnet",
		Name:      "Old Name",
		Status:    store.ModelStatusOffline,
		Verified:  false,
		IsDefault: false,
		SortOrder: 999,
	}
	if err := s.CreateModel(ctx, record); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}

	candidates := []discoveredModel{{ID: "claude-4-5-sonnet", Name: "Claude 4.5 Sonnet (Warp)", SortOrder: 0}}
	result, err := applyModelRefresh(ctx, s, "Warp", "test", candidates)
	if err != nil {
		t.Fatalf("applyModelRefresh() error = %v", err)
	}
	if result.Deleted != 0 {
		t.Fatalf("Deleted=%d want 0", result.Deleted)
	}

	model, err := s.GetModelByChannelAndModelID(ctx, "Warp", "claude-4-5-sonnet")
	if err != nil {
		t.Fatalf("GetModelByChannelAndModelID() error = %v", err)
	}
	if model == nil {
		t.Fatal("expected model to remain in store")
	}
	if model.Status != store.ModelStatusAvailable {
		t.Fatalf("Status=%q want %q", model.Status, store.ModelStatusAvailable)
	}
	if !model.Verified {
		t.Fatal("Verified=false want true")
	}
	if model.Name != "Claude 4.5 Sonnet (Warp)" {
		t.Fatalf("Name=%q want %q", model.Name, "Claude 4.5 Sonnet (Warp)")
	}
	if model.SortOrder != 0 {
		t.Fatalf("SortOrder=%d want 0", model.SortOrder)
	}
}

func TestRefreshModelRequestConfig_ClampsOrchidsTimeout(t *testing.T) {
	cfg := refreshModelRequestConfig(&config.Config{RequestTimeout: 30}, "Orchids")
	if cfg.RequestTimeout != 10 {
		t.Fatalf("RequestTimeout=%d want 10", cfg.RequestTimeout)
	}
}

func setupModelRefreshStore(t *testing.T) (*store.Store, func()) {
	t.Helper()

	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{
		StoreMode:   "redis",
		RedisAddr:   mini.Addr(),
		RedisPrefix: "model_refresh_test:",
	})
	if err != nil {
		mini.Close()
		t.Fatalf("store.New() error = %v", err)
	}

	return s, func() {
		_ = s.Close()
		mini.Close()
	}
}

func clearModelsForChannel(t *testing.T, ctx context.Context, s *store.Store, channel string) {
	t.Helper()

	models, err := s.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	for _, model := range models {
		if model == nil || !strings.EqualFold(model.Channel, channel) {
			continue
		}
		if err := s.DeleteModel(ctx, model.ID); err != nil {
			t.Fatalf("DeleteModel(%q) error = %v", model.ID, err)
		}
	}
}
