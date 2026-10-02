package errors

import (
	"orchids-api/internal/testutil"
	"testing"
)

// TestClassifySharedQueueRefusalDoesNotRotate pins that every shape of a
// shared/upstream-wide queue refusal is recognised. Falling through to the
// default branch labelled it "unknown" with SwitchAccount=true, so one shared
// refusal was retried against every account in the pool in turn: Qoder's nine
// accounts were all parked as "429" within seconds and the pool went empty.
func TestClassifySharedQueueRefusalDoesNotRotate(t *testing.T) {
	// The exact status_message stored for production account 22: the text blames
	// the credential, the body says the service is unavailable.
	const production = `qoder upstream rejected the credential: {"code":"10605","message":"{\"isQueued\":true,\"modelKey\":\"qfmodel\",\"queueCount\":0,\"queueType\":\"p3\",\"retryAfterSeconds\":30,\"serviceAvailable\":false,\"waitTime\":30}"}`

	for name, tc := range map[string]struct {
		message  string
		category string
	}{
		"production message":      {production, "upstream_queue"},
		"classified busy form":    {"qoder gateway is busy: serviceAvailable=false retryAfterSeconds=29", "upstream_queue"},
		"upstream pool throttled": {"qoder API error: the available upstream accounts are rate-limited", "rate_limit"},
		"service unavailable":     {`qoder upstream error: {"serviceAvailable":false}`, "upstream_unavailable"},
		"queued flag":             {`qoder upstream error: {"isQueued":true}`, "upstream_queue"},
	} {
		t.Run(name, func(t *testing.T) {
			class := ClassifyUpstreamError(tc.message)
			testutil.CheckEqual(t, class.Category, tc.category)
			testutil.CheckFalse(t, class.SwitchAccount, "SwitchAccount = true; every account meets the identical refusal")
		})
	}
}

// TestClassifyEscapedMarkersSurviveNesting is the regression test for the
// dead-marker bug: Qoder's payload reaches the classifier as a JSON string
// nested inside another one, so the text holds `\"isQueued\":true`. Searching it
// for `"isqueued":true` never matched, and the shared refusal stayed classified
// only because the bare "10605" digits happened to sit in the same string. A
// closed gate reported without those digits was read as a credential problem.
func TestClassifyEscapedMarkersSurviveNesting(t *testing.T) {
	// No 10605 anywhere: the classification has to come from the escaped flags.
	const escapedNoCode = `qoder upstream rejected the credential: {"message":"{\"isQueued\":true,\"queueCount\":0,\"serviceAvailable\":false,\"waitTime\":30}"}`
	class := ClassifyUpstreamError(escapedNoCode)
	testutil.Equal(t, class.Category, "upstream_queue")
	testutil.False(t, class.SwitchAccount, "SwitchAccount = true; a closed gate is identical for every account")

	// The same flags under an explicit 401 envelope, which is how Qoder reports
	// 10605: the credential branch used to win and the handler rotated the pool.
	escapedUnder401 := `qoder gateway is busy: qoder API error: status=401, method=POST, path=/algo/api/v2/chat, code=10605, message={\"isQueued\":true,\"queueCount\":0,\"serviceAvailable\":false,\"retryAfterSeconds\":30}`
	class = ClassifyUpstreamError(escapedUnder401)
	testutil.Equal(t, class.Category, "upstream_queue")
	testutil.False(t, class.SwitchAccount, "SwitchAccount = true; the 401 envelope made the handler rotate the whole pool")
}

// TestClassifyOrdinaryThrottleStillSwitches guards the other direction: an
// account-scoped throttle must keep switching, so the shared-refusal rule cannot
// swallow the channels it does not describe.
func TestClassifyOrdinaryThrottleStillSwitches(t *testing.T) {
	for name, message := range map[string]string{
		"plain 429":         "upstream API error: status=429, too many requests",
		"cline prose cap":   "cline inference cap reached: Try again in 17h 59m",
		"qoder agent limit": "qoder agent limit reached; resets at 2026-09-27T19:47:13Z",
	} {
		t.Run(name, func(t *testing.T) {
			class := ClassifyUpstreamError(message)
			testutil.CheckEqual(t, class.Category, "rate_limit")
			testutil.CheckFalse(t, !class.SwitchAccount, "SwitchAccount = false; an account-scoped throttle must still rotate")
		})
	}
}
