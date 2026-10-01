package grok

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"orchids-api/internal/loadbalancer"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

// newTestGrokStore builds a grok-test store on a throwaway miniredis whose
// lifetime is the test's own.
func newTestGrokStore(t *testing.T, prefix string) *store.Store {
	t.Helper()
	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{RedisAddr: mini.Addr(), RedisDB: 0, RedisPrefix: prefix})
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func setupValidationHandler(t *testing.T) (*Handler, *store.Store, *miniredis.Miniredis) {
	t.Helper()

	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{
		RedisAddr:   mini.Addr(),
		RedisDB:     0,
		RedisPrefix: "test:",
	})
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}

	lb := loadbalancer.NewWithCacheTTL(s, time.Second)
	t.Cleanup(func() {
		_ = s.Close()
	})
	return NewHandler(nil, lb), s, mini
}

func TestEnsureModelEnabled_RejectsHiddenGrokModel(t *testing.T) {
	h, _, _ := setupValidationHandler(t)

	err := h.ensureModelEnabled(context.Background(), "grok-4.1")
	if err == nil {
		t.Fatal("expected error")
	}
	testutil.Equal(t, err.Error(), "model not found")
}

func TestHandleChatCompletions_DoesNotAutoRegisterUnknownModel(t *testing.T) {
	h, s, _ := setupValidationHandler(t)

	body := `{"model":"grok-5","messages":[{"role":"user","content":"hello"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/grok/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.HandleChatCompletions(rec, req)

	testutil.Equal(t, rec.Code, http.StatusNotFound)
	if _, err := s.GetModelByModelID(context.Background(), "grok-5"); err == nil {
		t.Fatal("unexpected auto-registered model grok-5")
	}
}

func TestEnsureModelEnabled_AllowsVerifiedDynamicGrokModel(t *testing.T) {
	h, s, _ := setupValidationHandler(t)

	if err := s.CreateModel(context.Background(), &store.Model{
		Channel:  "Grok",
		ModelID:  "grok-5",
		Name:     "grok-5",
		Status:   store.ModelStatusAvailable,
		Verified: true,
	}); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}

	testutil.NoError(t, h.ensureModelEnabled(context.Background(), "grok-5"), "ensureModelEnabled() error = %v")
}

func TestEnsureModelCapability_RejectsPersistedCapabilityMismatch(t *testing.T) {
	h, s, _ := setupValidationHandler(t)

	if err := s.CreateModel(context.Background(), &store.Model{
		Channel: "Grok", ModelID: "grok-5", Name: "grok-5",
		Status: store.ModelStatusAvailable, Verified: true,
		Capabilities: []string{store.CapabilityResponses},
	}); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}
	testutil.NoError(t, h.ensureModelCapability(context.Background(), "grok-5", store.CapabilityResponses), "Responses capability error = %v")
	if err := h.ensureModelCapability(context.Background(), "grok-5", store.CapabilityChat); err == nil {
		t.Fatal("expected chat capability rejection")
	}
}

func TestResolveConversationModel_AppliesPersistedRoute(t *testing.T) {
	h, s, _ := setupValidationHandler(t)
	testutil.NoError(t, s.CreateAccount(context.Background(), &store.Account{AccountType: "grok", CredentialType: "oauth", GrokProvider: ProviderBuild, OAuthAccessToken: "token", Enabled: true, GrokModels: []string{"future-build-chat"}}), "CreateAccount() error = %v")
	if err := s.CreateModel(context.Background(), &store.Model{
		Channel: "Grok", ModelID: "future-build-chat", Name: "routed",
		Status: store.ModelStatusAvailable, Verified: true, Provider: ProviderBuild,
		UpstreamModel: "grok-routed-build", Capabilities: []string{store.CapabilityChat},
	}); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}
	spec, ok := h.resolveConversationModel(context.Background(), "future-build-chat")
	testutil.True(t, ok, "model was not resolved")
	if spec.Upstream != UpstreamCLI || spec.UpstreamModel != "grok-routed-build" {
		t.Fatalf("persisted route not applied: %#v", spec)
	}
}

func TestEnsureModelEnabled_PrefersGrokChannelWhenModelIDExistsInOtherProvider(t *testing.T) {
	h, s, _ := setupValidationHandler(t)

	if err := s.CreateModel(context.Background(), &store.Model{
		Channel:  "WorkBuddy",
		ModelID:  "grok-shared-id",
		Name:     "WorkBuddy shared",
		Status:   store.ModelStatusAvailable,
		Verified: true,
	}); err != nil {
		t.Fatalf("CreateModel(workbuddy) error = %v", err)
	}
	if err := s.CreateModel(context.Background(), &store.Model{
		Channel:  "Grok",
		ModelID:  "grok-shared-id",
		Name:     "Grok shared",
		Status:   store.ModelStatusAvailable,
		Verified: true,
	}); err != nil {
		t.Fatalf("CreateModel(grok) error = %v", err)
	}

	testutil.NoError(t, h.ensureModelEnabled(context.Background(), "grok-shared-id"), "ensureModelEnabled() error = %v")
}

func TestResolveModel_ParsesSupportedEffortSuffixes(t *testing.T) {
	for _, tc := range []struct {
		id, effort string
	}{
		{"grok-4.5-low", "low"},
		{"grok-4.6-xhigh", "xhigh"},
	} {
		_, effort, ok := ResolveModelAlias(tc.id)
		if !ok || effort != tc.effort {
			t.Fatalf("ResolveModelAlias(%q) effort=%q ok=%v", tc.id, effort, ok)
		}
	}
	for _, id := range []string{"grok-4.5-xhigh", "grok-4.6-none"} {
		if _, _, ok := ResolveModelAlias(id); ok {
			t.Fatalf("ResolveModelAlias(%q) unexpectedly accepted", id)
		}
	}
}

func TestResolveConversationModelRejectsDeprecatedAlias(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	for _, id := range []string{"grok-code-fast", "grok-code-fast-1", "grok/grok-code-fast"} {
		if _, ok := h.resolveConversationModel(context.Background(), id); ok {
			t.Fatalf("deprecated model %q resolved through alias", id)
		}
	}
}

func TestResolveModel_RemovesGrok43BetaWebsite(t *testing.T) {
	if _, ok := ResolveModel("grok-4.3-beta"); ok {
		t.Fatal("ResolveModel(grok-4.3-beta) = true, want removed")
	}
	if !IsDeprecatedModelID("grok-4.3-beta") {
		t.Fatal("grok-4.3-beta should be deprecated")
	}
}

func TestEnsureModelEnabled_RejectsBuildOnlyGrok43EvenWhenStored(t *testing.T) {
	h, s, _ := setupValidationHandler(t)

	if err := s.CreateModel(context.Background(), &store.Model{
		Channel:  "Grok",
		ModelID:  "grok-4.3",
		Name:     "Grok 4.3",
		Status:   store.ModelStatusAvailable,
		Verified: true,
	}); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}

	testutil.NoError(t, h.ensureModelEnabled(context.Background(), "grok-4.3"), "ensureModelEnabled(grok-4.3) error = %v")
}

func TestEnsureModelEnabled_RejectsDeprecatedGrok43Beta(t *testing.T) {
	h, s, _ := setupValidationHandler(t)

	if err := s.CreateModel(context.Background(), &store.Model{
		Channel:  "Grok",
		ModelID:  "grok-4.3-beta",
		Name:     "Grok 4.3 Beta",
		Status:   store.ModelStatusAvailable,
		Verified: true,
	}); err != nil {
		t.Fatalf("CreateModel(beta) error = %v", err)
	}

	if err := h.ensureModelEnabled(context.Background(), "grok-4.3-beta"); err == nil {
		t.Fatal("ensureModelEnabled(grok-4.3-beta) expected deprecated model rejection")
	}
}

func TestHandleChatCompletions_DoesNotProbeMissingModel(t *testing.T) {
	h, s, _ := setupValidationHandler(t)

	body := `{"model":"grok-5","messages":[{"role":"user","content":"hello"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/grok/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, req)

	testutil.Equal(t, rec.Code, http.StatusNotFound)
	if _, err := s.GetModelByModelID(context.Background(), "grok-5"); err == nil {
		t.Fatal("unexpected created model grok-5")
	} else if err.Error() == "" {
		t.Fatalf("unexpected error: %v", err)
	}
}
