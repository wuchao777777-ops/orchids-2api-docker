package grok

import (
	"bytes"
	"compress/gzip"
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

func TestFetchCLIBillingUsesIdentityAndAppliesWeeklyQuota(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing" || r.URL.Query().Get("format") != "credits" {
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || r.Header.Get("X-XAI-Token-Auth") != "xai-grok-cli" || r.Header.Get("x-userid") != "user-1" || r.Header.Get("x-grok-team-id") != "team-1" {
			t.Fatalf("unexpected billing headers: %#v", r.Header)
		}
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		_, _ = io.WriteString(writer, `{"config":{"currentPeriod":{"end":"2026-09-01T00:00:00Z"},"creditUsagePercent":96}}`)
		_ = writer.Close()
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed.Bytes())
	}))
	defer server.Close()
	client := NewCLIClient(&config.Config{GrokCLIBaseURL: server.URL + "/v1"})
	client.httpClient = server.Client()
	client.oauth.httpClient = server.Client()
	acc := &store.Account{
		OAuthAccessToken: jwtWithClaims(t, `{"sub":"user-1","team_id":"team-1"}`),
		OAuthExpiresAt:   time.Now().Add(time.Hour),
	}
	info, err := client.FetchBilling(context.Background(), acc)
	if err != nil {
		t.Fatalf("FetchBilling() error = %v", err)
	}
	if !info.HasUsagePercent || info.UsagePercent != 96 || acc.UserID != "user-1" || acc.TeamID != "team-1" {
		t.Fatalf("info=%+v account=%+v", info, acc)
	}
	if !ApplyCLIBillingInfo(acc, info) || acc.Subscription != "unknown" || acc.UsageLimit != 100 || acc.UsageCurrent != 4 {
		t.Fatalf("billing not applied: %+v", acc)
	}
}
