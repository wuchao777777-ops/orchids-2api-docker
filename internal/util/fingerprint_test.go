package util

import (
	"orchids-api/internal/testutil"
	"testing"
)

// TestFingerprint_StableAndNonReversible pins the two properties the account
// table relies on: the same secret always maps to the same short id, different
// secrets map to different ids, and the digest never contains the secret.
func TestFingerprint_StableAndNonReversible(t *testing.T) {
	token := "workbuddy-session-token-abcdef"
	first := Fingerprint(token)
	second := Fingerprint("  " + token + "  ")
	if first == "" || len(first) != 12 {
		t.Fatalf("Fingerprint() = %q, want 12 hex characters", first)
	}
	testutil.Equal(t, first, second)
	testutil.NotEqual(t, Fingerprint("workbuddy-session-token-abcdeg"), first)
	if Fingerprint("") != "" {
		t.Fatal("an empty secret has no fingerprint")
	}
}
