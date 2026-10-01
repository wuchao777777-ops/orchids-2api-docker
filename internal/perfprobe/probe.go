// Package perfprobe provides network-free fixtures for local performance tests.
package perfprobe

import (
	"io"
	"net/http"
	"strings"
)

type Transport struct{ Body string }

func (t Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(t.Body)), Request: r}, nil
}

const OpenAI = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
