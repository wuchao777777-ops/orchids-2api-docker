package errors

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestUpstreamUnavailableAnswerIsHonest pins the answer a caller gets when the
// upstream says its own service is down for the model.
//
// Production measured 636 such refusals in one day, every one carrying
// serviceAvailable:false and queueCount:0, and every one was answered as 429
// "the available upstream accounts are rate-limited" after a p50 of 104s of
// waiting: a lie about the cause, with no hint about when to come back.
func TestUpstreamUnavailableAnswerIsHonest(t *testing.T) {
	category := ClassifyUpstreamError(`qoder gateway is busy: {"code":"10605","serviceAvailable":false,"queueCount":0,"retryAfterSeconds":30}`).Category
	if category != "upstream_unavailable" {
		t.Fatalf("category = %q, want upstream_unavailable", category)
	}
	if got := StatusForCategory(category); got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", got, http.StatusServiceUnavailable)
	}
	message := PublicMessage(`qoder gateway is busy: {"code":"10605","serviceAvailable":false,"queueCount":0}`)
	if message != "The upstream service for this model is temporarily unavailable. Retry after the indicated delay." {
		t.Fatalf("message = %q", message)
	}
	if got := StatusForCategory("rate_limit"); got != http.StatusTooManyRequests {
		t.Fatalf("rate_limit status = %d, want 429: a real throttle keeps its own answer", got)
	}
}

// TestAppErrorPublishesRetryAfter keeps the hint on the wire. A capacity answer
// that took a minute of upstream retries to produce has to tell the caller when
// to come back; without the header the client only learns that something failed
// and retries on its own schedule, adding load to the condition.
func TestAppErrorPublishesRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	NewWithRetryAfter("upstream_unavailable", "down", StatusForCategory("upstream_unavailable"), 30*time.Second).WriteResponse(rec)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After = %q, want \"30\"", got)
	}

	// No hint means no header: an invented one would be a promise nobody made.
	rec = httptest.NewRecorder()
	New("rate_limit", "limited", StatusForCategory("rate_limit")).WriteResponse(rec)
	if got := rec.Header().Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After = %q, want it unset when no hint is known", got)
	}
}

// TestPoolAnswerDistinguishesEntitlementFromThrottle pins the pool's answer for
// a model the account plan does not cover. Qoder reports it as business code 112
// and the gateway cached it as a model cooldown, so asking for that model again
// was answered with "the requested model is temporarily rate-limited" — inviting
// a retry for a condition only a plan change fixes.
func TestPoolAnswerDistinguishesEntitlementFromThrottle(t *testing.T) {
	entitlement := ClassifyPoolExhaustion(nil, "qoder account has no usable plan or allowance; the model requires a subscription (upstream code=112)")
	if entitlement.Category != "model_unavailable" {
		t.Fatalf("category = %q, want model_unavailable", entitlement.Category)
	}
	if got := StatusForCategory(entitlement.Category); got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", got)
	}

	unavailable := ClassifyPoolExhaustion(nil, `qoder gateway is busy: {"code":"10605","serviceAvailable":false,"queueCount":0}`)
	if unavailable.Category != "upstream_unavailable" {
		t.Fatalf("category = %q, want upstream_unavailable", unavailable.Category)
	}
	if got := StatusForCategory(unavailable.Category); got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", got)
	}
}

// TestPoolAnswerReadsTheSelectorReason covers the other entrance: a request the
// selection layer refuses before any upstream call. All it can report is its own
// note, so the note has to carry the reason the filter gave — modelling a
// day-long plan verdict as "cooling down" is what invited a retry that could only
// fail the same way.
func TestPoolAnswerReadsTheSelectorReason(t *testing.T) {
	plan := "no enabled accounts available for channel: qoder (the requested model is not covered by any matching account's plan)"
	got := ClassifyPoolExhaustion(errors.New(plan), plan)
	if got.Category != "model_unavailable" {
		t.Fatalf("category = %q, want model_unavailable", got.Category)
	}
	if status := StatusForCategory(got.Category); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}

	mixed := "no enabled accounts available for channel: qoder (the requested model is cooling down on some matching accounts and not covered by the plans of the rest)"
	got = ClassifyPoolExhaustion(errors.New(mixed), mixed)
	if got.Category != "rate_limit" {
		t.Fatalf("category = %q, want rate_limit: part of the pool only needs a wait", got.Category)
	}

	throttled := "no enabled accounts available for channel: qoder (all matching accounts are cooling down for the requested model)"
	got = ClassifyPoolExhaustion(errors.New(throttled), throttled)
	if got.Category != "rate_limit" {
		t.Fatalf("category = %q, want rate_limit", got.Category)
	}
}
