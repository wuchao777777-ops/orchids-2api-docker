package util

// Traceparent renders a W3C Trace Context header value for one request.
//
// The value is four dash-separated fields: version 00, a 32-character lowercase
// hex trace id, a 16-character hex parent span id, and the flags byte 01
// (sampled).
//
// Callers pass whatever identifier they already minted for the request, and the
// characters that are not hex digits are dropped, so the result is a well-formed
// header whatever the input shape. A UUID is the awkward case and the reason
// this is shared: its dashes are not hex digits, so copying the raw id into the
// header produces a value the trace-context grammar rejects.
//
// The trace id is the first 32 hex characters, zero-padded when the identifier
// carries fewer. The parent span reuses the leading 16 so the value stays
// deterministic — a trace can be reconstructed from the request id alone
// instead of being logged alongside it.
func Traceparent(requestID string) string {
	digits := make([]byte, 0, 32)
	for i := 0; i < len(requestID) && len(digits) < 32; i++ {
		switch char := requestID[i]; {
		case char >= '0' && char <= '9', char >= 'a' && char <= 'f':
			digits = append(digits, char)
		case char >= 'A' && char <= 'F':
			// The trace-context grammar is lowercase hex.
			digits = append(digits, char+('a'-'A'))
		}
	}
	for len(digits) < 32 {
		digits = append(digits, '0')
	}
	traceID := string(digits)
	return "00-" + traceID + "-" + traceID[:16] + "-01"
}
