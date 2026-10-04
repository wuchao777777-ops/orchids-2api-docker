package httpserver

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orchids-api/internal/testutil"
)

type shortWriter struct{ http.ResponseWriter }

func (w shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestWriteSSEBytesWritesEventFrame(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteSSEBytes(rec, "demo", []byte(`{"ok":true}`))

	got := rec.Body.String()
	testutil.MustContainAll(t, got, "event: demo\n", `data: {"ok":true}`)
}

func TestWriteSSEBytesPropagatesShortWrite(t *testing.T) {
	writer := shortWriter{httptest.NewRecorder()}
	err := WriteSSEBytes(writer, "demo", []byte(`{"ok":true}`))
	testutil.Falsef(t, !errors.Is(err, io.ErrShortWrite), "error=%v", err)
}

func TestStreamResponseHeadersMatchSSEProxyContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	recorder.Header().Set("Connection", "keep-alive")
	StreamResponseHeaders(recorder)
	got := recorder.Header().Get("Content-Type")
	testutil.Falsef(t, got != "text/event-stream; charset=utf-8", "content-type=%q", got)
	testutil.Falsef(t, recorder.Header().Get("X-Accel-Buffering") != "no" || recorder.Header().Get("Connection") != "", "headers=%v", recorder.Header())
}

func TestErrorCodeForStatusCoversClientAndServerStatuses(t *testing.T) {
	cases := map[int]string{
		http.StatusBadRequest:            "invalid_request",
		http.StatusUnauthorized:          "invalid_api_key",
		http.StatusForbidden:             "permission_denied",
		http.StatusNotFound:              "not_found",
		http.StatusMethodNotAllowed:      "method_not_allowed",
		http.StatusConflict:              "conflict",
		http.StatusRequestEntityTooLarge: "request_too_large",
		http.StatusUnsupportedMediaType:  "unsupported_media_type",
		http.StatusTooManyRequests:       "rate_limit_exceeded",
		http.StatusServiceUnavailable:    "service_unavailable",
		http.StatusGatewayTimeout:        "timeout",
		http.StatusInternalServerError:   "server_error",
		http.StatusTeapot:                "invalid_request",
	}
	for status, want := range cases {
		testutil.Equal(t, ErrorCodeForStatus(status), want)
	}
}

// WriteErrorCode raises a sub-400 status to 502: a caller that reached the
// error writer has failed, and a 200 carrying an error body is invisible to
// every client that only looks at the status line.
func TestWriteErrorCodeRaisesNonErrorStatusToBadGateway(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteErrorCode(rec, http.StatusOK, "boom", "something failed")
	testutil.Equal(t, rec.Code, http.StatusBadGateway)
	testutil.MustContainAll(t, rec.Body.String(), `"code":"boom"`, `"message":"something failed"`, `"param":null`)
}

func TestWriteErrorDerivesCodeFromStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusNotFound, "missing")
	testutil.Equal(t, rec.Code, http.StatusNotFound)
	testutil.MustContainAll(t, rec.Body.String(), `"code":"not_found"`, `"type":"invalid_request_error"`)
}

func TestRequireMethodKeepsPlainTextVariantSeparate(t *testing.T) {
	envelope := httptest.NewRecorder()
	testutil.False(t, RequireMethod(envelope, httptest.NewRequest(http.MethodGet, "/x", nil), http.MethodPost))
	testutil.Equal(t, envelope.Code, http.StatusMethodNotAllowed)
	testutil.MustContain(t, envelope.Body.String(), `"error"`)

	plain := httptest.NewRecorder()
	testutil.False(t, RequirePlainMethod(plain, httptest.NewRequest(http.MethodGet, "/x", nil), http.MethodPost))
	testutil.Equal(t, plain.Code, http.StatusMethodNotAllowed)
	testutil.MustNotContain(t, plain.Body.String(), `"error"`)
	testutil.MustContain(t, plain.Body.String(), "Method not allowed")
}

func TestDecodeJSONBodyRejectsNonJSONContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "text/plain")
	var payload map[string]interface{}
	testutil.False(t, DecodeJSONBody(rec, req, &payload), "DecodeJSONBody accepted a non-JSON content type")
	testutil.Equal(t, rec.Code, http.StatusUnsupportedMediaType)
}

func BenchmarkWriteSSE_Bytes(b *testing.B) {
	writer := httptest.NewRecorder()
	data := []byte(`{"ok":true}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		writer.Body.Reset()
		WriteSSEBytes(writer, "demo", data)
	}
}
