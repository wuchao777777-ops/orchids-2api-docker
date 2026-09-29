package store

import (
	"encoding/json"
	"testing"
	"time"
)

// TestModelCooldown_ScopedToModelNotAccount is the rule the pool depends on: a
// throttled model must be skipped for that model while the account stays usable
// for every other model.
func TestModelCooldown_ScopedToModelNotAccount(t *testing.T) {
	now := time.Now()
	acc := &Account{
		AccountType: "grok",
		ModelCooldowns: map[string]time.Time{
			"grok-4.6":     now.Add(90 * time.Second),
			"grok-heavy":   now.Add(time.Hour),
			"grok-expired": now.Add(-time.Minute),
		},
	}

	if got := ModelCooldownRemaining(acc, "grok-4.6", now); got <= 0 {
		t.Fatalf("throttled model remaining = %v, want > 0", got)
	}
	if got := ModelCooldownRemaining(acc, "grok-4.5", now); got != 0 {
		t.Fatalf("untouched model remaining = %v, want 0", got)
	}
	if got := ModelCooldownRemaining(acc, "grok-expired", now); got != 0 {
		t.Fatalf("expired cooldown remaining = %v, want 0", got)
	}
	if got := ModelCooldownRemaining(nil, "grok-4.6", now); got != 0 {
		t.Fatalf("nil account remaining = %v, want 0", got)
	}
}

// TestRecordModelCooldown_SetsOnlyThatModel keeps the write path honest: a
// throttle for one model must never mark the account or another model.
func TestRecordModelCooldown_SetsOnlyThatModel(t *testing.T) {
	now := time.Now()
	acc := &Account{AccountType: "grok"}

	RecordModelCooldown(acc, "grok-4.6", now.Add(time.Minute))
	if remaining := ModelCooldownRemaining(acc, "grok-4.6", now); remaining <= 0 {
		t.Fatal("the throttled model must be marked")
	}
	if remaining := ModelCooldownRemaining(acc, "grok-4.5", now); remaining != 0 {
		t.Fatal("an unrelated model must stay available")
	}
	if acc.StatusCode != "" {
		t.Fatalf("a model-scoped throttle must not set the account status, got %q", acc.StatusCode)
	}

	// A later deadline wins; an earlier one does not shorten it.
	RecordModelCooldown(acc, "grok-4.6", now.Add(5*time.Minute))
	if got := ModelCooldownRemaining(acc, "grok-4.6", now); got < 4*time.Minute {
		t.Fatalf("remaining = %v, want the later deadline", got)
	}
	RecordModelCooldown(acc, "grok-4.6", now.Add(10*time.Second))
	if got := ModelCooldownRemaining(acc, "grok-4.6", now); got < 4*time.Minute {
		t.Fatalf("an earlier deadline shortened the cooldown to %v", got)
	}

	// A past deadline is not recorded at all.
	before := len(acc.ModelCooldowns)
	RecordModelCooldown(acc, "grok-4.7", now.Add(-time.Minute))
	if len(acc.ModelCooldowns) != before {
		t.Fatal("an expired deadline must not be recorded")
	}
	RecordModelCooldown(acc, "  ", now.Add(time.Minute))
	if len(acc.ModelCooldowns) != before {
		t.Fatal("an empty model name must not be recorded")
	}
}

// TestMergeModelCooldowns_KeepsLatestAndDropsExpired covers the write path: a
// partial update must not erase a cooldown another path just recorded, and dead
// entries must not accumulate.
func TestMergeModelCooldowns_KeepsLatestAndDropsExpired(t *testing.T) {
	now := time.Now()
	merged := mergeModelCooldowns(
		map[string]time.Time{
			"grok-4.6":   now.Add(30 * time.Second),
			"grok-stale": now.Add(-time.Hour),
		},
		map[string]time.Time{
			"grok-4.6": now.Add(5 * time.Minute),
			"grok-4.5": now.Add(time.Minute),
			"":         now.Add(time.Hour),
		},
	)
	if len(merged) != 2 {
		t.Fatalf("merged = %v, want two live models", merged)
	}
	if merged["grok-4.6"].Sub(now) < 4*time.Minute {
		t.Fatalf("grok-4.6 = %v, want the later deadline", merged["grok-4.6"].Sub(now))
	}
	if _, dead := merged["grok-stale"]; dead {
		t.Fatal("an expired cooldown must not be carried forward")
	}
	if mergeModelCooldowns(nil, nil) != nil {
		t.Fatal("empty input must not create an empty map")
	}
}

