package middleware

import (
	"bytes"
	"net/http"
	"sync"
	"time"
)

// CoalesceStreamFlush keeps the first frame and terminal events immediate.
// A bounded timer flushes sparse trailing deltas even if upstream goes idle.
func CoalesceStreamFlush(interval func() time.Duration) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			delay := interval()
			if delay <= 0 {
				next(w, r)
				return
			}
			if _, ok := w.(http.Flusher); !ok {
				next(w, r)
				return
			}
			writer := &coalescedWriter{ResponseWriter: w, interval: delay}
			defer writer.close()
			next(writer, r)
		}
	}
}

type coalescedWriter struct {
	http.ResponseWriter
	mu               sync.Mutex
	interval         time.Duration
	timer            *time.Timer
	last             time.Time
	pending          int
	terminal, closed bool
}

func (w *coalescedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *coalescedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.ResponseWriter.Write(p)
	w.pending += n
	if bytes.Contains(p, []byte("[DONE]")) || bytes.Contains(p, []byte("response.completed")) || bytes.Contains(p, []byte("response.failed")) || bytes.Contains(p, []byte("message_stop")) || bytes.Contains(p, []byte("event: error")) {
		w.terminal = true
	}
	return n, err
}
func (w *coalescedWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ResponseWriter.WriteHeader(status)
}
func (w *coalescedWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	if w.last.IsZero() || w.terminal || w.pending >= 2048 || time.Since(w.last) >= w.interval {
		w.flushLocked()
		return
	}
	if w.pending > 0 && w.timer == nil {
		w.timer = time.AfterFunc(w.interval-time.Since(w.last), func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.timer = nil
			if !w.closed && w.pending > 0 {
				w.flushLocked()
			}
		})
	}
}
func (w *coalescedWriter) flushLocked() {
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	w.ResponseWriter.(http.Flusher).Flush()
	if w.pending > 0 {
		w.last = time.Now()
	}
	w.pending = 0
	w.terminal = false
}
func (w *coalescedWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if w.pending > 0 && !w.last.IsZero() {
		w.flushLocked()
	}
}
