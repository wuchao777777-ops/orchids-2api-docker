package errors

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestUpstreamQueueGateAnswerIsHonest pins the answer a caller gets for Qoder's
// admission gate: the upstream queued the request and named a window.
//
// Production measured these refusals 636 times in one day, every one carrying
// serviceAvailable:false and queueCount:0, and every one was answered as 429
// "the available upstream accounts are rate-limited" after a p50 of 104s of
// waiting: a lie about the cause, with no hint about when to come back. The
// correction over-swung: it started answering 503 "the upstream service is
// temporarily unavailable" for a gate the same upstream served fine seconds
// later. The honest answer names the queue and keeps the 429 a client already
// knows how to back off from.
func TestUpstreamQueueGateAnswerIsHonest(t *testing.T) {
	const production = `qoder gateway is busy: {"code":"10605","serviceAvailable":false,"queueCount":0,"retryAfterSeconds":30}`

	category := ClassifyUpstreamError(production).Category
	if category != "upstream_queue" {
		t.Fatalf("category = %q, want upstream_queue", category)
	}
	if got := StatusForCategory(category); got != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", got, http.StatusTooManyRequests)
	}
	message := PublicMessage(`qoder gateway is busy: {"code":"10605","serviceAvailable":false,"queueCount":0}`)
	if !strings.Contains(message, "queue") {
		t.Fatalf("message = %q, want it to name the queue the upstream reported", message)
	}
	if strings.Contains(message, "temporarily unavailable") {
		t.Fatalf("message = %q, must not read as an outage: the same upstream answers most requests", message)
	}
	if got := StatusForCategory("rate_limit"); got != http.StatusTooManyRequests {
		t.Fatalf("rate_limit status = %d, want 429: a real throttle keeps its own answer", got)
	}
}

// TestUpstreamUnavailableAnswerStaysAnOutage guards the other direction: a
// payload that says the service is down without naming a queue is still an
// upstream fault, and must not be folded into the 429 the gate gets.
func TestUpstreamUnavailableAnswerStaysAnOutage(t *testing.T) {
	category := ClassifyUpstreamError(`qoder upstream error: {"serviceAvailable":false}`).Category
	if category != "upstream_unavailable" {
		t.Fatalf("category = %q, want upstream_unavailable", category)
	}
	if got := StatusForCategory(category); got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", got, http.StatusServiceUnavailable)
	}
	message := PublicMessage(`qoder upstream error: {"serviceAvailable":false}`)
	if message != "The upstream service for this model is temporarily unavailable. Retry after the indicated delay." {
		t.Fatalf("message = %q", message)
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

	// A queued gate is answered 429 with the same hint, which is the pair a
	// client needs to back off correctly instead of treating the gateway as down.
	rec = httptest.NewRecorder()
	NewWithRetryAfter("upstream_queue", "queued", StatusForCategory("upstream_queue"), 30*time.Second).WriteResponse(rec)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
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
	if unavailable.Category != "upstream_queue" {
		t.Fatalf("category = %q, want upstream_queue", unavailable.Category)
	}
	if got := StatusForCategory(unavailable.Category); got != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", got)
	}
	if unavailable.Message != PoolUpstreamQueueMessage {
		t.Fatalf("message = %q, want the queue answer", unavailable.Message)
	}

	// A bare serviceAvailable:false names no queue, so the pool still answers it
	// as an outage rather than inviting a retry that changes nothing.
	outage := ClassifyPoolExhaustion(nil, `qoder upstream error: {"serviceAvailable":false}`)
	if outage.Category != "upstream_unavailable" {
		t.Fatalf("category = %q, want upstream_unavailable", outage.Category)
	}
	if got := StatusForCategory(outage.Category); got != http.StatusServiceUnavailable {
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