// TestUpdateAccount_PreservesModelCooldownsAcrossPartialWrites pins the guard:
// saving an unrelated field (enabled, weight) keeps cooldowns in Redis.
func TestUpdateAccount_PreservesModelCooldownsAcrossPartialWrites(t *testing.T) {
	s, _ := newTestRedisStore(t, "modelcool:")

	ctx := t.Context()
	acc := &Account{
		AccountType: "grok",
		Enabled:     true,
		ModelCooldowns: map[string]time.Time{
			"grok-4.6": time.Now().Add(2 * time.Minute),
		},
	}
	if err := s.CreateAccount(ctx, acc); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	// A request path that knows nothing about model cooldowns saves the account.
	stale, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	stale.Weight = 3
	stale.ModelCooldowns = nil
	if err := s.UpdateAccount(ctx, stale); err != nil {
		t.Fatalf("UpdateAccount() error = %v", err)
	}

	after, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if after.Weight != 3 {
		t.Fatalf("weight = %d, want the partial update applied", after.Weight)
	}
	if remaining := ModelCooldownRemaining(after, "grok-4.6", time.Now()); remaining <= 0 {
		t.Fatal("a partial write erased the model cooldown")
	}
}

// TestModelCooldownKind_SeparatesPlanFromThrottle pins what the selection layer
// reads. The pool can see a cooled model but not the verdict behind it, so the
// label decides whether an emptied pool invites a retry or reports a model no
// matching account can serve.
func TestModelCooldownKind_SeparatesPlanFromThrottle(t *testing.T) {
	now := time.Now()
	acc := &Account{AccountType: "qoder"}
	RecordModelCooldownWithReason(acc, "glm-5.3", now.Add(24*time.Hour), ModelCooldownUnavailable)
	RecordModelCooldownWithReason(acc, "efficient", now.Add(30*time.Second), ModelCooldownThrottled)

	if got := ModelCooldownKind(acc, "glm-5.3", now); got != ModelCooldownUnavailable {
		t.Fatalf("glm-5.3 kind = %q, want unavailable", got)
	}
	if got := ModelCooldownKind(acc, "efficient", now); got != ModelCooldownThrottled {
		t.Fatalf("efficient kind = %q, want throttled", got)
	}
	if got := ModelCooldownKind(acc, "untouched", now); got != "" {
		t.Fatalf("untouched model kind = %q, want none", got)
	}
	if got := ModelCooldownKind(nil, "glm-5.3", now); got != "" {
		t.Fatalf("nil account kind = %q, want none", got)
	}

	// A deadline recorded before reasons were stored is read from the deadline:
	// a day-long hold cannot be the 30s throttle.
	legacy := &Account{ModelCooldowns: map[string]time.Time{
		"day-long": now.Add(27 * time.Hour),
		"short":    now.Add(20 * time.Second),
	}}
	if got := ModelCooldownKind(legacy, "day-long", now); got != ModelCooldownUnavailable {
		t.Fatalf("legacy long hold kind = %q, want unavailable", got)
	}
	if got := ModelCooldownKind(legacy, "short", now); got != ModelCooldownThrottled {
		t.Fatalf("legacy short hold kind = %q, want throttled", got)
	}

	// An expired cooldown is not cooling down at all, label or not.
	expired := &Account{
		ModelCooldowns:       map[string]time.Time{"gone": now.Add(-time.Minute)},
		ModelCooldownReasons: map[string]ModelCooldownReason{"gone": ModelCooldownUnavailable},
	}
	if got := ModelCooldownKind(expired, "gone", now); got != "" {
		t.Fatalf("expired kind = %q, want none", got)
	}
}

// TestRecordModelCooldownWithReason_KeepsTheStoredVerdict pins that a shorter
// hold cannot relabel a longer one: a 30s throttle arriving after a day-long plan
// verdict must not turn it back into "retry later".
func TestRecordModelCooldownWithReason_KeepsTheStoredVerdict(t *testing.T) {
	now := time.Now()
	acc := &Account{AccountType: "qoder"}

	RecordModelCooldownWithReason(acc, "glm-5.3", now.Add(24*time.Hour), ModelCooldownUnavailable)
	RecordModelCooldown(acc, "glm-5.3", now.Add(30*time.Second))
	if got := ModelCooldownKind(acc, "glm-5.3", now); got != ModelCooldownUnavailable {
		t.Fatalf("kind = %q, want the day-long verdict to survive a shorter throttle", got)
	}

	RecordModelCooldown(acc, "efficient", now.Add(30*time.Second))
	RecordModelCooldownWithReason(acc, "efficient", now.Add(24*time.Hour), ModelCooldownUnavailable)
	if got := ModelCooldownKind(acc, "efficient", now); got != ModelCooldownUnavailable {
		t.Fatalf("kind = %q, want the later verdict to win", got)
	}

	// An unrecognised label falls back to the deadline rather than being trusted.
	odd := &Account{
		ModelCooldowns:       map[string]time.Time{"m": now.Add(24 * time.Hour)},
		ModelCooldownReasons: map[string]ModelCooldownReason{"m": "something-else"},
	}
	if got := ModelCooldownKind(odd, "m", now); got != ModelCooldownUnavailable {
		t.Fatalf("kind = %q, want the deadline fallback for an unknown label", got)
	}
}

