package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestGetP95_NotEnoughData(t *testing.T) {
	cl := NewConcurrencyLimiter(1, time.Second, true)
	for i := 0; i < 9; i++ {
		cl.UpdateStats(10 * time.Millisecond)
	}
	if p95 := cl.GetP95(); p95 != 0 {
		t.Fatalf("expected 0 with insufficient samples, got %d", p95)
	}
}

func TestGetP95_Computes(t *testing.T) {
	cl := NewConcurrencyLimiter(1, time.Second, true)
	for i := 0; i < 100; i++ {
		cl.UpdateStats(time.Duration(i+1) * time.Millisecond)
	}
	
	// Force the 1s throttle to expire so a recalc occurs
	time.Sleep(1100 * time.Millisecond)
	cl.UpdateStats(100 * time.Millisecond)
	
	p95 := cl.GetP95()
	if p95 < 90 || p95 > 100 {
		t.Fatalf("expected p95 near top end, got %d", p95)
	}
}

func TestLimiter_RejectsWhenBusy(t *testing.T) {
	cl := NewConcurrencyLimiter(1, 200*time.Millisecond, false)
	// tiny timeout makes acquisition fail quickly
	cl.timeout = 20 * time.Millisecond

	block := make(chan struct{})
	h := cl.Limit(func(w http.ResponseWriter, r *http.Request) {
		<-block
		w.WriteHeader(200)
	})

	// first request occupies only slot
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://x/", nil)
		h(rec, req)
	}()

	// allow goroutine to acquire slot
	time.Sleep(10 * time.Millisecond)

	// second request should be rejected due to wait timeout
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "http://x/", nil)
	h(rec2, req2)
	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec2.Code)
	}

	close(block)
	wg.Wait()
}
