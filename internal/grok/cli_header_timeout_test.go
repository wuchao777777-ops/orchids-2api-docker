package grok

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
)

func headerTimeoutTestClient(t *testing.T, server *httptest.Server, timeout time.Duration) (*CLIClient, *store.Account) {
	t.Helper()
	client := NewCLIClient(&config.Config{GrokCLIBaseURL: server.URL + "/v1"})
	client.httpClient = server.Client()
	client.oauth.httpClient = server.Client()
	client.responseHeaderTimeout = timeout
	account := &store.Account{
		OAuthAccessToken: jwtWithClaims(t, `{"sub":"header-timeout-user"}`),
		OAuthExpiresAt:   time.Now().Add(time.Hour),
	}
	return client, account
}

func TestCLIResponsesHeaderTimeoutSwitchesAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, account := headerTimeoutTestClient(t, server, 20*time.Millisecond)
	started := time.Now()
	_, err := client.request(context.Background(), account, http.MethodPost, server.URL+"/v1/responses", []byte(`{}`), nil)
	if err == nil || !strings.Contains(err.Error(), "response header timeout") {
		t.Fatalf("err=%v", err)
	}
	if !shouldSwitchGrokAccount(err) {
		t.Fatalf("timeout must switch account: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 120*time.Millisecond {
		t.Fatalf("header timeout took %s", elapsed)
	}
}

func TestCLIResponsesHeaderTimeoutStopsAfterHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(60 * time.Millisecond)
		_, _ = io.WriteString(w, "data: done\n\n")
	}))
	defer server.Close()
	client, account := headerTimeoutTestClient(t, server, 20*time.Millisecond)
	resp, err := client.request(context.Background(), account, http.MethodPost, server.URL+"/v1/responses", []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "data: done\n\n" {
		t.Fatalf("body=%q", body)
	}
}
