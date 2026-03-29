package main

import (
	"testing"

	"orchids-api/internal/grok"
)

func TestNormalizeGrokSSOToken(t *testing.T) {
	raw := "foo=1; sso=abc123; sso-rw=abc123"
	if got := grok.NormalizeSSOToken(raw); got != "abc123" {
		t.Fatalf("NormalizeSSOToken()=%q want abc123", got)
	}
}
