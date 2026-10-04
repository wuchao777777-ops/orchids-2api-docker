package errors

import "strings"

// Shared predicates for provider refusal phrases and business codes.
// Callers normalize phrase inputs to lower case before classification.
// qoderBusinessCodes are Qoder's numeric business codes that name a condition
// the prose may have lost. 10605 is the queue/busy gate.
var qoderBusinessCodes = []string{"10605"}

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
