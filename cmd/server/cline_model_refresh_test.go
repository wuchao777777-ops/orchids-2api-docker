package main

import (
	"context"
	"testing"

	"orchids-api/internal/cline"
	"orchids-api/internal/config"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

// TestClineCatalogToDiscoveredPublishesTheFeedName pins the display name the
// feed published.
//
// The identifier is what a client asks for; the name is what a human reads.
// Publishing the identifier in both columns made the whole Cline page a list of
// slugs, which is what the upstream's own catalog is not.
func TestClineCatalogToDiscoveredPublishesTheFeedName(t *testing.T) {
	got := clineCatalogToDiscovered([]cline.Model{
		{ID: "cline-free/deepseek-v4.1-flash", Name: "Deepseek-v4.1-Flash", Provider: "cline-free"},
		{ID: "z-ai/glm-5.3-flash", Name: "glm-5.3-flash", Provider: "z-ai"},
		// A feed row with no name falls back to the identifier rather than
		// publishing an empty display name.
		{ID: "poolside/laguna-s-2.1:free", Provider: "poolside"},
		{ID: "   "},
	})
	testutil.Equal(t, len(got), 3)
	want := []struct {
		id, name, provider string
	}{
		{"cline-free/deepseek-v4.1-flash", "Deepseek-v4.1-Flash", "cline-free"},
		{"z-ai/glm-5.3-flash", "glm-5.3-flash", "z-ai"},
		{"poolside/laguna-s-2.1:free", "poolside/laguna-s-2.1:free", "poolside"},
	}
	for i, want := range want {
		testutil.CheckEqual(t, got[i].ID, want.id)
		testutil.CheckEqual(t, got[i].Name, want.name)
		testutil.CheckEqual(t, got[i].Provider, want.provider)
		// The upstream identifier is what the request path sends, so it is the
		// upstream model of every published row.
		testutil.CheckEqual(t, got[i].UpstreamModel, want.id)
		testutil.CheckFalsef(t, !got[i].Verified, "row %d Verified=false: a catalog read is an observation", i)
	}
}

// TestApplyModelRefreshWritesTheClineRouteMetadata checks that a refresh
// publishes the vendor the feed named, so the public model list can tell two
// free models of the same channel apart.
func TestApplyModelRefreshWritesTheClineRouteMetadata(t *testing.T) {
	s, cleanup := setupModelRefreshStore(t)
	defer cleanup()
	ctx := context.Background()
	clearModelsForChannel(t, ctx, s, "Cline")

	result, err := applyModelRefreshWithPrune(ctx, s, "Cline", "cline_recommended_models", clineCatalogToDiscovered([]cline.Model{
		{ID: "cline-free/deepseek-v4.1-flash", Name: "Deepseek-v4.1-Flash", Provider: "cline-free"},
		{ID: "z-ai/glm-5.3-flash", Name: "glm-5.3-flash", Provider: "z-ai"},
	}), true)
	testutil.NoError(t, err, "applyModelRefreshWithPrune() error = %v")
	testutil.Equal(t, result.Added, 2)

	row, err := s.GetModelByChannelAndModelID(ctx, "Cline", "z-ai/glm-5.3-flash")
	testutil.NoError(t, err, "GetModelByChannelAndModelID() error = %v")
	testutil.CheckEqual(t, row.Name, "glm-5.3-flash")
	testutil.CheckEqual(t, row.Provider, "z-ai")
	testutil.CheckEqual(t, row.UpstreamModel, "z-ai/glm-5.3-flash")
}

// TestApplyModelRefreshFillsInARowThatPredatesTheMetadata is the migration half:
// the five Cline rows this deployment already has were published before the feed
// named a provider, and a refresh has to complete them without touching a name
// or a provider an operator set by hand.
func TestApplyModelRefreshFillsInARowThatPredatesTheMetadata(t *testing.T) {
	s, cleanup := setupModelRefreshStore(t)
	defer cleanup()
	ctx := context.Background()
	clearModelsForChannel(t, ctx, s, "Cline")

	stored := &store.Model{
		Channel:  "Cline",
		ModelID:  "cline-free/deepseek-v4.1-flash",
		Name:     "cline-free/deepseek-v4.1-flash",
		Status:   store.ModelStatusAvailable,
		Verified: false,
	}
	testutil.NoError(t, s.CreateModel(ctx, stored), "CreateModel() error = %v")
	// A row an operator renamed must keep its name.
	renamed := &store.Model{
		Channel:  "Cline",
		ModelID:  "z-ai/glm-5.3-flash",
		Name:     "运营改过的名字",
		Status:   store.ModelStatusAvailable,
		Verified: true,
		Provider: "hand-set",
	}
	testutil.NoError(t, s.CreateModel(ctx, renamed), "CreateModel() error = %v")

	if _, err := applyModelRefreshWithPrune(ctx, s, "Cline", "cline_recommended_models", clineCatalogToDiscovered([]cline.Model{
		{ID: "cline-free/deepseek-v4.1-flash", Name: "Deepseek-v4.1-Flash", Provider: "cline-free"},
		{ID: "z-ai/glm-5.3-flash", Name: "glm-5.3-flash", Provider: "z-ai"},
	}), true); err != nil {
		t.Fatalf("applyModelRefreshWithPrune() error = %v", err)
	}

	completed, err := s.GetModelByChannelAndModelID(ctx, "Cline", "cline-free/deepseek-v4.1-flash")
	testutil.NoError(t, err, "GetModelByChannelAndModelID() error = %v")
	testutil.CheckFalse(t, !completed.Verified, "an observed row stayed unverified")
	// The name was a copy of the identifier, so it adopts the feed's name.
	testutil.CheckEqual(t, completed.Name, "Deepseek-v4.1-Flash")
	testutil.CheckEqual(t, completed.Provider, "cline-free")

	kept, err := s.GetModelByChannelAndModelID(ctx, "Cline", "z-ai/glm-5.3-flash")
	testutil.NoError(t, err, "GetModelByChannelAndModelID() error = %v")
	testutil.CheckEqual(t, kept.Name, "运营改过的名字")
	testutil.CheckEqual(t, kept.Provider, "hand-set")
}

// TestDiscoverClineModelsReportsNoAccount keeps the channel's contract: without
// an account there is no observation, and no compiled-in list may stand in.
func TestDiscoverClineModelsReportsNoAccount(t *testing.T) {
	s, cleanup := setupModelRefreshStore(t)
	defer cleanup()

	_, err := discoverAccountCatalogModels(context.Background(), &config.Config{}, s, "Cline", defaultModelRefreshConcurrency)
	testutil.False(t, err == nil, "discoverAccountCatalogModels() error = nil, want no active accounts")
	var noAccount *noActiveAccountsError
	testutil.True(t, errorsAs(err, &noAccount), "error = %v, want noActiveAccountsError")
}

func errorsAs(err error, target **noActiveAccountsError) bool {
	for err != nil {
		if typed, ok := err.(*noActiveAccountsError); ok {
			*target = typed
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		next := unwrapper.Unwrap()
		if next == err {
			return false
		}
		err = next
	}
	return false
}

// TestClineRefreshSourceIsAnUpstreamCatalog keeps the source in the set that may
// publish and prune: a refresh that reads the feed is authoritative for the
// whole channel.
func TestClineRefreshSourceIsAnUpstreamCatalog(t *testing.T) {
	testutil.False(t, !isUpstreamCatalogSource("cline_recommended_models"), "cline_recommended_models must be an upstream catalog source")
	testutil.Equal(t, normalizeAdminModelChannel("cline"), "Cline")
	testutil.CheckEqual(t, refreshModelRequestConfig(&config.Config{RequestTimeout: 600}, "cline").RequestTimeout, 15)
}
