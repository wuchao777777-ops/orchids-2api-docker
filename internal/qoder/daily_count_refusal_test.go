package qoder

import (
	"errors"
	"orchids-api/internal/testutil"
	"strconv"
	"strings"
	"testing"
)

// The gateway reports an exhausted daily request count under the same 401/403
// envelope it uses for a rejected credential. Reading it as unauthorized made the
// client rotate the account's OAuth token and replay the request for a credential
// that was never the problem — production logged 13 such refreshes in a day —
// and could retire a working account when the refresh itself failed.
func TestDailyCountRefusalIsNotACredentialRejection(t *testing.T) {
	const body = `{"code":112,"message":"Billing daily count exceeded"}`

	for _, status := range []int{401, 403, 500} {
		err := classifyStatus(status, "", []byte(body))
		testutil.Error(t, err, "status %d: expected an error")
		testutil.Falsef(t, isUnauthorized(err), "status %d: daily-count refusal reported as a credential rejection: %v", status, err)
		testutil.Falsef(t, !errors.Is(err, ErrDailyCountExceeded), "status %d: err = %v, want ErrDailyCountExceeded", status, err)
		// The shared classifiers and the account policy both key off this phrase,
		// so it has to survive the wrap that carries the sentinel.
		testutil.MustContain(t, strings.ToLower(err.Error()), "billing daily count exceeded")
	}
}

// The same refusal delivered inside a 200 SSE envelope takes the same route.
func TestStreamDailyCountRefusalIsNotACredentialRejection(t *testing.T) {
	t.Parallel()

	body := "data: " + `{"statusCodeValue":401,"body":` + strconv.Quote(`{"code":112,"message":"Billing daily count exceeded"}`) + `}` + "\n\n"
	_, _, err := collectStream(t, body)
	testutil.False(t, err == nil, "consumeStream() error = nil for an error envelope")
	testutil.Falsef(t, errors.Is(err, errUpstreamUnauthorized), "daily-count refusal was misclassified as unauthorized: %v", err)
	testutil.Falsef(t, !errors.Is(err, ErrDailyCountExceeded), "error = %v, want ErrDailyCountExceeded", err)
	testutil.MustContain(t, strings.ToLower(err.Error()), "billing daily count exceeded")
}

// A genuine credential rejection must keep its meaning: the split above only
// moves the daily-count verdict, it does not weaken authentication handling.
func TestGenuineCredentialRejectionStaysUnauthorized(t *testing.T) {
	err := classifyStatus(401, "", []byte(`{"message":"invalid access token"}`))
	testutil.True(t, isUnauthorized(err), "err = %v, want a credential rejection")
	testutil.Falsef(t, errors.Is(err, ErrDailyCountExceeded), "err = %v, want no daily-count verdict", err)
}
