package accountpolicy

import (
	"errors"
	"testing"
	"time"

	apperrors "orchids-api/internal/errors"
	"orchids-api/internal/store"
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
			if verdict.Status != "" {
				t.Errorf("account status = %q, want none; a shared refusal is not this account's fault", verdict.Status)
			}
			if verdict.Scope == ScopeAccount || verdict.Scope == ScopeCredential {
				t.Errorf("scope = %q, want a scope that does not hold the account", verdict.Scope)
			}
			// Nothing is persisted. A model cooldown here would take this account,
			// and then every other one, out of selection for the window, which is
			// the fail-fast this replaces.
			if verdict.Cooldown != 0 {
				t.Errorf("cooldown = %v, want 0 so no account or model state is written", verdict.Cooldown)
			}
			// Wait and retry, on the account already held.
			if !verdict.Retryable {
				t.Error("Retryable = false; a short queue window should be waited out, not failed")
			}
			if verdict.SwitchAccount {
				t.Error("SwitchAccount = true; rotating multiplies one shared refusal across every account")
			}
			if verdict.Model != "qwen3.8-flash" {
				t.Errorf("model = %q, want the reported model", verdict.Model)
			}
		})
	}
}

// TestClassifyAccountScopedRateLimitStillRotates guards the other direction: a
// genuine per-account throttle must keep its account scope and still switch, so
// the global-refusal rule cannot swallow the channels it does not describe.
func TestClassifyAccountScopedRateLimitStillRotates(t *testing.T) {
	acc := &store.Account{ID: 3, AccountType: "cline", Enabled: true}
	verdict := Classify(acc, retryAfterTestError{wait: 2 * time.Minute}, "claude-sonnet-4")

	if verdict.Status != "429" {
		t.Errorf("status = %q, want 429 for a real per-account cap", verdict.Status)
	}
	if verdict.Scope != ScopeAccount {
		t.Errorf("scope = %q, want account", verdict.Scope)
	}
	if !verdict.SwitchAccount || !verdict.Retryable {
		t.Error("a genuine per-account throttle must still switch accounts and retry")
	}
	if verdict.Cooldown != 2*time.Minute {
		t.Errorf("cooldown = %v, want the upstream hint", verdict.Cooldown)
	}
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
			if verdict.Cooldown != 0 {
				t.Errorf("cooldown = %v, want 0 regardless of the hint", verdict.Cooldown)
			}
			if verdict.Status != "" {
				t.Errorf("status = %q, want none", verdict.Status)
			}
			if !verdict.Retryable || verdict.SwitchAccount {
				t.Errorf("retryable=%v switch=%v, want a wait on the same account", verdict.Retryable, verdict.SwitchAccount)
			}
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

			if verdict.SwitchAccount != class.SwitchAccount {
				t.Fatalf("switch disagreement: policy=%v classifier=%v for %q", verdict.SwitchAccount, class.SwitchAccount, message)
			}
			if verdict.SwitchAccount {
				t.Error("a refusal every account meets must not rotate the pool")
			}
			if verdict.Status != "" {
				t.Errorf("status = %q, want none: nothing about this account is wrong", verdict.Status)
			}
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

	if verdict.Status != "" {
		t.Fatalf("status = %q, want none: an escaped closed gate is not this account's rate limit", verdict.Status)
	}
	if verdict.Scope == ScopeAccount || verdict.Scope == ScopeCredential {
		t.Fatalf("scope = %q, want a scope that does not hold the account", verdict.Scope)
	}
	if verdict.Cooldown != 0 {
		t.Fatalf("cooldown = %v, want 0 so no account or model state is written", verdict.Cooldown)
	}
	if verdict.SwitchAccount {
		t.Fatal("SwitchAccount = true; rotating multiplies one shared refusal")
	}
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

	if verdict.Status != "401" {
		t.Fatalf("status = %q, want 401", verdict.Status)
	}
	if !verdict.SwitchAccount {
		t.Error("SwitchAccount = false; a refused credential must let the pool try another account")
	}
	if !verdict.NeedsLogin {
		t.Error("NeedsLogin = false; waiting cannot repair a credential the upstream retired")
	}
	if verdict.SwitchAccount != class.SwitchAccount {
		t.Fatalf("switch disagreement: policy=%v classifier=%v", verdict.SwitchAccount, class.SwitchAccount)
	}
}

// TestModelCooldownKindTravelsWithTheVerdict pins the label the pool reads back
// later. The verdict is the only place that knows whether a cooled model is
// throttled or missing from the account's plan, and the selection layer sees only
// what was stored.
func TestModelCooldownKindTravelsWithTheVerdict(t *testing.T) {
	acc := &store.Account{ID: 22, AccountType: "qoder", Enabled: true}

	entitlement := Classify(acc, errors.New("qoder account has no usable plan or allowance; the model requires a subscription"), "ultimate")
	if entitlement.Scope != ScopeModel {
		t.Fatalf("scope = %q, want model", entitlement.Scope)
	}
	if entitlement.ModelCooldownKind != store.ModelCooldownUnavailable {
		t.Fatalf("kind = %q, want unavailable for a plan refusal", entitlement.ModelCooldownKind)
	}

	throttle := Classify(acc, errors.New("qoder model rate limited: code=6004"), "efficient")
	if throttle.Scope != ScopeModel {
		t.Fatalf("scope = %q, want model", throttle.Scope)
	}
	if throttle.ModelCooldownKind != store.ModelCooldownThrottled {
		t.Fatalf("kind = %q, want throttled for a frequency limit", throttle.ModelCooldownKind)
	}
}
