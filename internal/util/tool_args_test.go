package util

import (
	"orchids-api/internal/testutil"
	"testing"
)

// TestNormalizeToolInputUnwrapsNestedArguments proves the OpenAI argument
// wrapping is removed before the tool dispatcher sees the input.
func TestNormalizeToolInputUnwrapsNestedArguments(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		`{"arguments":"{\"a\":1}"}`: `{"a":1}`,
		`"{\"a\":1}"`:               `{"a":1}`,
		``:                          `{}`,
		`null`:                      `{}`,
		`{"a":1}`:                   `{"a":1}`,
	}
	for input, want := range cases {
		testutil.CheckEqual(t, NormalizeToolInput(input), want)
	}
}

// TestUnwrapOpenAIArgumentsRejectsOtherShapes pins the fallback: anything that is
// not the argument envelope, or whose inner text is not JSON, reports false so
// the caller keeps the original input.
func TestUnwrapOpenAIArgumentsRejectsOtherShapes(t *testing.T) {
	t.Parallel()

	got, ok := unwrapOpenAIArguments(`{"arguments":"{\"a\":1}"}`)
	testutil.Falsef(t, !ok || got != `{"a":1}`, "unwrapOpenAIArguments() = (%q, %v), want the inner object", got, ok)
	// An argument envelope with no inner text still unwraps to an empty object.
	got, ok = unwrapOpenAIArguments(`{"arguments":"null"}`)
	testutil.Falsef(t, !ok || got != "{}", "unwrapOpenAIArguments(null text) = (%q, %v), want an empty object", got, ok)
	for name, input := range map[string]string{
		"no arguments key": `{"other":1}`,
		"not an object":    `"plain"`,
		"inner not json":   `{"arguments":"not json"}`,
		"arguments number": `{"arguments":42}`,
		"empty":            ``,
	} {
		got, ok := unwrapOpenAIArguments(input)
		testutil.Falsef(t, ok, "%s: unwrapOpenAIArguments(%q) = (%q, true), want false", name, input, got)
	}
}

// TestNormalizeToolInputDepthStopsAtTheLimit proves the recursion is bounded.
func TestNormalizeToolInputDepthStopsAtTheLimit(t *testing.T) {
	t.Parallel()

	// One layer deeper than the limit is left as-is rather than being unwrapped
	// again, so a nested payload cannot drive unbounded recursion.
	nested := `{"arguments":"{\"arguments\":\"{\\\"a\\\":1}\"}"}`
	testutil.Equal(t, normalizeToolInputDepth(nested, 0), nested)
	testutil.Equal(t, NormalizeToolInput(nested), `{"a":1}`)
}
