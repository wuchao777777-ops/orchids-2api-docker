package qoder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"orchids-api/internal/debug"
	"orchids-api/internal/upstream"
	"strings"
	"testing"
)

func TestQoderDiagnosticAttemptsCaptureSSEAndModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(envelope(`{"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":"stop"}]}`) + "event:finish\ndata:{\"firstTokenDuration\":12,\"totalDuration\":20,\"serverDuration\":2}\n\n"))
	}))
	defer srv.Close()
	client := NewFromAccount(signedTestAccount(), nil)
	ctx, capture := debug.WithCapture(context.Background(), "latency-test")
	_, err := client.attemptChat(ctx, chatURL(srv.URL), EncodeBody([]byte(`{"messages":[],"tools":[],"session_id":"private-session","parameters":{"context_length":200000}}`)), modelEntry{Key: "qfmodel", Source: "system"}, "request", RuntimeFields{Key: "secret-key", EncryptUserInfo: "secret-info"}, client.currentCredentials(), false, func(upstream.SSEMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, section := range capture.Bundle().Sections {
		if strings.Contains(section.Payload, "secret-key") || strings.Contains(section.Payload, "secret-info") {
			t.Fatal("secret leaked")
		}
		if !strings.HasSuffix(section.Name, "latency.json") {
			continue
		}
		var v map[string]interface{}
		if e := json.Unmarshal([]byte(section.Payload), &v); e != nil {
			t.Fatal(e)
		}
		for _, key := range []string{"first_sse_ms", "first_text_ms", "total_ms", "response_headers_ms", "remote_address", "conversation_fingerprint"} {
			if _, ok := v[key]; !ok {
				t.Fatalf("missing %s: %v", key, v)
			}
		}
		if v["model_key"] != "qfmodel" || v["upstream_firstTokenDuration"] != float64(12) {
			t.Fatalf("metadata=%v", v)
		}
		found = true
	}
	if !found {
		t.Fatal("missing latency artifact")
	}
}
