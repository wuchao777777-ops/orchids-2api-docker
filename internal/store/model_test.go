package store

import (
	"context"
	"github.com/goccy/go-json"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestModelStatus_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    ModelStatus
		enabled bool
	}{
		{name: "bool true", input: `true`, want: ModelStatusAvailable, enabled: true},
		{name: "bool false", input: `false`, want: ModelStatusOffline, enabled: false},
		{name: "available", input: `"available"`, want: ModelStatusAvailable, enabled: true},
		{name: "maintenance", input: `"maintenance"`, want: ModelStatusMaintenance, enabled: false},
		{name: "offline", input: `"offline"`, want: ModelStatusOffline, enabled: false},
		{name: "unknown", input: `"something"`, want: ModelStatusOffline, enabled: false},
		{name: "null", input: `null`, want: ModelStatusOffline, enabled: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s ModelStatus
			if err := json.Unmarshal([]byte(tt.input), &s); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if s != tt.want {
				t.Fatalf("got %q want %q", s, tt.want)
			}
			if s.Enabled() != tt.enabled {
				t.Fatalf("enabled=%v want %v", s.Enabled(), tt.enabled)
			}
		})
	}
}

func TestModelStatus_MarshalJSON(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(ModelStatusAvailable)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if string(b) != `"available"` {
		t.Fatalf("got %s want %s", string(b), `"available"`)
	}
}

func TestGetModelByChannelAndModelID_AllowsDuplicateModelIDsAcrossChannels(t *testing.T) {
	t.Parallel()

	mini := miniredis.RunT(t)
	s, err := New(Options{
		StoreMode:   "redis",
		RedisAddr:   mini.Addr(),
		RedisDB:     0,
		RedisPrefix: "test:",
	})
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		mini.Close()
	})

	ctx := context.Background()

	puterModel, err := s.GetModelByChannelAndModelID(ctx, "puter", "deepseek-v4-pro")
	if err != nil {
		t.Fatalf("GetModelByChannelAndModelID(puter) error = %v", err)
	}
	if puterModel.Channel != "Puter" {
		t.Fatalf("puter model channel = %q, want Puter", puterModel.Channel)
	}

	warpModel, err := s.GetModelByChannelAndModelID(ctx, "warp", "auto-open")
	if err != nil {
		t.Fatalf("GetModelByChannelAndModelID(warp) error = %v", err)
	}
	if warpModel.Channel != "Warp" {
		t.Fatalf("warp model channel = %q, want Warp", warpModel.Channel)
	}
	if warpModel.ID == puterModel.ID {
		t.Fatalf("expected different records across channels, got same id %q", warpModel.ID)
	}
}

func TestStoreNew_SeedsGrokImagineModels(t *testing.T) {
	t.Parallel()

	mini := miniredis.RunT(t)
	s, err := New(Options{
		StoreMode:   "redis",
		RedisAddr:   mini.Addr(),
		RedisDB:     0,
		RedisPrefix: "test:",
	})
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		mini.Close()
	})

	ctx := context.Background()
	model, err := s.GetModelByChannelAndModelID(ctx, "grok", "grok-imagine-image")
	if err != nil {
		t.Fatalf("GetModelByChannelAndModelID(grok, grok-imagine-image) error = %v", err)
	}
	if model == nil {
		t.Fatal("expected grok imagine model to be seeded")
	}
	if model.Channel != "Grok" {
		t.Fatalf("model.Channel=%q want %q", model.Channel, "Grok")
	}
	if model.Status != ModelStatusAvailable {
		t.Fatalf("model.Status=%q want %q", model.Status, ModelStatusAvailable)
	}
}

func TestStoreNew_PreservesExistingModelList(t *testing.T) {
	t.Parallel()

	mini := miniredis.RunT(t)
	opts := Options{
		StoreMode:   "redis",
		RedisAddr:   mini.Addr(),
		RedisDB:     0,
		RedisPrefix: "test:",
	}
	s, err := New(opts)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}

	ctx := context.Background()
	model, err := s.GetModelByChannelAndModelID(ctx, "puter", "deepseek-v4-pro")
	if err != nil {
		t.Fatalf("GetModelByChannelAndModelID() error = %v", err)
	}
	if err := s.DeleteModel(ctx, model.ID); err != nil {
		t.Fatalf("DeleteModel() error = %v", err)
	}
	_ = s.Close()

	s, err = New(opts)
	if err != nil {
		t.Fatalf("store.New() second error = %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		mini.Close()
	})

	if _, err := s.GetModelByChannelAndModelID(ctx, "puter", "deepseek-v4-pro"); err == nil {
		t.Fatal("expected deleted model to stay deleted after store restart")
	}
}

