package store

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestParseBoltBundleModelChoices(t *testing.T) {
	t.Parallel()

	js := `
const Xt={ClaudeCode:"claude-code",Codex:"codex",Bolt:"bolt"};
const St={
	"claude-haiku-4-5-20251001":{label:"Haiku 4.5",paidOnly:!0},
	"claude-sonnet-4-5-20250929":{label:"Sonnet 4.5",paidOnly:!1},
	"claude-sonnet-4-6":{label:"Sonnet 4.6",paidOnly:!0},
	"claude-opus-4-5-20251101":{label:"Opus 4.5",paidOnly:!0},
	"claude-opus-4-6":{label:"Opus 4.6",paidOnly:!0}
};
const LI=new Map([[Xt.ClaudeCode,[St["claude-haiku-4-5-20251001"],St["claude-sonnet-4-5-20250929"],St["claude-sonnet-4-6"],St["claude-opus-4-5-20251101"],St["claude-opus-4-6"]]]]);
`

	got, ok := parseBoltBundleModelChoices(js)
	if !ok {
		t.Fatalf("parseBoltBundleModelChoices() ok = false")
	}
	if len(got) != 5 {
		t.Fatalf("len(got) = %d, want 5", len(got))
	}
	if got[0].ID != "claude-haiku-4-5-20251001" || got[0].Name != "Haiku 4.5" {
		t.Fatalf("first model = %+v", got[0])
	}
	if got[4].ID != "claude-opus-4-6" || got[4].Name != "Opus 4.6" {
		t.Fatalf("last model = %+v", got[4])
	}
}

func TestExtractBoltAssetURLs_ResolvesRelativeImports(t *testing.T) {
	t.Parallel()

	text := `
import "./index-ABC123.js";
import{a as b}from"./components-XYZ789.js";
const asset="/assets/entry.client-ROOT.js";
`

	got := extractBoltAssetURLs(text, "https://bolt.new/assets/Chat-CX987Kmc.js")
	want := []string{
		"https://bolt.new/assets/entry.client-ROOT.js",
		"https://bolt.new/assets/index-ABC123.js",
		"https://bolt.new/assets/components-XYZ789.js",
	}

	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d; got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBuildBoltSeedModels_RefreshesBoltModelsFromBundle(t *testing.T) {
	prev := fetchBoltModelChoices
	fetchBoltModelChoices = func(ctx context.Context) ([]boltModelChoice, error) {
		return []boltModelChoice{
			{ID: "claude-haiku-4-5-20251001", Name: "Haiku 4.5"},
			{ID: "claude-sonnet-4-5-20250929", Name: "Sonnet 4.5"},
			{ID: "claude-sonnet-4-6", Name: "Sonnet 4.6"},
			{ID: "claude-opus-4-5-20251101", Name: "Opus 4.5"},
			{ID: "claude-opus-4-6", Name: "Opus 4.6"},
		}, nil
	}
	t.Cleanup(func() {
		fetchBoltModelChoices = prev
	})

	models := BuildBoltSeedModels(context.Background())
	if len(models) != 5 {
		t.Fatalf("len(models) = %d, want 5", len(models))
	}
	if !models[1].IsDefault {
		t.Fatalf("models[1].IsDefault = false, want true")
	}
	if models[0].Name != "Claude Haiku 4.5 (Bolt)" {
		t.Fatalf("models[0].Name = %q", models[0].Name)
	}
}

func TestSeedModels_UsesStaticBoltFallbackWithoutFetchingBundle(t *testing.T) {
	prev := fetchBoltModelChoices
	called := false
	fetchBoltModelChoices = func(ctx context.Context) ([]boltModelChoice, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() {
		fetchBoltModelChoices = prev
	})

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
	if called {
		t.Fatal("expected store.New() to avoid fetching bolt bundle on startup")
	}

	defaultModel, err := s.GetModelByChannelAndModelID(ctx, "bolt", "claude-sonnet-4-5-20250929")
	if err != nil {
		t.Fatalf("GetModelByChannelAndModelID(default) error = %v", err)
	}
	if !defaultModel.IsDefault {
		t.Fatalf("defaultModel.IsDefault = false, want true")
	}

	haikuModel, err := s.GetModelByChannelAndModelID(ctx, "bolt", "claude-haiku-4-5-20251001")
	if err != nil {
		t.Fatalf("GetModelByChannelAndModelID(haiku) error = %v", err)
	}
	if haikuModel.Name != "Claude Haiku 4.5 (Bolt)" {
		t.Fatalf("haikuModel.Name = %q", haikuModel.Name)
	}

	oldDefault, err := s.GetModelByChannelAndModelID(ctx, "bolt", "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("GetModelByChannelAndModelID(old default) error = %v", err)
	}
	if oldDefault.IsDefault {
		t.Fatalf("old default should have been cleared")
	}
}

func TestSeedModels_DoesNotOverwriteExistingBoltModels(t *testing.T) {
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
	model, err := s.GetModelByChannelAndModelID(ctx, "bolt", "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("GetModelByChannelAndModelID() error = %v", err)
	}

	model.Name = "Custom Bolt Name"
	model.IsDefault = true
	model.SortOrder = 99
	if err := s.UpdateModel(ctx, model); err != nil {
		t.Fatalf("UpdateModel() error = %v", err)
	}

	if err := s.seedModels(); err != nil {
		t.Fatalf("seedModels() error = %v", err)
	}

	reloaded, err := s.GetModelByChannelAndModelID(ctx, "bolt", "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("GetModelByChannelAndModelID(reloaded) error = %v", err)
	}
	if reloaded.Name != "Custom Bolt Name" {
		t.Fatalf("reloaded.Name = %q, want %q", reloaded.Name, "Custom Bolt Name")
	}
	if !reloaded.IsDefault {
		t.Fatal("reloaded.IsDefault = false, want true")
	}
	if reloaded.SortOrder != 99 {
		t.Fatalf("reloaded.SortOrder = %d, want 99", reloaded.SortOrder)
	}
}
