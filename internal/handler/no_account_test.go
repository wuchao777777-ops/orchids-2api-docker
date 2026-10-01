package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	apperrors "orchids-api/internal/errors"
	"orchids-api/internal/testutil"
)

// TestWritePoolExhaustion_CapacityProblemIsRetryable is the initial-selection
// contract: a pool that is cooling down is a capacity problem, so the answer is a
// retryable 429 with the cause in the text — not the 503 "overloaded_error" that
// carried the selector's internal note and made a cooldown look like a server
// fault (the shape the WorkBuddy outage was reported to its callers in).
func TestWritePoolExhaustion_CapacityProblemIsRetryable(t *testing.T) {
	selectErr := errors.New("no enabled accounts available for channel: workbuddy (all matching accounts are cooling down for the requested model)")
	rec := httptest.NewRecorder()

	writePoolExhaustion(rec, classifyPoolExhaustion(selectErr, selectErr.Error()))

	testutil.Equal(t, rec.Code, http.StatusTooManyRequests)
	body := rec.Body.String()
	testutil.MustContain(t, body, "the requested model is cooling down on this channel")
	testutil.MustNotContain(t, body, "no enabled accounts available for channel")
	testutil.MustNotContain(t, body, "overloaded_error")
}

// TestWritePoolExhaustion_ResidualCausePointsAtTheOperator covers everything that
// cannot be explained by a cooldown or an allowance: with no accounts at all, or
// accounts the upstream refuses outright, waiting is not the answer and the client
// needs to be told that an operator has to act.
func TestWritePoolExhaustion_ResidualCausePointsAtTheOperator(t *testing.T) {
	selectErr := errors.New("no enabled accounts available for channel: grok")
	rec := httptest.NewRecorder()

	writePoolExhaustion(rec, classifyPoolExhaustion(selectErr, ""))

	testutil.Equal(t, rec.Code, http.StatusServiceUnavailable)
	body := rec.Body.String()
	testutil.MustContain(t, body, apperrors.PoolNoAccountsMessage)
	testutil.MustNotContain(t, body, "no enabled accounts available for channel")
}
