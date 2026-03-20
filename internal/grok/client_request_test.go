package grok

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orchids-api/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestCloneHeaderShallow_SetIsolation(t *testing.T) {
	src := http.Header{
		"X-Test":  []string{"alpha"},
		"X-Other": []string{"one"},
	}
	cloned := cloneHeaderShallow(src, 1)
	cloned.Set("X-Test", "beta")
	cloned.Set("X-New", "two")

	if got := src.Get("X-Test"); got != "alpha" {
		t.Fatalf("source header changed, got=%q", got)
	}
	if got := src.Get("X-New"); got != "" {
		t.Fatalf("source unexpectedly contains new key, got=%q", got)
	}
}

func TestBuildGrokCookie(t *testing.T) {
	got := buildGrokCookie("tok", "cf-clear", "bm")
	want := "sso=tok; sso-rw=tok; cf_clearance=cf-clear; __cf_bm=bm"
	if got != want {
		t.Fatalf("buildGrokCookie()=%q want=%q", got, want)
	}
}

func TestDoRequest_DoesNotMutateInputHeaders(t *testing.T) {
	t.Parallel()

	attempt := 0
	requestIDs := make([]string, 0, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		requestIDs = append(requestIDs, strings.TrimSpace(r.Header.Get("x-xai-request-id")))
		if attempt == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "retry", http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := New(&config.Config{
		MaxRetries: 1,
		RetryDelay: 1,
	})
	headers := c.headers("token-abc")
	originalID := headers.Get("x-xai-request-id")

	resp, err := c.doRequest(context.Background(), srv.URL, http.MethodGet, nil, headers, http.StatusOK, false)
	if err != nil {
		t.Fatalf("doRequest() error: %v", err)
	}
	_ = resp.Body.Close()

	if got := headers.Get("x-xai-request-id"); got != originalID {
		t.Fatalf("input headers mutated: got=%q want=%q", got, originalID)
	}
	if attempt != 2 {
		t.Fatalf("expected 2 attempts, got=%d", attempt)
	}
	for i, id := range requestIDs {
		if id == "" {
			t.Fatalf("request %d missing x-xai-request-id", i+1)
		}
	}
}

func TestDoRequest_FallsBackWhenUTLSTransportSeesMalformedHTTP2Response(t *testing.T) {
	t.Parallel()

	primaryCalls := 0
	fallbackCalls := 0
	c := &Client{
		cfg: &config.Config{MaxRetries: 0},
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				primaryCalls++
				return nil, fmt.Errorf(`Post "https://grok.com/rest/app-chat/conversations/new": net/http: HTTP/1.x transport connection broken: malformed HTTP response "\x00\x00\x12\x04"`)
			}),
		},
		fallbackHTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				fallbackCalls++
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("ok")),
					Request:    req,
				}, nil
			}),
		},
	}

	resp, err := c.doRequest(context.Background(), "https://grok.com/rest/app-chat/conversations/new", http.MethodPost, []byte(`{"message":"hi"}`), http.Header{}, http.StatusOK, false)
	if err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	_ = resp.Body.Close()

	if primaryCalls != 1 {
		t.Fatalf("primaryCalls=%d want 1", primaryCalls)
	}
	if fallbackCalls != 1 {
		t.Fatalf("fallbackCalls=%d want 1", fallbackCalls)
	}
}

func TestDoRequest_DoesNotFallbackForGenericTransportError(t *testing.T) {
	t.Parallel()

	primaryCalls := 0
	fallbackCalls := 0
	c := &Client{
		cfg: &config.Config{MaxRetries: 0},
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				primaryCalls++
				return nil, fmt.Errorf("dial tcp: connection refused")
			}),
		},
		fallbackHTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				fallbackCalls++
				return nil, fmt.Errorf("unexpected fallback call")
			}),
		},
	}

	_, err := c.doRequest(context.Background(), "https://grok.com/rest/app-chat/conversations/new", http.MethodPost, []byte(`{"message":"hi"}`), http.Header{}, http.StatusOK, false)
	if err == nil {
		t.Fatal("expected doRequest() to fail")
	}
	if primaryCalls != 1 {
		t.Fatalf("primaryCalls=%d want 1", primaryCalls)
	}
	if fallbackCalls != 0 {
		t.Fatalf("fallbackCalls=%d want 0", fallbackCalls)
	}
}
