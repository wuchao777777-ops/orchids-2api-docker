package errors

import (
	"errors"
	"testing"
)

func TestIsAccountAuthFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "signed out", err: errors.New("signed out: no active sessions found"), want: true},
		{name: "forbidden", err: errors.New("HTTP 403 forbidden"), want: true},
		{name: "rate limit", err: errors.New("too many requests"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAccountAuthFailure(tt.err); got != tt.want {
				t.Fatalf("IsAccountAuthFailure(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyUpstreamError(t *testing.T) {
	tests := []struct {
		name         string
		errStr       string
		wantCategory string
		wantRetry    bool
		wantSwitch   bool
	}{
		{
			name:         "model not found is client error",
			errStr:       "puter API error: message=Model not found, please try another model",
			wantCategory: "client",
			wantRetry:    false,
			wantSwitch:   false,
		},
		{
			name:         "no implementation available is client error",
			errStr:       "puter API error: code=no_implementation_available, status=502, message=No implementation available for interface `puter-chat-completion`.",
			wantCategory: "client",
			wantRetry:    false,
			wantSwitch:   false,
		},
		{
			name:         "insufficient funds is rate limit",
			errStr:       "puter API error: code=insufficient_funds, status=402, message=Available funding is insufficient for this request.",
			wantCategory: "rate_limit",
			wantRetry:    true,
			wantSwitch:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyUpstreamError(tt.errStr)
			if got.Category != tt.wantCategory || got.Retryable != tt.wantRetry || got.SwitchAccount != tt.wantSwitch {
				t.Fatalf("ClassifyUpstreamError(%q) = %#v, want category=%q retry=%v switch=%v", tt.errStr, got, tt.wantCategory, tt.wantRetry, tt.wantSwitch)
			}
		})
	}
}
