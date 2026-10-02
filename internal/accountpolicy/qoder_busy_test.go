package accountpolicy

import (
	"errors"
	"testing"
	"time"

	apperrors "orchids-api/internal/errors"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

// qoderBusyError reproduces the two things production actually paired: the text
// Qoder's refusal reached us as, and the retry hint the error advertises.
type qoderBusyError struct {
	message string
	wait    time.Duration
}

func (e qoderBusyError) Error() string             { return e.message }
func (e qoderBusyError) RetryAfter() time.Duration { return e.wait }

// The exact status_message stored for production account 22. The body says
// serviceAvailable:false / retryAfterSeconds:30, but the text blames the
// credential, which is how the pool drained: every account was parked as "429"
// for 30s and the gateway rotated to the next one, which met the same answer.
const productionQoderBusyMessage = `qoder upstream rejected the credential: {"code":"10605","message":"{\"isQueued\":true,\"modelKey\":\"qfmodel\",\"queueCount\":0,\"queueType\":\"p3\",\"retryAfterSeconds\":30,\"serviceAvailable\":false,\"waitTime\":30}"}`

// TestClassifyGlobalQueueRefusalWaitsWithoutHoldingTheAccount is the regression
// test for the Qoder pool drain.
//
// The contract has two halves. The account must not be held or rotated -- a
// refusal that every account receives cannot be escaped by moving, and parking
// accounts is what emptied the pool. The request must still be retryable, on the
// account it already holds, so a short upstream window becomes a short wait
// instead of a failure.
func TestClassifyGlobalQueueRefusalWaitsWithoutHoldingTheAccount(t *testing.T) {
	acc := &store.Account{ID: 22, AccountType: "qoder", Enabled: true}
	for _, tc := range []struct {
		name    string
		message string
	}{
		{"credential-shaped text carrying 10605", productionQoderBusyMessage},
		{"classified busy form", "qoder gateway is busy: serviceAvailable=false retryAfterSeconds=29"},
		{"upstream pool throttled", "qoder API error: available upstream accounts are rate-limited"},
		{"service unavailable flag alone", `qoder upstream error: status=401, {"serviceAvailable":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verdict := Classify(acc, qoderBusyError{message: tc.message, wait: 30 * time.Second}, "qwen3.8-flash")

			// The account must stay in the pool: no account status, and a scope
			// that is not the account or the credential.
			testutil.CheckEqual(t, verdict.Status, "")
			testutil.CheckFalsef(t, verdict.Scope == ScopeAccount || verdict.Scope == ScopeCredential, "scope = %q, want a scope that does not hold the account", verdict.Scope)
			// Nothing is persisted. A model cooldown here would take this account,
			// and then every other one, out of selection for the window, which is
			// the fail-fast this replaces.
			testutil.CheckEqual(t, verdict.Cooldown, 0)
			// Wait and retry, on the account already held.
			testutil.CheckFalse(t, !verdict.Retryable, "Retryable = false; a short queue window should be waited out, not failed")
			testutil.CheckFalse(t, verdict.SwitchAccount, "SwitchAccount = true; rotating multiplies one shared refusal across every account")
			testutil.CheckEqual(t, verdict.Model, "qwen3.8-flash")
		})
	}
}

// TestClassifyAccountScopedRateLimitStillRotates guards the other direction: a
// genuine per-account throttle must keep its account scope and still switch, so
// the global-refusal rule cannot swallow the channels it does not describe.
func TestClassifyAccountScopedRateLimitStillRotates(t *testing.T) {
	acc := &store.Account{ID: 3, AccountType: "cline", Enabled: true}
	verdict := Classify(acc, retryAfterTestError{wait: 2 * time.Minute}, "claude-sonnet-4")

	testutil.CheckEqual(t, verdict.Status, "429")
	testutil.CheckEqual(t, verdict.Scope, ScopeAccount)
	testutil.CheckFalse(t, !verdict.SwitchAccount || !verdict.Retryable, "a genuine per-account throttle must still switch accounts and retry")
	testutil.CheckEqual(t, verdict.Cooldown, 2*time.Minute)
}

// TestClassifyGlobalRefusalIgnoresAnUnusableHint pins that a shared refusal does
// not derive any account state from the hint. The wait itself is bounded where
// it is actually spent (the handler caps an honoured retry-after), so nothing
// here may invent a cooldown from a missing or absurd value.
func TestClassifyGlobalRefusalIgnoresAnUnusableHint(t *testing.T) {
	acc := &store.Account{ID: 22, AccountType: "qoder", Enabled: true}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"no hint at all", errors.New("qoder gateway is busy")},
		{"zero hint", qoderBusyError{message: "qoder gateway is busy", wait: 0}},
		{"absurd hint", qoderBusyError{message: "qoder gateway is busy", wait: 12 * time.Hour}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verdict := Classify(acc, tc.err, "qwen3.8-flash")
			testutil.CheckEqual(t, verdict.Cooldown, 0)
			testutil.CheckEqual(t, verdict.Status, "")
			testutil.CheckFalsef(t, !verdict.Retryable || verdict.SwitchAccount, "retryable=%v switch=%v, want a wait on the same account", verdict.Retryable, verdict.SwitchAccount)
		})
	}
}

// TestRetryLoopAndAccountPolicyAgreeOnSwitching pins the two classifiers
// together. The retry loop reads the state decision from the policy verdict but
// used to read the switch decision from internal/errors, and for Qoder's
// 401-shaped 10605 refusal the two disagreed: the policy said "wait on this
// account", the error class said "switch", so the handler walked the request
// across the pool and answered the client with an authentication failure.
func TestRetryLoopAndAccountPolicyAgreeOnSwitching(t *testing.T) {
	acc := &store.Account{ID: 22, AccountType: "qoder", Enabled: true}
	for name, message := range map[string]string{
		"credential-shaped 10605": productionQoderBusyMessage,
		"classified busy form":    "qoder gateway is busy: serviceAvailable=false retryAfterSeconds=29",
		"401 envelope with 10605": `qoder gateway is busy: qoder API error: status=401, method=POST, path=/algo/api/v2/chat, code=10605, message={"isQueued":true,"queueCount":0,"serviceAvailable":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			verdict := Classify(acc, qoderBusyError{message: message, wait: 30 * time.Second}, "qwen3.8-flash")
			class := apperrors.ClassifyUpstreamError(message)

			testutil.Equal(t, verdict.SwitchAccount, class.SwitchAccount)
			testutil.CheckFalse(t, verdict.SwitchAccount, "a refusal every account meets must not rotate the pool")
			testutil.CheckEqual(t, verdict.Status, "")
		})
	}
}

// TestGlobalRefusalSurvivesEscapedNesting is the regression test for the dead
// markers. The payload arrives as a JSON string nested inside another one, so
// the text holds `\"isQueued\":true`; searching it for `"isqueued":true` never
// matched, and the classification survived only because the bare "10605" digits
// sat in the same string. A closed gate reported without those digits was read
// as an account-level rate limit: the account was parked as "429" for 30s and
// the pool rotated, which is how the whole pool drained.
func TestGlobalRefusalSurvivesEscapedNesting(t *testing.T) {
	acc := &store.Account{ID: 22, AccountType: "qoder", Enabled: true}
	// No 10605 anywhere: the verdict has to come from the escaped flags.
	escaped := `qoder upstream rejected the credential: {"message":"{\"isQueued\":true,\"queueCount\":0,\"serviceAvailable\":false,\"waitTime\":30}"}`
	verdict := Classify(acc, qoderBusyError{message: escaped, wait: 30 * time.Second}, "qwen3.8-flash")

	testutil.Equal(t, verdict.Status, "")
	testutil.Falsef(t, verdict.Scope == ScopeAccount || verdict.Scope == ScopeCredential, "scope = %q, want a scope that does not hold the account", verdict.Scope)
	testutil.Equal(t, verdict.Cooldown, 0)
	testutil.False(t, verdict.SwitchAccount, "SwitchAccount = true; rotating multiplies one shared refusal")
}

// TestDeadCredentialStillRotates guards the other direction of the same wiring.
// The retry loop now reads the switch decision from the verdict, so a verdict
// that forgets to ask for rotation would retry a credential the upstream has
// already rejected, four times, on every request.
func TestDeadCredentialStillRotates(t *testing.T) {
	acc := &store.Account{ID: 22, AccountType: "qoder", Enabled: true}
	const refused = "upstream API error: status=401, message=invalid credential"

	verdict := Classify(acc, errors.New(refused), "qwen3.8-flash")
	class := apperrors.ClassifyUpstreamError(refused)

	testutil.Equal(t, verdict.Status, "401")
	testutil.CheckFalse(t, !verdict.SwitchAccount, "SwitchAccount = false; a refused credential must let the pool try another account")
	testutil.CheckFalse(t, !verdict.NeedsLogin, "NeedsLogin = false; waiting cannot repair a credential the upstream retired")
	testutil.Equal(t, verdict.SwitchAccount, class.SwitchAccount)
}

// TestModelCooldownKindTravelsWithTheVerdict pins the label the pool reads back
// later. The verdict is the only place that knows whether a cooled model is
// throttled or missing from the account's plan, and the selection layer sees only
// what was stored.
func TestModelCooldownKindTravelsWithTheVerdict(t *testing.T) {
	acc := &store.Account{ID: 22, AccountType: "qoder", Enabled: true}

	entitlement := Classify(acc, errors.New("qoder account has no usable plan or allowance; the model requires a subscription"), "ultimate")
	testutil.Equal(t, entitlement.Scope, ScopeModel)
	testutil.Equal(t, entitlement.ModelCooldownKind, store.ModelCooldownUnavailable)

	throttle := Classify(acc, errors.New("qoder model rate limited: code=6004"), "efficient")
	testutil.Equal(t, throttle.Scope, ScopeModel)
	testutil.Equal(t, throttle.ModelCooldownKind, store.ModelCooldownThrottled)
}
