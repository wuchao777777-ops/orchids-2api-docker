package qoder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"orchids-api/internal/testutil"
	"regexp"
	"strings"
	"testing"
	"time"
)

// traceparentRE is the W3C Trace Context shape for version 00: the version, a
// 32-character lowercase hex trace id, a 16-character hex parent span id and
// the sampled flag. The value itself is built by util.Traceparent, which owns
// the shape; these tests only prove the channels send it.
var traceparentRE = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)

func validTraceparent(value string) bool { return traceparentRE.MatchString(strings.TrimSpace(value)) }

// TestCatalogFetchCarriesTheClientHeadersToo pins the trace context and the
// negotiation headers on the catalog read. The capture carries
// accept-language, sec-fetch-mode and a trace context on the model-list GET as
// well as on inference, so this is a property of the client rather than of the
// chat path. The gateway adopts the trace id and answers with it as
// sw-trace-id, which is what makes a request correlatable upstream-side.
func TestCatalogFetchCarriesTheClientHeadersToo(t *testing.T) {
	t.Parallel()

	headersCh := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headersCh <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(observedCatalogResponse))
	}))
	defer server.Close()

	acc := signedTestAccount()
	client := NewFromAccount(acc, nil)
	setTestEndpoints(client, server.URL, server.URL, server.URL)

	_, err := client.FetchUpstreamModels(context.Background())
	testutil.CheckNoError(t, err)

	select {
	case headers := <-headersCh:
		for name, want := range map[string]string{
			"Accept-Language": "*",
			"Sec-Fetch-Mode":  "cors",
		} {
			testutil.CheckEqual(t, headers.Get(name), want)
		}
		trace := headers.Get("Traceparent")
		testutil.CheckFalsef(t, !validTraceparent(trace), "catalog Traceparent = %q, want a version 00 trace context", trace)
	case <-time.After(2 * time.Second):
		t.Fatal("the stub server received no catalog request")
	}
}
