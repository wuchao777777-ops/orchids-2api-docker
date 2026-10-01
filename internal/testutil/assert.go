// Package testutil holds the response assertions the console and provider test
// suites share, so one idiom has one implementation instead of a copy per file.
package testutil

import (
	"strings"
	"testing"
)

// NoError fails when err is not nil. The context is the call site's own
// description of the operation ("CreateModel() error ="), kept so a failure
// still names what failed instead of printing a bare error.
func NoError(t *testing.T, err error, context ...string) {
	t.Helper()
	if err == nil {
		return
	}
	t.Fatalf("%s: %v", errorContext(context), err)
}

// CheckNoError is NoError for the call sites that keep going after a failure.
func CheckNoError(t *testing.T, err error, context ...string) {
	t.Helper()
	if err == nil {
		return
	}
	t.Errorf("%s: %v", errorContext(context), err)
}

// errorContext trims the format-string tail ("= %v") the call site used to
// carry, leaving the operation description.
func errorContext(context []string) string {
	if len(context) == 0 {
		return "unexpected error"
	}
	if what := strings.TrimRight(strings.TrimSpace(context[0]), " :="); what != "" {
		return what
	}
	return "unexpected error"
}

// Equal fails unless got equals want. Both values are printed, so the failure
// still says what the code under test produced.
func Equal[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// EqualAny is Equal for the comparisons where one side is an untyped interface
// value, which type inference cannot unify with the other side.
func EqualAny(t *testing.T, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// CheckEqual is Equal for the call sites that keep going after a failure.
func CheckEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// NotEqual fails when got equals unwanted; the call sites use it to assert that
// a value changed or that a field was left empty.
func NotEqual[T comparable](t *testing.T, got, unwanted T) {
	t.Helper()
	if got == unwanted {
		t.Fatalf("got %v, want a different value", got)
	}
}

// CheckNotEqual is NotEqual for the non-fatal call sites.
func CheckNotEqual[T comparable](t *testing.T, got, unwanted T) {
	t.Helper()
	if got == unwanted {
		t.Errorf("got %v, want a different value", got)
	}
}

// CheckEqualAny is EqualAny for the non-fatal call sites.
func CheckEqualAny(t *testing.T, got, want any) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// True fails unless ok holds, keeping the call site's description of what it
// expected.
func True(t *testing.T, ok bool, context ...string) {
	t.Helper()
	if !ok {
		t.Fatalf("%s", errorContext(context))
	}
}

// CheckTrue is True for the non-fatal call sites.
func CheckTrue(t *testing.T, ok bool, context ...string) {
	t.Helper()
	if !ok {
		t.Errorf("%s", errorContext(context))
	}
}

// MustContain fails unless haystack contains needle. The whole haystack is
// quoted so a failure still shows what the code under test actually produced.
func MustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("missing %q in:\n%s", needle, haystack)
	}
}

// MustNotContain fails when haystack contains needle. Used for the leak guards:
// a credential or upstream code that reaches a response must never be present.
func MustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("unexpected %q in:\n%s", needle, haystack)
	}
}

// MustContainAll fails unless every needle is present. It replaces a chain of
// `||`-joined Contains clauses that all observe the same payload.
func MustContainAll(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			t.Fatalf("missing %q in:\n%s", needle, haystack)
		}
	}
}

// MustNotContainAny fails when any needle is present — the leak guards' shape:
// none of the secrets may appear anywhere in the payload.
func MustNotContainAny(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			t.Fatalf("unexpected %q in:\n%s", needle, haystack)
		}
	}
}

// CheckContain is MustContain for the places that keep going after a failure, so
// one broken assertion still reports the rest of the observations.
func CheckContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("missing %q in:\n%s", needle, haystack)
	}
}

// CheckNotContain is MustNotContain for the non-fatal call sites.
func CheckNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("unexpected %q in:\n%s", needle, haystack)
	}
}

// CheckContainAll is MustContainAll for the non-fatal call sites.
func CheckContainAll(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			t.Errorf("missing %q in:\n%s", needle, haystack)
		}
	}
}
