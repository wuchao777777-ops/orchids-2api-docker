package grok

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"encoding/json"

	"orchids-api/internal/config"
	"orchids-api/internal/modelcatalog"
	"orchids-api/internal/store"
)

func TestBuildChatAppliesSelectedAccountReasoningProfile(t *testing.T) {
	for _, tc := range []struct {
		name, requested, expected string
		status                    int
	}{
		{"explicit none uses lowest supported effort", "none", "low", http.StatusOK},
		{"unsupported effort", "ultra", "", http.StatusBadRequest},
		{"safe default", "", "low", http.StatusOK},
		{"explicit low", "low", "low", http.StatusOK},
		{"max alias", "max", "xhigh", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if got := payload["reasoning"].(map[string]interface{})["effort"]; got != tc.expected {
					t.Errorf("effort=%v want=%s", got, tc.expected)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"resp_profile","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}`))
			}))
			defer upstream.Close()
			h, s, mini := setupValidationHandler(t)
			defer s.Close()
			defer mini.Close()
			model := "grok-4.7"
			if err := s.CreateModel(context.Background(), &store.Model{Channel: "Grok", ModelID: model, Name: model, Status: store.ModelStatusAvailable, Verified: true}); err != nil {
				t.Fatal(err)
			}
			acc := &store.Account{AccountType: "grok", GrokProvider: ProviderBuild, CredentialType: "oauth", Enabled: true, OAuthAccessToken: jwtWithClaims(t, `{"sub":"profile-user","team_id":"profile-team"}`), OAuthExpiresAt: time.Now().Add(time.Hour), GrokModels: []string{model}, GrokModelsSyncedAt: time.Now(), GrokModelCatalog: []modelcatalog.Profile{{ModelID: model, ReasoningEfforts: []string{"xhigh", "high", "medium", "low"}, DefaultReasoningEffort: "high", SupportsReasoningEffort: true}}}
			if err := s.CreateAccount(context.Background(), acc); err != nil {
				t.Fatal(err)
			}
			h.cfg = &config.Config{GrokCLIBaseURL: upstream.URL + "/v1"}
			h.cliClient = NewCLIClient(h.cfg)
			h.cliClient.httpClient = upstream.Client()
			h.cliClient.oauth.httpClient = upstream.Client()
			req := ChatCompletionsRequest{Model: model, Messages: []ChatMessage{{Role: "user", Content: "hello"}}}
			if tc.requested != "" {
				req.ReasoningEffort = &tc.requested
			}
			body, _ := json.Marshal(req)
			rec := httptest.NewRecorder()
			h.HandleChatCompletions(rec, httptest.NewRequest(http.MethodPost, "/grok/v1/chat/completions", bytes.NewReader(body)))
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tc.status == http.StatusBadRequest {
				if calls != 0 || !strings.Contains(rec.Body.String(), "supported values: xhigh, high, medium, low") {
					t.Fatalf("calls=%d body=%s", calls, rec.Body.String())
				}
			} else if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestBuildPayloadForAccountUsesCatalogDefaultAndValidation(t *testing.T) {
	acc := &store.Account{GrokModelCatalog: []modelcatalog.Profile{{ModelID: "grok-4.7", ReasoningEfforts: []string{"xhigh", "high", "medium", "low"}, DefaultReasoningEffort: "high", SupportsReasoningEffort: true}}}
	payload, err := buildPayloadForAccount(map[string]interface{}{"reasoning": map[string]interface{}{"summary": "auto"}}, acc, "grok-4.7")
	if err != nil {
		t.Fatal(err)
	}
	reasoning := payload["reasoning"].(map[string]interface{})
	if reasoning["effort"] != "low" {
		t.Fatalf("effort=%v", reasoning["effort"])
	}
	payload, err = buildPayloadForAccount(map[string]interface{}{"reasoning": map[string]interface{}{"effort": "none"}}, acc, "grok-4.7")
	if err != nil || payload["reasoning"].(map[string]interface{})["effort"] != "low" {
		t.Fatalf("none compatibility payload=%v err=%v", payload, err)
	}
	_, err = buildPayloadForAccount(map[string]interface{}{"reasoning": map[string]interface{}{"effort": "ultra"}}, acc, "grok-4.7")
	var profileErr *buildReasoningProfileError
	if !errors.As(err, &profileErr) {
		t.Fatalf("err=%v", err)
	}
	payload, err = buildPayloadForAccount(map[string]interface{}{"reasoning": map[string]interface{}{"effort": "max"}}, acc, "grok-4.7")
	if err != nil {
		t.Fatal(err)
	}
	if payload["reasoning"].(map[string]interface{})["effort"] != "xhigh" {
		t.Fatalf("payload=%v", payload)
	}
}

func TestBuildPayloadForAccountDoesNotMutateImmutableSource(t *testing.T) {
	source := map[string]interface{}{"reasoning": map[string]interface{}{"effort": "max"}}
	acc := &store.Account{GrokModelCatalog: []modelcatalog.Profile{{ModelID: "grok-4.7", ReasoningEfforts: []string{"xhigh"}, SupportsReasoningEffort: true}}}
	if _, err := buildPayloadForAccount(source, acc, "grok-4.7"); err != nil {
		t.Fatal(err)
	}
	if source["reasoning"].(map[string]interface{})["effort"] != "max" {
		t.Fatalf("source mutated: %v", source)
	}
}

func TestBuildExplicitNoneUsesLowestCompatibleEffort(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile *modelcatalog.Profile
		want    string
	}{
		{"catalog chooses supported low", &modelcatalog.Profile{ModelID: "grok-4.7", SupportsReasoningEffort: true, ReasoningEfforts: []string{"high", "low"}}, "low"},
		{"missing catalog uses static low", nil, "low"},
		{"no effort capability rejects explicit none", &modelcatalog.Profile{ModelID: "grok-4.7"}, "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := map[string]interface{}{"reasoning": map[string]interface{}{"effort": " NONE ", "summary": "auto"}}
			acc := &store.Account{}
			if tc.profile != nil {
				acc.GrokModelCatalog = []modelcatalog.Profile{*tc.profile}
			}
			payload, err := buildPayloadForAccount(source, acc, "grok-4.7")
			if tc.want == "error" {
				var profileErr *buildReasoningProfileError
				if !errors.As(err, &profileErr) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			reasoning := payload["reasoning"].(map[string]interface{})
			if got := interfaceString(reasoning["effort"]); !strings.EqualFold(got, tc.want) {
				t.Fatalf("effort=%q want=%q", got, tc.want)
			}
			if reasoning["summary"] != "auto" || source["reasoning"].(map[string]interface{})["effort"] != " NONE " {
				t.Fatal("summary or immutable source changed")
			}
		})
	}
}

func TestBuildMissingCatalogDefaultsKnownReasoningModelToLow(t *testing.T) {
	payload, err := buildPayloadForAccount(map[string]interface{}{}, &store.Account{}, "grok-4.7")
	if err != nil {
		t.Fatal(err)
	}
	if got := payload["reasoning"].(map[string]interface{})["effort"]; got != "low" {
		t.Fatalf("effort=%v", got)
	}
	payload, err = buildPayloadForAccount(map[string]interface{}{}, &store.Account{}, "unknown-model")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["reasoning"]; exists {
		t.Fatalf("unexpected reasoning=%v", payload["reasoning"])
	}
}
