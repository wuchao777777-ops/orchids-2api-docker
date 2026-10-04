package errors

import "strings"

// Provider phrases the classifier has to recognise.
//
// An upstream states its refusal in its own words, and those words are the only
// reliable signal: a rate limit and an outage can arrive on the same status
// code, and guessing from the status misclassifies them. So the classifier must
// know these strings.
//
// It should know each of them exactly once. They used to be written inline at
// every branch that needed them, which let two branches of the same classifier
// drift apart and made adding a channel a hunt through switch cases. They are
// collected here instead, grouped by the channel they belong to.
//
// The keys are channel.ID values as documented in internal/channel; this
// package imports nothing from internal/ on purpose, so they are plain strings
// with the mapping written in the comments. Adding an import edge from
// internal/errors would reach every package that classifies an error —
// loadbalancer, handler, upstream and accountpolicy among them.
var providerPhrases = map[string][]string{
	// channel.ID "qoder".
	"qoder": {
		// The pricing page is how Qoder reports "no plan or allowance covers
		// this": an entitlement refusal, not a broken credential.
		"qoder.com/pricing",
		// Per-agent concurrency ceiling, reset at a named time.
		"qoder agent limit reached",
		// Per-model throttle.
		"qoder model rate limited",
		// The rendered form of business code 10605: the request was queued.
		"qoder gateway is busy",
		// Account allowance fully spent.
		"qoder quota exhausted",
	},
	// channel.ID "cline".
	"cline": {
		// Inference cap, with the wait written in prose ("Try again in 17h 59m").
		"cline inference cap reached",
		// A model reachable only through a Cline product surface.
		"only available via cline product surfaces",
	},
}

// qoderBusinessCodes are Qoder's numeric business codes that name a condition
// the prose may have lost. 10605 is the queue/busy gate.
var qoderBusinessCodes = []string{"10605"}

// HasProviderPhrase reports whether text contains any phrase registered for the
// channel. text must already be normalised (lower-cased, unwrapped) the way the
// caller normalises everything else it matches on.
func HasProviderPhrase(channel, text string) bool {
	for _, phrase := range providerPhrases[strings.ToLower(strings.TrimSpace(channel))] {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// IsQoderEntitlement reports a Qoder refusal that says no plan or allowance
// covers the request. The credential was accepted, so this must not become an
// account status.
func IsQoderEntitlement(lower string) bool { return strings.Contains(lower, "qoder.com/pricing") }

// IsQoderAgentLimit reports Qoder's per-agent concurrency ceiling.
func IsQoderAgentLimit(lower string) bool {
	return strings.Contains(lower, "qoder agent limit reached")
}

// IsQoderModelRateLimited reports Qoder's per-model throttle.
func IsQoderModelRateLimited(lower string) bool {
	return strings.Contains(lower, "qoder model rate limited")
}

// IsQoderQuotaExhausted reports a Qoder account whose allowance is spent.
func IsQoderQuotaExhausted(lower string) bool {
	return strings.Contains(lower, "qoder quota exhausted")
}

// IsQoderGatewayBusy reports the rendered form of Qoder's queue/busy gate.
func IsQoderGatewayBusy(text string) bool { return strings.Contains(text, "qoder gateway is busy") }

// IsQoderQueueGate reports whether text names Qoder's queue gate, either by the
// business code or by the phrase this gateway renders it as once a parser has
// unwrapped the envelope. Both survive when the flags were dropped on the way.
func IsQoderQueueGate(text string) bool {
	for _, code := range qoderBusinessCodes {
		if strings.Contains(text, code) {
			return true
		}
	}
	return IsQoderGatewayBusy(text)
}

// IsClineInferenceCap reports Cline's inference cap.
func IsClineInferenceCap(lower string) bool {
	return strings.Contains(lower, "cline inference cap reached")
}

// IsClineProductSurface reports a model reachable only through a Cline product
// surface.
func IsClineProductSurface(lower string) bool {
	return strings.Contains(lower, "only available via cline product surfaces")
}