func TestStoreNew_PuterCleanupDoesNotRecreateDeletedModels(t *testing.T) {
	t.Parallel()

	mini := miniredis.RunT(t)
	opts := Options{
		StoreMode:   "redis",
		RedisAddr:   mini.Addr(),
		RedisDB:     0,
		RedisPrefix: "test:",
	}
	s, err := New(opts)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	ctx := context.Background()
	current, err := s.GetModelByChannelAndModelID(ctx, "puter", "claude-opus-5")
	if err != nil {
		t.Fatalf("GetModelByChannelAndModelID(current) error = %v", err)
	}
	if err := s.DeleteModel(ctx, current.ID); err != nil {
		t.Fatalf("DeleteModel(current) error = %v", err)
	}
	if err := s.CreateModel(ctx, &Model{
		Channel:  "Puter",
		ModelID:  "claude-opus-5-fast",
		Name:     "removed catalog alias",
		Status:   ModelStatusAvailable,
		Verified: true,
	}); err != nil {
		t.Fatalf("CreateModel(removed alias) error = %v", err)
	}
	_ = s.Close()

	s, err = New(opts)
	if err != nil {
		t.Fatalf("store.New() second error = %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		mini.Close()
	})
	if _, err := s.GetModelByChannelAndModelID(ctx, "puter", "claude-opus-5"); err == nil {
		t.Fatal("deleted current Puter model was unexpectedly recreated")
	}
	if _, err := s.GetModelByChannelAndModelID(ctx, "puter", "claude-opus-5-fast"); err == nil {
		t.Fatal("catalog-missing Puter alias was not cleaned up")
	}
}

func TestStoreNew_SeedsCurrentGrokModelsWithoutConsoleLegacyModels(t *testing.T) {
	t.Parallel()

	mini := miniredis.RunT(t)
	opts := Options{
		StoreMode:   "redis",
		RedisAddr:   mini.Addr(),
		RedisDB:     0,
		RedisPrefix: "test:",
	}
	s, err := New(opts)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}

	ctx := context.Background()
	for _, id := range []string{"grok-4.5", "grok-imagine-image-quality"} {
		model, err := s.GetModelByChannelAndModelID(ctx, "grok", id)
		if err != nil {
			t.Fatalf("GetModelByChannelAndModelID(%s) error = %v", id, err)
		}
		if err := s.DeleteModel(ctx, model.ID); err != nil {
			t.Fatalf("DeleteModel(%s) error = %v", id, err)
		}
	}
	if err := s.CreateModel(ctx, &Model{
		Channel:  "Grok",
		ModelID:  "grok-user-custom",
		Name:     "User Custom",
		Status:   ModelStatusAvailable,
		Verified: true,
	}); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}
	_ = s.Close()

	s, err = New(opts)
	if err != nil {
		t.Fatalf("store.New() second error = %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		mini.Close()
	})

	for _, id := range []string{"grok-4.5", "grok-4.6", "grok-imagine-image-quality"} {
		if _, err := s.GetModelByChannelAndModelID(ctx, "grok", id); err != nil {
			t.Fatalf("expected current model %s to be seeded after restart: %v", id, err)
		}
	}
	for _, id := range []string{"grok-4.3", "grok-4.3-beta", "grok-imagine-image-pro"} {
		if _, err := s.GetModelByChannelAndModelID(ctx, "grok", id); err == nil {
			t.Fatalf("expected console legacy model %s to stay removed", id)
		}
	}
	if _, err := s.GetModelByChannelAndModelID(ctx, "grok", "grok-user-custom"); err != nil {
		t.Fatalf("expected user custom model to remain: %v", err)
	}
}
