package qoder

import (
	"orchids-api/internal/testutil"
	"testing"
)

// The catalog declares max_input_tokens per model and the request path already
// forwards it. This is the same observation, made readable to the public model
// list so a client can budget its context against the real number.
func TestCatalogContextWindowsReadsStoredSnapshot(t *testing.T) {
	snapshot := CatalogSnapshot(newCatalog([]modelEntry{
		{Key: "ultimate", Name: "Ultimate", DisplayName: "Ultimate", MaxInputTokens: 1000000},
		{Key: "qfmodel", Name: "Qwen3.8-Flash", DisplayName: "Qwen3.8-Flash", MaxInputTokens: 180000},
	}))

	windows := CatalogContextWindows(snapshot)
	for _, want := range []struct {
		key  string
		want int
	}{
		{"ultimate", 1000000},
		{"qfmodel", 180000},
		// Every published spelling resolves, so a client asking by display name is
		// answered too.
		{"qwen3.8-flash", 180000},
	} {
		testutil.Equal(t, windows[want.key], want.want)
	}
}

// A row with no declared window must be skipped rather than reported as zero:
// "never observed" has to stay distinguishable from "cannot hold anything".
func TestCatalogContextWindowsSkipsRowsWithoutAWindow(t *testing.T) {
	snapshot := CatalogSnapshot(newCatalog([]modelEntry{
		{Key: "no-window", Name: "No Window"},
	}))
	windows := CatalogContextWindows(snapshot)
	testutil.Falsef(t, windows != nil, "windows = %#v, want nil", windows)
}

// A snapshot written before the richer form existed is a "<key>\t<name>" pair.
// It still resolves, it simply carries no window.
func TestCatalogContextWindowsAcceptsLegacySnapshot(t *testing.T) {
	windows := CatalogContextWindows([]string{"legacykey\tLegacy Name"})
	testutil.Falsef(t, windows != nil, "windows = %#v, want nil for a legacy row", windows)
}
