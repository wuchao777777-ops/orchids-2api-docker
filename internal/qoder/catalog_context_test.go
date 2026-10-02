package qoder

import (
	"encoding/json"
	"orchids-api/internal/testutil"
	"testing"
)

// Synthetic fixtures exercise the conservative schema, not captured wire data.
func TestCatalogContextConfigMergeAndRoundTrip(t *testing.T) {
	raw := []byte(`{"data":{"chat":[
 {"key":"k","display_name":"Model","max_input_tokens":180000,"context_config":[{"context_length":200000,"is_default":true,"opaque":{"price":1}}]},
 {"key":"k","display_name":"Model","max_input_tokens":900000,"context_config":{"context_length":1000000,"is_default":false,"future":"keep"}},
 {"key":"k","context_config":{"context_length":1000000,"is_default":false,"future":"keep"}},
 {"key":"k","enable":false,"context_config":{"context_length":2000000,"is_default":true}}
 ]}}`)
	catalog, err := parseModelList(raw)
	testutil.NoError(t, err)
	testutil.Equal(t, catalog.Len(), 1)
	for _, c := range []*Catalog{catalog, catalogFromIDs(catalogToIDs(catalog))} {
		for _, name := range []string{"k", "Model"} {
			model, err := c.Resolve(name)
			testutil.NoError(t, err)
			testutil.Equal(t, model.MaxInputTokens, 180000)
			testutil.Equal(t, len(model.ContextConfigVariants), 1)
			info := model.ContextWindowInfo()
			testutil.Falsef(t, info.DefaultInputTokens != 180000 || info.DefaultContextTokens != 200000 || info.MaxContextTokens != 1000000 || info.DefaultConflict || info.HasUnparsedConfig, "info=%+v", info)
			var tiers []map[string]json.RawMessage
			err = json.Unmarshal(model.ContextConfig, &tiers)
			testutil.Falsef(t, err != nil || string(tiers[0]["opaque"]) != "{\"price\":1}", "lost raw config: %s", model.ContextConfig)
		}
	}
	snapshot := catalogToIDs(catalog)
	testutil.Equal(t, CatalogContextWindows(snapshot)["model"], 180000)
	testutil.Equal(t, len(catalogFromIDs(snapshot).entries[0].ContextConfigVariants), 1)
}

func TestCatalogContextUnknownAndConflictingDefaults(t *testing.T) {
	for _, tc := range []struct {
		config string
		want   ContextWindowInfo
	}{
		{`{"tiers":{"200K":{"is_default":true}},"future":7}`, ContextWindowInfo{DefaultInputTokens: 180000, HasUnparsedConfig: true}},
		{`[{"context_length":200000,"is_default":true},{"context_length":400000,"is_default":true}]`, ContextWindowInfo{DefaultInputTokens: 180000, MaxContextTokens: 400000, DefaultConflict: true}},
		{`[{"context_length":400000},{"context_length":"1M"}]`, ContextWindowInfo{DefaultInputTokens: 180000, MaxContextTokens: 400000, HasUnparsedConfig: true}},
		{`null`, ContextWindowInfo{DefaultInputTokens: 180000}},
	} {
		c := newCatalog([]modelEntry{{Key: "k", MaxInputTokens: 180000, ContextConfig: json.RawMessage(tc.config)}})
		restored := catalogFromIDs(catalogToIDs(c))
		m, _ := restored.Resolve("k")
		testutil.Equal(t, compactContextConfig(m.ContextConfig), tc.config)
		testutil.Equal(t, m.ContextWindowInfo(), tc.want)
	}
}
