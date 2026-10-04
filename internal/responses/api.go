package responses

import (
	"io"
	"strings"
)

// MaxEventBytes bounds one SSE frame. A scanner with no bound lets a single
// upstream frame allocate arbitrary memory in the gateway.
const MaxEventBytes = 8 << 20

// MaxNormalizedNumberBytes bounds a JSON number the argument normalizer will
// parse with big.Rat: past this length it is left alone rather than decoded.
const MaxNormalizedNumberBytes = 256

// ReadSSEBytes consumes whole SSE frames, including multi-line data, while
// keeping payloads as bytes. Callers that decode JSON can therefore pass the
// payload straight to json.Unmarshal without a string -> []byte round trip.
func ReadSSEBytes(reader io.Reader, consume func(string, []byte) error) error {
	return ConsumeSSE(reader, func(event SSEEvent) error {
		if !event.HasData() {
			return nil
		}
		return consume(event.Event, event.Data())
	})
}

// ReadSSE retains the string callback used by text-oriented callers.
func ReadSSE(reader io.Reader, consume func(string, string) error) error {
	return ReadSSEBytes(reader, func(event string, data []byte) error {
		return consume(event, string(data))
	})
}

// IsPrivateBuildControlEvent reports whether an event is Grok Build's private
// control traffic rather than part of the public Responses stream. It matches
// both the SSE event name and the payload `type`, because the upstream emits
// the marker in either place, so both spellings are matched.
func IsPrivateBuildControlEvent(kind string) bool {
	return strings.TrimSpace(kind) == "response.doom_loop_check"
}

// NormalizeFunctionRoot normalizes a function's parameter schema on its own,
// for callers that do not walk a whole tool declaration.
func NormalizeFunctionRoot(schema map[string]interface{}) map[string]interface{} {
	return normalizeFunctionRoot(schema)
}

// MapsEqualJSON compares two decoded JSON objects by their rendered form, so a
// normalization that only reordered keys still counts as unchanged.
func MapsEqualJSON(left, right map[string]interface{}) bool {
	return mapsEqualJSON(left, right)
}
