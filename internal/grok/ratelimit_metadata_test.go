package grok

import (
	"net/http"
	"orchids-api/internal/testutil"
	"testing"
	"time"
)

func TestParseRateLimitMetadataBuildTeamRPS(t *testing.T) {
	body := []byte(`{"code":"resource-exhausted","error":"Too many requests for team f1692451-874f-4765-ab9b-5285f6c6ff65 and model grok-4.5-build-free. Your team's rate limit is — Requests per Second (actual/limit): 2/2."}`)
	metadata := ParseRateLimitMetadata(body)
	if metadata == nil {
		t.Fatal("expected metadata")
	}
	testutil.Equal(t, metadata.Scope, RateLimitScopeRPS)
	testutil.Equal(t, metadata.TeamID, "f1692451-874f-4765-ab9b-5285f6c6ff65")
	testutil.Equal(t, metadata.Model, "grok-4.5-build-free")
	if metadata.Actual != 2 || metadata.Limit != 2 {
		t.Fatalf("actual/limit = %d/%d", metadata.Actual, metadata.Limit)
	}
	testutil.Equal(t, metadata.RetryAfter, 2*time.Second)
}

func TestParseRateLimitMetadataRPMWithResetsIn(t *testing.T) {
	body := []byte(`{"code":"resource-exhausted","error":"Too many requests for team 00000000-0000-0000-0000-000000000013 and model grok-4.5. Your team's rate limit is — Requests per Minute (actual/limit): 58/60. Resets in: 45s."}`)
	metadata := ParseRateLimitMetadata(body)
	if metadata == nil {
		t.Fatal("expected metadata")
	}
	testutil.Equal(t, metadata.Scope, RateLimitScopeRPM)
	testutil.Equal(t, metadata.TeamID, "00000000-0000-0000-0000-000000000013")
	testutil.Equal(t, metadata.Model, "grok-4.5")
	if metadata.Actual != 58 || metadata.Limit != 60 {
		t.Fatalf("actual/limit = %d/%d", metadata.Actual, metadata.Limit)
	}
	// resets-in 45s overrides the default 1m RPM fallback.
	testutil.Equal(t, metadata.RetryAfter, 45*time.Second)
}

func TestParseRateLimitMetadataResetsInHour(t *testing.T) {
	body := []byte(`Too many requests for team f1692451-874f-4765-ab9b-5285f6c6ff65 and model grok-4.5. Requests per Minute (actual/limit): 60/60. Resets in: 1h 30m.`)
	metadata := ParseRateLimitMetadata(body)
	if metadata == nil {
		t.Fatal("expected metadata")
	}
	testutil.Equal(t, metadata.Scope, RateLimitScopeRPM)
	testutil.Equal(t, metadata.RetryAfter, 90*time.Minute)
}

func TestParseRateLimitMetadataOrdinary429(t *testing.T) {
	if metadata := ParseRateLimitMetadata([]byte(`{"error":"You are sending requests too quickly"}`)); metadata != nil {
		t.Fatalf("ordinary 429 must not parse as team rate limit: %#v", metadata)
	}
	if metadata := ParseRateLimitMetadata(nil); metadata != nil {
		t.Fatalf("nil body must not parse: %#v", metadata)
	}
	if metadata := ParseRateLimitMetadata([]byte(`not json at all`)); metadata != nil {
		t.Fatalf("plain text without pattern must not parse: %#v", metadata)
	}
}

func TestRateLimitFromResponseRetryAfterHeader(t *testing.T) {
	body := []byte(`Requests per Second (actual/limit): 6/6 for team 00000000-0000-0000-0000-000000000013 and model grok-4.5`)
	header := http.Header{}
	header.Set("Retry-After", "17")
	metadata := RateLimitFromResponse(http.StatusTooManyRequests, header, body)
	if metadata == nil {
		t.Fatal("expected metadata")
	}
	testutil.Equal(t, metadata.RetryAfter, 17*time.Second)
	testutil.Equal(t, metadata.Scope, RateLimitScopeRPS)
}

func TestRateLimitFromResponseNon429(t *testing.T) {
	if metadata := RateLimitFromResponse(http.StatusForbidden, nil, []byte(`Requests per Second (actual/limit): 6/6`)); metadata != nil {
		t.Fatalf("403 must not parse: %#v", metadata)
	}
}
