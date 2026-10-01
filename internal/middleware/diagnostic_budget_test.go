package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"orchids-api/internal/debug"
)

func TestDiagnosticSamplingAndConcurrentBudget(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	store := debug.NewDiagnosticStore(client, "budget:")
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := Diagnostics(store, func() bool { return true }, func() DiagnosticBudget { return DiagnosticBudget{SampleEvery: 2, MaxConcurrent: 1} })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if debug.FromContext(r.Context()) != nil {
			close(entered)
			<-release
		}
		w.WriteHeader(http.StatusOK)
	}))
	first := httptest.NewRecorder()
	go func() { handler.ServeHTTP(first, httptest.NewRequest("POST", "/v1/responses", nil)); close(finished) }()
	<-entered
	second, third := httptest.NewRecorder(), httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest("POST", "/v1/responses", nil))
	handler.ServeHTTP(third, httptest.NewRequest("POST", "/v1/responses", nil))
	if second.Header().Get("X-Diagnostic-Capture") != "sampled-out" || third.Header().Get("X-Diagnostic-Capture") != "budget-exhausted" {
		t.Fatal("diagnostic budget did not skip captures")
	}
	close(release)
	<-finished
	if first.Code != http.StatusOK || first.Header().Get("X-Diagnostic-Capture") != "enabled" {
		t.Fatal("capture changed response")
	}
}
