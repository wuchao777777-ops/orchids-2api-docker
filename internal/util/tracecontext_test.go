package util

import (
	"orchids-api/internal/testutil"
	"regexp"
	"strings"
	"testing"
)

var traceparentPattern = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)

// TestTraceparentAcceptsEveryIdentifierShape pins the property that made this a
// shared helper: the desktop clients mint request ids in different shapes, and a
// UUID carries dashes that are not hex digits. Copying the raw id into the
// header produced a value the trace-context grammar rejects, which is invisible
// until something parses it.
func TestTraceparentAcceptsEveryIdentifierShape(t *testing.T) {
	t.Parallel()

	const uuid = "6ffd0df8-6611-4e20-804d-4c535da80521"
	const hex32 = "f996b0d92767cacfdd963e636348e0bb"

	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "a UUID drops its dashes",
			input: uuid,
			want:  "00-6ffd0df866114e20804d4c535da80521-6ffd0df866114e20-01",
		},
		{
			// A 32-character hex id is already the right length: the trace id
			// must survive verbatim so an existing caller's wire value does not
			// change when the header is built here.
			name:  "a 32-character hex id passes through",
			input: hex32,
			want:  "00-f996b0d92767cacfdd963e636348e0bb-f996b0d92767cacf-01",
		},
		{
			name:  "uppercase is lowercased",
			input: strings.ToUpper(hex32),
			want:  "00-f996b0d92767cacfdd963e636348e0bb-f996b0d92767cacf-01",
		},
		{
			name:  "a short id is zero-padded",
			input: "abc",
			want:  "00-abc" + strings.Repeat("0", 29) + "-abc" + strings.Repeat("0", 13) + "-01",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Traceparent(tc.input)
			testutil.Equal(t, got, tc.want)
			if !traceparentPattern.MatchString(got) {
				t.Fatalf("Traceparent(%q) = %q, which is not a version 00 trace context", tc.input, got)
			}
			testutil.Equal(t, len(got), 55)
		})
	}

	// The same request must render the same header, and degenerate input must
	// still produce a usable one rather than a panic.
	testutil.Equal(t, Traceparent(uuid), Traceparent(uuid))
	for _, input := range []string{"", "   ", "not-a-uuid", "zzzz", "-", "0", strings.Repeat("0", 32), strings.Repeat("0", 16) + strings.Repeat("a", 16)} {
		got := Traceparent(input)
		if !traceparentPattern.MatchString(got) {
			t.Fatalf("Traceparent(%q) = %q, want a valid trace context", input, got)
		}
		parts := strings.Split(got, "-")
		if parts[1] == strings.Repeat("0", 32) || parts[2] == strings.Repeat("0", 16) {
			t.Errorf("Traceparent(%q) has a forbidden zero trace/span ID: %q", input, got)
		}
		testutil.CheckEqual(t, got, Traceparent(input))
	}
	testutil.NotEqual(t, Traceparent(""), Traceparent("zzzz"))
}
