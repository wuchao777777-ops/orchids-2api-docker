package grok

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
)

func TestHandleResponses_ProxiesBuildOAuthNatively(t *testing.T) {
	var received map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Fatalf("upstream request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-ID", "native-response-test")
		_, _ = io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\"}\n\n")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
	}))
	defer upstream.Close()

	h, s, mini := setupValidationHandler(t)
	defer func() {
		_ = s.Close()
		mini.Close()
	}()

	if err := s.CreateModel(context.Background(), &store.Model{
		Channel: "Grok", ModelID: "grok-4.5", Name: "Grok 4.5",
		Status: store.ModelStatusAvailable, Verified: true,
	}); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if err := s.CreateAccount(context.Background(), &store.Account{
		AccountType: "grok", Enabled: true, CredentialType: "oauth", GrokProvider: ProviderBuild,
		OAuthAccessToken: jwtWithClaims(t, `{"sub":"user-1","team_id":"team-1"}`),
		OAuthExpiresAt:   time.Now().Add(time.Hour), GrokModels: []string{"grok-4.5"},
		GrokModelsSyncedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	h.cfg = &config.Config{GrokCLIBaseURL: upstream.URL + "/v1"}
	h.cliClient = NewCLIClient(h.cfg)
	h.cliClient.SetAccountStore(s)
	h.cliClient.httpClient = upstream.Client()
	h.cliClient.oauth.httpClient = upstream.Client()

	body := `{
		"model":"grok-4.5", "input":"hello", "stream":true,
		"previous_response_id":"resp_previous", "metadata":{"trace":"keep"},
		"include":["reasoning.encrypted_content"],
		"tools":[{"type":"function","name":"weather","description":"get weather","parameters":{"type":"object"}}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := rec.Header().Get("X-Request-Id"); got != "native-response-test" {
		t.Fatalf("X-Request-Id=%q", got)
	}
	if got := rec.Body.String(); !strings.Contains(got, "event: response.created") || strings.Contains(got, "chat.completion.chunk") {
		t.Fatalf("response was not native Responses SSE: %q", got)
	}
	if got := received["previous_response_id"]; got != "resp_previous" {
		t.Fatalf("previous_response_id=%v", got)
	}
	metadata, _ := received["metadata"].(map[string]interface{})
	if got := metadata["trace"]; got != "keep" {
		t.Fatalf("metadata.trace=%v", got)
	}
	if got := len(interfaceSlice(received["tools"])); got != 1 {
		t.Fatalf("tools len=%d", got)
	}
}
