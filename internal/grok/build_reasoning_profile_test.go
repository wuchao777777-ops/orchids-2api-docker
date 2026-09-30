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

	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/modelcatalog"
	"orchids-api/internal/store"
)

func TestBuildChatAppliesSelectedAccountReasoningProfile(t *testing.T) {
	for _, tc := range []struct {
		name, requested, expected string
		status                    int
	}{
		{"unsupported none", "none", "", http.StatusBadRequest},
		{"catalog default", "", "high", http.StatusOK},
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
	if reasoning["effort"] != "high" {
		t.Fatalf("effort=%v", reasoning["effort"])
	}
	_, err = buildPayloadForAccount(map[string]interface{}{"reasoning": map[string]interface{}{"effort": "none"}}, acc, "grok-4.7")
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
