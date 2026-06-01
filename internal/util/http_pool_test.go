package util

import (
	"testing"
	"time"
)

func TestResponseHeaderTimeoutForClient(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "default", timeout: 0, want: 30 * time.Second},
		{name: "short", timeout: 20 * time.Second, want: 20 * time.Second},
		{name: "one minute", timeout: 60 * time.Second, want: 60 * time.Second},
		{name: "grok default cap", timeout: 600 * time.Second, want: 120 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := responseHeaderTimeoutForClient(tt.timeout); got != tt.want {
				t.Fatalf("responseHeaderTimeoutForClient(%s)=%s want=%s", tt.timeout, got, tt.want)
			}
		})
	}
}

func TestSharedHTTPClientCacheKeyIncludesTimeout(t *testing.T) {
	short := sharedHTTPClientCacheKey("direct", 30*time.Second)
	long := sharedHTTPClientCacheKey("direct", 600*time.Second)
	if short == long {
		t.Fatalf("cache key should include timeout, got same key %q", short)
	}
}
