package debug

import (
	"context"
	"orchids-api/internal/testutil"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestCaptureRoundTripAndRetention(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	store := NewDiagnosticStore(client, "test:")
	ctx, capture := WithCapture(context.Background(), "request/../1")
	logger := NewForContext(ctx, false, false)
	logger.LogIncomingRequest(map[string]interface{}{"model": "test", "api_key": "sensitive-key", "max_tokens": 100})
	logger.LogUpstreamRequest("https://upstream.test", map[string]string{"Authorization": "Bearer a-secret"}, []byte(`{"messages":[{"content":"hello"}]}`))
	raw := `data: {"text":"hello","access_token":"super-secret"}` + "\n"
	capture.Append("4_upstream_sse.jsonl", raw)
	logger.Close()
	bundle := capture.Bundle()
	testutil.NoError(t, store.Save(ctx, bundle))
	loaded, err := store.Get(ctx, bundle.RequestID)
	if err != nil || loaded == nil {
		t.Fatalf("bundle=%v err=%v", loaded, err)
	}
	joined := ""
	for _, section := range loaded.Sections {
		joined += section.Payload
	}
	testutil.MustNotContainAny(t, joined, "super-secret", "sensitive-key", "a-secret")
	testutil.MustContainAll(t, joined, "hello", "max_tokens")
	indexes, err := store.Indexes(ctx, []string{bundle.RequestID, "missing"})
	if err != nil || len(indexes) != 1 {
		t.Fatalf("indexes=%v err=%v", indexes, err)
	}
	server.FastForward(DiagnosticRetention + time.Second)
	loaded, err = store.Get(ctx, bundle.RequestID)
	if err != nil || loaded != nil {
		t.Fatal("expired bundle is still visible")
	}
	indexes, err = store.Indexes(ctx, []string{bundle.RequestID})
	if err != nil || len(indexes) != 0 {
		t.Fatal("expired index is still visible")
	}
}
func TestCaptureLongSectionAndRawJSON(t *testing.T) {
	ctx, c := WithCapture(context.Background(), "bounded")
	defer c.Close()
	logger := NewForContext(ctx, false, false)
	logger.LogUpstreamRequest("https://example.test", nil, []byte(`{"messages":["human-readable"]}`))
	c.Append("4_upstream_sse.jsonl", strings.Repeat("x", maxCaptureBytes+100))
	b := c.Bundle()
	if b.Truncated {
		t.Fatal("missing truncation marker")
	}
	for _, s := range b.Sections {
		if s.Name == "4_upstream_sse.jsonl" && s.Bytes != maxCaptureBytes+100 {
			t.Fatal("unbounded section")
		}
		if s.Name == "upstream_001_request.json" && !strings.Contains(s.Payload, "human-readable") {
			t.Fatal("request body encoded as base64")
		}
	}
}

func TestCaptureRedactsTruncatedCredentialAndPrefixedKeys(t *testing.T) {
	for _, raw := range []string{`{"oauth_access_token":"incomplete-secret`, `{"client_cookie":"sso-secret"}`, `{"url":"http://user:pwd@host"}`} {
		clean := sanitizeCapture(raw)
		for _, secret := range []string{"incomplete-secret", "sso-secret", ":pwd@"} {
			testutil.MustNotContain(t, clean, secret)
		}
	}
}

func TestLargeCompressedBundleAndLegacyCompatibility(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	store := NewDiagnosticStore(client, "test:")
	ctx, c := WithCapture(context.Background(), "large")
	defer c.Close()
	payload := strings.Repeat("long-content-中文\n", 100000) + "FINAL_SENTINEL"
	c.Append("response.txt", payload[:65535])
	c.Append("response.txt", payload[65535:])
	b := c.Bundle()
	if b.Truncated || b.Sections[0].Payload != payload {
		t.Fatal("large capture differs")
	}
	testutil.NoError(t, store.Save(ctx, b))
	stored, _ := client.Get(ctx, store.key("large")).Bytes()
	if len(stored) >= len(payload)/2 || stored[0] != 0x1f {
		t.Fatal("not compressed")
	}
	loaded, err := store.Get(ctx, "large")
	if err != nil || loaded.Sections[0].Payload != payload {
		t.Fatal("compressed round trip failed", err)
	}
	client.Set(ctx, store.key("legacy"), `{"request_id":"legacy","sections":[],"bytes":0}`, DiagnosticRetention)
	old, err := store.Get(ctx, "legacy")
	if err != nil || old.RequestID != "legacy" {
		t.Fatal("legacy read failed", err)
	}
}
