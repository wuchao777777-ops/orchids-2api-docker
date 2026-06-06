package warp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFetchFeatureAgentModeModelChoices_NormalizesIDsAndDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/graphql/v2" {
			t.Fatalf("path=%q want /graphql/v2", got)
		}
		if got := r.URL.Query().Get("op"); got != "GetFeatureModelChoices" {
			t.Fatalf("op=%q want GetFeatureModelChoices", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !strings.Contains(string(body), "\"operationName\":\"GetFeatureModelChoices\"") {
			t.Fatalf("request body missing operation name: %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"user": {
					"__typename": "UserOutput",
					"user": {
						"workspaces": [
							{
								"featureModelChoice": {
									"agentMode": {
										"defaultId": "gpt-5.1-medium",
										"choices": [
											{"id": "claude-4.6-sonnet", "displayName": "Claude 4.6 Sonnet"},
											{"id": "gpt-5.1-medium", "displayName": "GPT 5.1 Medium"}
										]
									}
								}
							}
						]
					}
				}
			}
		}`))
	}))
	defer server.Close()

	choices, defaultID, err := fetchFeatureAgentModeModelChoices(context.Background(), warpRewriteClient(t, server.URL), "jwt")
	if err != nil {
		t.Fatalf("fetchFeatureAgentModeModelChoices() error: %v", err)
	}
	if defaultID != "gpt-5.1-medium" {
		t.Fatalf("defaultID=%q want gpt-5.1-medium", defaultID)
	}
	gotIDs := []string{choices[0].ID, choices[1].ID}
	wantIDs := []string{"claude-4.6-sonnet", "gpt-5.1-medium"}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("choice ids=%+v want %+v", gotIDs, wantIDs)
	}
}

func TestFetchDiscoveredModelChoices_UsesFeatureAgentMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("op") {
		case "GetFeatureModelChoices":
			_, _ = w.Write([]byte(`{
				"data": {
					"user": {
						"__typename": "UserOutput",
						"user": {
							"workspaces": [
								{
									"featureModelChoice": {
										"agentMode": {
											"defaultId": "auto",
											"choices": [
												{"id": "auto", "displayName": "Auto"},
												{"id": "gpt-5.2-medium", "displayName": "GPT 5.2 Medium"}
											]
										}
									}
								}
							]
						}
					}
				}
			}`))
		default:
			t.Fatalf("unexpected op=%q", r.URL.Query().Get("op"))
		}
	}))
	defer server.Close()

	client := &Client{
		authClient: warpRewriteClient(t, server.URL),
		session: &session{
			jwt:       "jwt",
			expiresAt: time.Now().Add(time.Hour),
		},
	}

	choices, source, err := client.FetchDiscoveredModelChoices(context.Background())
	if err != nil {
		t.Fatalf("FetchDiscoveredModelChoices() error: %v", err)
	}
	if source != "feature_model_choice_agent_mode" {
		t.Fatalf("source=%q want feature_model_choice_agent_mode", source)
	}
	gotIDs := []string{choices[0].ID, choices[1].ID}
	wantIDs := []string{"auto", "gpt-5.2-medium"}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("choice ids=%+v want %+v", gotIDs, wantIDs)
	}
}

func TestMergeWarpModelChoices_DedupesAndMovesDefaultFirst(t *testing.T) {
	merged := mergeWarpModelChoices(
		"gpt-5.1-medium",
		[]ModelChoice{
			{ID: "claude-4.6-sonnet", Name: "Claude 4.6 Sonnet"},
			{ID: "gpt-5.1-medium", Name: "GPT 5.1 Medium"},
		},
		[]ModelChoice{
			{ID: "gpt-5-1-medium", Name: "GPT 5.1 Medium"},
			{ID: "auto", Name: "Auto"},
		},
	)

	gotIDs := make([]string, 0, len(merged))
	for _, choice := range merged {
		gotIDs = append(gotIDs, choice.ID)
	}
	wantIDs := []string{"gpt-5.1-medium", "claude-4.6-sonnet", "gpt-5-1-medium", "auto"}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("merged ids=%+v want %+v", gotIDs, wantIDs)
	}
}

func warpRewriteClient(t *testing.T, targetURL string) *http.Client {
	t.Helper()

	target, err := url.Parse(targetURL)
	if err != nil {
		t.Fatalf("parse target url: %v", err)
	}
	base := http.DefaultTransport
	return &http.Client{
		Transport: modelChoiceRoundTripper(func(req *http.Request) (*http.Response, error) {
			clone := req.Clone(req.Context())
			clone.URL.Scheme = target.Scheme
			clone.URL.Host = target.Host
			clone.Host = target.Host
			return base.RoundTrip(clone)
		}),
	}
}

type modelChoiceRoundTripper func(*http.Request) (*http.Response, error)

func (f modelChoiceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
