package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTrustedProxyMiddlewareRejectsSpoofedForwardingHeaders(t *testing.T) {
	middleware, err := TrustedProxyMiddleware([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := ClientIP(r); got != "203.0.113.10" {
			t.Fatalf("client IP = %q", got)
		}
		if got := r.Header.Get("X-Forwarded-For"); got != "" {
			t.Fatalf("untrusted forwarding header survived: %q", got)
		}
		if got := r.Header.Get("X-Forwarded-Proto"); got != "" {
			t.Fatalf("untrusted proto survived: %q", got)
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Forwarded-Proto", "https")
	handler.ServeHTTP(httptest.NewRecorder(), req)
}

func TestTrustedProxyMiddlewareWalksForwardedChain(t *testing.T) {
	middleware, err := TrustedProxyMiddleware([]string{"10.0.0.0/8", "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := ClientIP(r); got != "198.51.100.9" {
			t.Fatalf("client IP = %q", got)
		}
		if got := r.Header.Get("X-Forwarded-Proto"); got != "https" {
			t.Fatalf("trusted proto = %q", got)
		}
		if got := r.Header.Get("X-Forwarded-For"); got != "198.51.100.9" {
			t.Fatalf("sanitized forwarding chain = %q", got)
		}
		if got := r.Header.Get("X-Forwarded-Host"); got != "api.example.com" {
			t.Fatalf("sanitized forwarded host = %q", got)
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.9, 192.0.2.10")
	req.Header.Set("X-Forwarded-Proto", "javascript, https")
	req.Header.Set("X-Forwarded-Host", "evil.example, api.example.com")
	handler.ServeHTTP(httptest.NewRecorder(), req)
}

func TestTrustedProxyMiddlewareRejectsInvalidNetwork(t *testing.T) {
	if _, err := TrustedProxyMiddleware([]string{"0.0.0.0/0"}); err != nil {
		// A broad network is syntactically valid and deliberately explicit.
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := TrustedProxyMiddleware([]string{"not-an-ip"}); err == nil {
		t.Fatal("expected invalid proxy error")
	}
}
