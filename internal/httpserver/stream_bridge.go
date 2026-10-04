package httpserver

import (
	"context"
	"io"
	"net/http"
	"sync"
)

// CheckedStreamWriter makes write failures visible to translators whose SSE
// helpers predate error returns. The caller then closes its pipe and cancels
// the upstream request instead of leaving a blocked producer behind.
type CheckedStreamWriter struct {
	Target io.Writer
	Err    error
}

// NewCheckedStreamWriter wraps target, recording the first write failure.
func NewCheckedStreamWriter(target io.Writer) *CheckedStreamWriter {
	return &CheckedStreamWriter{Target: target}
}

// Write forwards the write and remembers a failure or a short write.
func (w *CheckedStreamWriter) Write(data []byte) (int, error) {
	if w.Err != nil {
		return 0, w.Err
	}
	n, err := w.Target.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	w.Err = err
	return n, err
}

// Flush flushes the target when it supports it.
func (w *CheckedStreamWriter) Flush() {
	if f, ok := w.Target.(http.Flusher); ok {
		f.Flush()
	}
}

type streamingChatWriter struct {
	header          http.Header
	committedHeader http.Header
	pipe            *io.PipeWriter
	status          int
	once            sync.Once
	ready           chan struct{}
}

func newStreamingChatWriter(pipe *io.PipeWriter) *streamingChatWriter {
	return &streamingChatWriter{header: make(http.Header), pipe: pipe, ready: make(chan struct{})}
}

func (w *streamingChatWriter) Header() http.Header { return w.header }
func (w *streamingChatWriter) WriteHeader(status int) {
	w.once.Do(func() { w.status = status; w.committedHeader = w.header.Clone(); close(w.ready) })
}
func (w *streamingChatWriter) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.pipe.Write(data)
}
func (w *streamingChatWriter) Flush() { w.WriteHeader(http.StatusOK) }

// StreamThroughChat runs a chat handler with a streaming ResponseWriter and
// hands the status, the committed headers and the body reader to consume. The
// producer is cancelled when consume returns, so a client disconnect stops the
// upstream.
func StreamThroughChat(req *http.Request, chat http.HandlerFunc, consume func(int, http.Header, io.Reader)) {
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	streamWriter := newStreamingChatWriter(writer)
	go func() {
		defer writer.Close()
		chat(streamWriter, req.Clone(ctx))
		streamWriter.WriteHeader(http.StatusOK)
	}()
	select {
	case <-streamWriter.ready:
		consume(streamWriter.status, streamWriter.committedHeader, reader)
	case <-ctx.Done():
	}
}
