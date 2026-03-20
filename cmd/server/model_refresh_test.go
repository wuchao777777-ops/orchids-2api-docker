package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/goccy/go-json"

	"orchids-api/internal/config"
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

func TestChooseRefreshedDefaultModel_PrefersExistingDefault(t *testing.T) {
	existing := map[string]*store.Model{
		"a": {ModelID: "a", IsDefault: true},
		"b": {ModelID: "b", IsDefault: false},
	}
	verified := map[string]verifiedModel{
		"a": {discoveredModel: discoveredModel{ID: "a"}, Available: true},
		"b": {discoveredModel: discoveredModel{ID: "b"}, Available: true},
	}
	ordered := []discoveredModel{{ID: "b"}, {ID: "a"}}

	got := chooseRefreshedDefaultModel(existing, verified, ordered)
	if got != "a" {
		t.Fatalf("default=%q want %q", got, "a")
	}
}

func TestApplyModelRefresh_DeletesUnavailableVerifiedPuterModel(t *testing.T) {
	s, cleanup := setupModelRefreshStore(t)
	defer cleanup()

	ctx := context.Background()
	clearModelsForChannel(t, ctx, s, "Puter")
	record := &store.Model{
		Channel:   "Puter",
		ModelID:   "puter-unavailable-model",
		Name:      "puter-unavailable-model",
		Status:    store.ModelStatusAvailable,
		Verified:  true,
		IsDefault: true,
		SortOrder: 999,
	}
	if err := s.CreateModel(ctx, record); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}

	candidates := []discoveredModel{{ID: "puter-unavailable-model", Name: "puter-unavailable-model", SortOrder: 0}}
	verified := map[string]verifiedModel{
		"puter-unavailable-model": {
			discoveredModel: candidates[0],
			Available:       false,
		},
	}

	result, err := applyModelRefresh(ctx, s, "Puter", "test", candidates, verified)
	if err != nil {
		t.Fatalf("applyModelRefresh() error = %v", err)
	}
	if result.Deleted != 1 {
		t.Fatalf("Deleted=%d want 1", result.Deleted)
	}
	if result.Offline != 0 {
		t.Fatalf("Offline=%d want 0", result.Offline)
	}

	models, err := s.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	for _, model := range models {
		if model != nil && strings.EqualFold(model.Channel, "Puter") && model.ModelID == "puter-unavailable-model" {
			t.Fatalf("expected puter-unavailable-model to be deleted, got %+v", model)
		}
	}
}

func TestApplyModelRefresh_OfflinesUnavailableVerifiedNonPuterModel(t *testing.T) {
	s, cleanup := setupModelRefreshStore(t)
	defer cleanup()

	ctx := context.Background()
	clearModelsForChannel(t, ctx, s, "Orchids")
	record := &store.Model{
		Channel:   "Orchids",
		ModelID:   "orchids-unavailable-model",
		Name:      "orchids-unavailable-model",
		Status:    store.ModelStatusAvailable,
		Verified:  true,
		IsDefault: true,
		SortOrder: 999,
	}
	if err := s.CreateModel(ctx, record); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}

	candidates := []discoveredModel{{ID: "orchids-unavailable-model", Name: "orchids-unavailable-model", SortOrder: 0}}
	verified := map[string]verifiedModel{
		"orchids-unavailable-model": {
			discoveredModel: candidates[0],
			Available:       false,
		},
	}

	result, err := applyModelRefresh(ctx, s, "Orchids", "test", candidates, verified)
	if err != nil {
		t.Fatalf("applyModelRefresh() error = %v", err)
	}
	if result.Deleted != 0 {
		t.Fatalf("Deleted=%d want 0", result.Deleted)
	}
	if result.Offline != 1 {
		t.Fatalf("Offline=%d want 1", result.Offline)
	}

	model, err := s.GetModelByChannelAndModelID(ctx, "Orchids", "orchids-unavailable-model")
	if err != nil {
		t.Fatalf("GetModelByChannelAndModelID() error = %v", err)
	}
	if model == nil {
		t.Fatal("expected orchids-unavailable-model to remain in store")
	}
	if model.Status != store.ModelStatusOffline {
		t.Fatalf("Status=%q want %q", model.Status, store.ModelStatusOffline)
	}
	if model.Verified {
		t.Fatal("Verified=true want false")
	}
}

func TestIsGrokModelInstantlyVerifiable(t *testing.T) {
	if !isGrokModelInstantlyVerifiable("grok-imagine-1.0") {
		t.Fatal("grok-imagine-1.0 should skip network verification")
	}
	if !isGrokModelInstantlyVerifiable("grok-imagine-1.0-video") {
		t.Fatal("grok-imagine-1.0-video should skip network verification")
	}
	if isGrokModelInstantlyVerifiable("grok-4.1-fast") {
		t.Fatal("grok-4.1-fast should still require usage verification")
	}
}

func TestGrokRefreshBudgetIsBounded(t *testing.T) {
	if got := refreshWorkersForChannel("Grok", 10); got != 4 {
		t.Fatalf("refreshWorkersForChannel(Grok)= %d want 4", got)
	}
	if got := refreshTimeoutForChannel("Grok"); got != 6*time.Second {
		t.Fatalf("refreshTimeoutForChannel(Grok)= %s want 6s", got)
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
