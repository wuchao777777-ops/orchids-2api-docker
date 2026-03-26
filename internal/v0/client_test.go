package v0

import "testing"

func TestExtractUserSessionFromCookieHeader(t *testing.T) {
	raw := "foo=bar; user_session=abc123; hello=world"
	if got := extractUserSession(raw); got != "abc123" {
		t.Fatalf("extractUserSession() = %q, want %q", got, "abc123")
	}
}

func TestNormalizeWebModelUsesV0Max(t *testing.T) {
	for _, model := range []string{"", "v0", "v0-max", "v0-1.5-md"} {
		if got := normalizeWebModel(model); got != "v0-max" {
			t.Fatalf("normalizeWebModel(%q) = %q, want %q", model, got, "v0-max")
		}
	}
}

func TestNormalizeWebModelPreservesKnownV0Variants(t *testing.T) {
	tests := map[string]string{
		"v0-auto":     "v0-auto",
		"v0 mini":     "v0-mini",
		"v0-pro":      "v0-pro",
		"v0 max fast": "v0-max-fast",
		"v0-custom":   "v0-custom",
	}
	for in, want := range tests {
		if got := normalizeWebModel(in); got != want {
			t.Fatalf("normalizeWebModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildSeedModelChoicesReturnsAllKnownV0Models(t *testing.T) {
	got := buildSeedModelChoices()
	want := []string{"v0-auto", "v0-mini", "v0-pro", "v0-max", "v0-max-fast"}
	if len(got) != len(want) {
		t.Fatalf("len(buildSeedModelChoices()) = %d, want %d", len(got), len(want))
	}
	for i, modelID := range want {
		if got[i].ID != modelID {
			t.Fatalf("buildSeedModelChoices()[%d].ID = %q, want %q", i, got[i].ID, modelID)
		}
		if got[i].Name == "" {
			t.Fatalf("buildSeedModelChoices()[%d].Name is empty", i)
		}
	}
}
