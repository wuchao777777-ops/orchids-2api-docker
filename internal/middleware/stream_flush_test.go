package middleware

import (
	"net/http"
	"net/http/httptest"
	"orchids-api/internal/testutil"
	"sync"
	"testing"
	"time"
)

type flushRecorder struct {
	*httptest.ResponseRecorder
	mu      sync.Mutex
	flushes int
	signal  chan struct{}
}

func (w *flushRecorder) Flush() {
	w.mu.Lock()
	w.flushes++
	w.mu.Unlock()
	if w.signal != nil {
		select {
		case w.signal <- struct{}{}:
		default:
		}
	}
}
func TestCoalescedFlushKeepsFirstTerminalAndFinalDrain(t *testing.T) {
	target := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	writer := &coalescedWriter{ResponseWriter: target, interval: time.Hour}
	writer.Write([]byte("data: first\n\n"))
	writer.Flush()
	for i := 0; i < 10; i++ {
		writer.Write([]byte("data: delta\n\n"))
		writer.Flush()
	}
	testutil.Equal(t, target.flushes, 1)
	writer.Write([]byte("data: [DONE]\n\n"))
	writer.Flush()
	if target.flushes != 2 {
		t.Fatal("terminal not immediate")
	}
	writer.Write([]byte("data: trailing\n\n"))
	writer.Flush()
	writer.close()
	if target.flushes != 3 {
		t.Fatal("trailing data not drained")
	}
}
func TestCoalescedFlushDoesNotHoldSparseDeltaUntilNextToken(t *testing.T) {
	target := &flushRecorder{ResponseRecorder: httptest.NewRecorder(), signal: make(chan struct{}, 4)}
	writer := &coalescedWriter{ResponseWriter: target, interval: 2 * time.Millisecond}
	defer writer.close()
	writer.Write([]byte("data: first\n\n"))
	writer.Flush()
	<-target.signal
	writer.Write([]byte("data: second\n\n"))
	writer.Flush()
	select {
	case <-target.signal:
	case <-time.After(time.Second):
		t.Fatal("sparse delta stuck")
	}
	writer.close()
	target.mu.Lock()
	before := target.flushes
	target.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	target.mu.Lock()
	defer target.mu.Unlock()
	if target.flushes != before {
		t.Fatal("flush after handler return")
	}
}
func TestCoalescingLeavesNonStreamingResponseUnchanged(t *testing.T) {
	rec := httptest.NewRecorder()
	h := CoalesceStreamFlush(func() time.Duration { return time.Millisecond })(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400); w.Write([]byte("bad")) })
	h(rec, httptest.NewRequest("POST", "/", nil))
	if rec.Code != 400 || rec.Body.String() != "bad" {
		t.Fatalf("response %d %s", rec.Code, rec.Body.String())
	}
}
