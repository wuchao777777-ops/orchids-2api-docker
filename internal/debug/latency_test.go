package debug

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLatencyCapturesReusedConnectionsAndSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer server.Close()
	ctx, capture := WithCapture(context.Background(), "latency")
	client := server.Client()
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
		req.Header.Set("Cosy-Key", "secret-key")
		req.Header.Set("Cosy-User", "private-user")
		a := BeginUpstream(ctx, req.Method, server.URL, req.Header, nil)
		traced, l := a.Trace(ctx, map[string]interface{}{"model_key": "qfmodel"})
		resp, err := client.Do(req.WithContext(traced))
		if err != nil {
			t.Fatal(err)
		}
		l.Response(resp)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		l.Mark("first_sse_ms")
		l.Mark("first_sse_ms")
		l.Finish(nil)
	}
	bundle := capture.Bundle()
	count := 0
	for _, s := range bundle.Sections {
		if strings.Contains(s.Payload, "secret-key") || strings.Contains(s.Payload, "private-user") {
			t.Fatal("credential leak")
		}
		if !strings.HasSuffix(s.Name, "latency.json") {
			continue
		}
		count++
		var v map[string]interface{}
		if err := json.Unmarshal([]byte(s.Payload), &v); err != nil {
			t.Fatal(err)
		}
		if count == 2 {
			if v["connection_reused"] != true {
				t.Fatalf("not reused: %v", v)
			}
			if _, ok := v["dns_ms"]; ok {
				t.Fatal("invented DNS timing")
			}
		}
		if v["http_status"] != float64(200) || v["http_protocol"] != "HTTP/1.1" {
			t.Fatalf("missing response: %v", v)
		}
	}
	if count != 2 {
		t.Fatalf("attempts=%d", count)
	}
}
func TestLatencyDisabledAndWaitCancellation(t *testing.T) {
	ctx := context.Background()
	var a *UpstreamAttempt
	traced, l := a.Trace(ctx, nil)
	if traced != ctx || l != nil {
		t.Fatal("traced disabled capture")
	}
	l.Mark("x")
	l.Finish(nil)
	ctx, c := WithCapture(ctx, "cancelled")
	RecordWait(ctx, "upstream_queue", time.Second, 10*time.Millisecond, true)

	found := false
	for _, s := range c.Bundle().Sections {
		if strings.Contains(s.Payload, `"actual_wait_ms": 10`) && strings.Contains(s.Payload, `"cancelled": true`) {
			found = true
		}
	}
	if !found {
		t.Fatal("wait lost")
	}
}
