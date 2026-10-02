package accountpolicy

import (
	"errors"
	"testing"

	apperrors "orchids-api/internal/errors"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

// TestRetryable_MatchesTheSharedUpstreamClassification pins the single rule the
// request path and the scheduler now share. Before this the handler decided
// retries from category strings while the scheduler decided state from the
// policy, so the same error could be "retryable" in one entrance and "cooling
// down for ten minutes" in the other.
func TestRetryable_MatchesTheSharedUpstreamClassification(t *testing.T) {
	cases := []struct {
		message string
		want    bool
	}{
		{"401: unauthenticated", true},
		{"429: too many requests", true},
		{"reading upstream body: context canceled", false},
		{"502: bad gateway", true},
	}
	for _, tc := range cases {
		err := errors.New(tc.message)
		class := apperrors.ClassifyUpstreamError(tc.message)
		testutil.Equal(t, Retryable(err), tc.want)
		got, want := Retryable(err), class.Retryable
		testutil.Falsef(t, got != want, "Retryable(%q) = %v but the shared classification says %v", tc.message, got, want)
	}
	testutil.False(t, Retryable(nil), "a nil error is not retryable")
}

// TestClassify_VerdictMatchesTheSharedRetryRule makes the two systems provably
// agree: whatever the policy says about retrying is what the shared
// classification says, for every verdict shape.
func TestClassify_VerdictMatchesTheSharedRetryRule(t *testing.T) {
	acc := &store.Account{AccountType: "grok"}
	messages := []string{
		"401: grok session unauthenticated",
		"403: forbidden",
		"402: out of credits",
		"429: too many requests",
		"404: model is not found",
		"context canceled",
		"502: bad gateway",
	}
	for _, message := range messages {
		err := errors.New(message)
		verdict := Classify(acc, err, "grok-4.6")
		testutil.Equal(t, verdict.Retryable, Retryable(err))
	}
}
