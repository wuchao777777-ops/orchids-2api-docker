package handler

import (
	"net/http"
	"net/http/httptest"
	"orchids-api/internal/testutil"
	"testing"
)

type unwrapOnlyWriter struct{ http.ResponseWriter }

func (w *unwrapOnlyWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type falseFlushWriter struct{ http.ResponseWriter }

func (w *falseFlushWriter) Flush()                      {}
func (w *falseFlushWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestResponseWriterSupportsFlushUnwrapsTheWholeChain(t *testing.T) {
	flushing := httptest.NewRecorder()
	testutil.False(t, !responseWriterSupportsFlush(&unwrapOnlyWriter{ResponseWriter: &unwrapOnlyWriter{ResponseWriter: flushing}}), "a wrapped flusher was not detected")

	nonFlushing := &plainResponseWriter{header: http.Header{}}
	testutil.False(t, responseWriterSupportsFlush(&falseFlushWriter{ResponseWriter: nonFlushing}), "a wrapper must not invent flush support when its underlying writer lacks it")
}

type plainResponseWriter struct {
	header http.Header
}

func (w *plainResponseWriter) Header() http.Header         { return w.header }
func (w *plainResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *plainResponseWriter) WriteHeader(int)             {}