// TestMergeModelCooldownReasons_FollowTheirDeadline keeps labels attached to the
// deadline they describe, and drops the ones whose deadline is gone.
func TestMergeModelCooldownReasons_FollowTheirDeadline(t *testing.T) {
	now := time.Now()

	// The stored deadline outlasts the incoming one, so the stored label stands.
	merged := mergeModelCooldownReasons(
		map[string]ModelCooldownReason{"m": ModelCooldownUnavailable},
		map[string]ModelCooldownReason{"m": ModelCooldownThrottled},
		map[string]time.Time{"m": now.Add(24 * time.Hour)},
		map[string]time.Time{"m": now.Add(30 * time.Second)},
	)
	if merged["m"] != ModelCooldownUnavailable {
		t.Fatalf("merged = %q, want the stored verdict", merged["m"])
	}

	// The incoming deadline wins, so its label does too.
	merged = mergeModelCooldownReasons(
		map[string]ModelCooldownReason{"m": ModelCooldownThrottled},
		map[string]ModelCooldownReason{"m": ModelCooldownUnavailable},
		map[string]time.Time{"m": now.Add(30 * time.Second)},
		map[string]time.Time{"m": now.Add(24 * time.Hour)},
	)
	if merged["m"] != ModelCooldownUnavailable {
		t.Fatalf("merged = %q, want the incoming verdict", merged["m"])
	}

	if got := mergeModelCooldownReasons(
		map[string]ModelCooldownReason{"dead": ModelCooldownUnavailable},
		nil,
		map[string]time.Time{"dead": now.Add(-time.Minute)},
		nil,
	); got != nil {
		t.Fatalf("merged = %v, want nil: a label must not outlive its deadline", got)
	}
	if got := mergeModelCooldownReasons(nil, nil, nil, nil); got != nil {
		t.Fatalf("merged = %v, want nil for empty input", got)
	}
}

// TestModelCooldownReasonsSurviveRedis covers the persistence contract: the label
// rides along with the deadline through partial writes, and the deadline map keeps
// the shape earlier binaries wrote and read, so a rollback cannot turn a cooled
// account into an undecodable document.
func TestModelCooldownReasonsSurviveRedis(t *testing.T) {
	s, _ := newTestRedisStore(t, "modelcoolreason:")
	ctx := t.Context()

	acc := &Account{AccountType: "qoder", Enabled: true}
	RecordModelCooldownWithReason(acc, "glm-5.3", time.Now().Add(24*time.Hour), ModelCooldownUnavailable)
	if err := s.CreateAccount(ctx, acc); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	// A path that knows nothing about cooldowns saves the account.
	stale, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	stale.Weight = 2
	stale.ModelCooldowns = nil
	stale.ModelCooldownReasons = nil
	if err := s.UpdateAccount(ctx, stale); err != nil {
		t.Fatalf("UpdateAccount() error = %v", err)
	}

	after, err := s.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if got := ModelCooldownKind(after, "glm-5.3", time.Now()); got != ModelCooldownUnavailable {
		t.Fatalf("kind = %q, want the label to survive a partial write", got)
	}

	// The deadline map still decodes exactly as an earlier binary wrote it.
	var legacy Account
	if err := json.Unmarshal([]byte(`{"id":9,"account_type":"qoder","model_cooldowns":{"m":"2030-01-01T00:00:00Z"}}`), &legacy); err != nil {
		t.Fatalf("legacy account document no longer decodes: %v", err)
	}
	if len(legacy.ModelCooldowns) != 1 {
		t.Fatalf("legacy cooldowns = %v, want the stored deadline", legacy.ModelCooldowns)
	}

	// And the other direction: an earlier binary reading a document this build
	// wrote. It knows only the deadline map, and encoding/json skips the sibling
	// field it does not define, which is what makes a rollback safe.
	raw, err := json.Marshal(after)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var previousBuild struct {
		ModelCooldowns map[string]time.Time `json:"model_cooldowns,omitempty"`
	}
	if err := json.Unmarshal(raw, &previousBuild); err != nil {
		t.Fatalf("a build that only knows model_cooldowns cannot read this document: %v", err)
	}
	if len(previousBuild.ModelCooldowns) != 1 {
		t.Fatalf("previous build read cooldowns = %v, want the stored deadline", previousBuild.ModelCooldowns)
	}
}
