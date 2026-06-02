package grok

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"orchids-api/internal/config"
)

func TestDoConsole_DoesNotRetry429OnSameAccount(t *testing.T) {
	t.Parallel()

	calls := 0
	h := &Handler{
		client: &Client{
			cfg: &config.Config{
				MaxRetries:       3,
				Retry429Interval: 30,
			},
			httpClient: &http.Client{
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					return &http.Response{
						StatusCode: http.StatusTooManyRequests,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"error":"rate limit"}`)),
						Request:    req,
					}, nil
				}),
			},
		},
	}

	_, err := h.doConsole(context.Background(), "token", map[string]interface{}{"model": "grok-4.3", "input": "hi"})
	if err == nil {
		t.Fatal("expected doConsole() to fail on 429")
	}
	if !strings.Contains(err.Error(), "grok upstream status=429") {
		t.Fatalf("error=%q, want 429 upstream error", err.Error())
	}
	if calls != 1 {
		t.Fatalf("calls=%d want 1", calls)
	}
}
