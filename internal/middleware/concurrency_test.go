package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterRejectsImmediatelyWhenBusyWithOpenAIError(t *testing.T) {
	cl := NewConcurrencyLimiter(1, time.Second)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	h := cl.Limit(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})

	go func() {
		defer close(done)
		h(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://x/", nil))
	}()
	<-entered

	recorder := httptest.NewRecorder()
	start := time.Now()
	h(recorder, httptest.NewRequest(http.MethodGet, "http://x/", nil))
	elapsed := time.Since(start)

	if elapsed >= 100*time.Millisecond {
		t.Fatalf("overloaded request waited %s; want immediate rejection", elapsed)
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want=%d", recorder.Code, http.StatusServiceUnavailable)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q want application/json", got)
	}
	if got := recorder.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After=%q want 1", got)
	}
	var envelope struct {
		Error struct {
			Message string  `json:"message"`
			Type    string  `json:"type"`
			Code    string  `json:"code"`
			Param   *string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v; body=%q", err, recorder.Body.String())
	}
	if envelope.Error.Message != "server is overloaded; retry later" ||
		envelope.Error.Type != "server_error" ||
		envelope.Error.Code != "server_overloaded" || envelope.Error.Param != nil {
		t.Fatalf("unexpected OpenAI error envelope: %+v", envelope.Error)
	}
	if got := atomic.LoadInt64(&cl.rejectedReqs); got != 1 {
		t.Fatalf("rejectedReqs=%d want=1", got)
	}

	close(release)
	<-done
}

func TestLimitPreservesExecutionTimeout(t *testing.T) {
	cl := NewConcurrencyLimiter(1, 20*time.Millisecond)
	contextErr := make(chan error, 1)
	h := cl.Limit(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		contextErr <- r.Context().Err()
		w.WriteHeader(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	h(recorder, httptest.NewRequest(http.MethodGet, "http://x/", nil))
	if got := <-contextErr; got == nil {
		t.Fatal("ordinary limiter did not apply execution timeout")
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d want=%d", recorder.Code, http.StatusNoContent)
	}
}
