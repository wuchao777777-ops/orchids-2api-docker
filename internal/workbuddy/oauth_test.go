package workbuddy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

func TestStartAuthLogin_ClassifiesUnreachable(t *testing.T) {
	t.Parallel()

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := dead.URL
	dead.Close()

	client := NewFromAccount(&store.Account{}, nil)
	client.baseURL = base

	_, _, err := client.StartAuthLogin(context.Background(), "5.5.2")
	if err == nil {
		t.Fatal("StartAuthLogin() error = nil, want a failure")
	}
	if !errors.Is(err, ErrAuthUnavailable) {
		t.Fatalf("error = %v, want ErrAuthUnavailable", err)
	}
}

func TestProbeReachability_DetectsBlockedEgress(t *testing.T) {
	t.Parallel()

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := dead.URL
	dead.Close()

	client := NewFromAccount(&store.Account{}, nil)
	client.baseURL = base
	if err := client.ProbeReachability(context.Background()); err == nil {
		t.Fatal("ProbeReachability() = nil, want a reachability failure")
	}

	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer live.Close()
	client.baseURL = live.URL
	testutil.NoError(t, client.ProbeReachability(context.Background()), "ProbeReachability() error = %v, want success")
}

func TestStartAuthLogin_ClassifiesRejection(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":40301,"msg":"forbidden"}`))
	}))
	defer srv.Close()

	client := NewFromAccount(&store.Account{}, nil)
	client.baseURL = srv.URL

	_, _, err := client.StartAuthLogin(context.Background(), "")
	if err == nil {
		t.Fatal("StartAuthLogin() error = nil, want a failure")
	}
	if !errors.Is(err, ErrAuthRejected) {
		t.Fatalf("error = %v, want ErrAuthRejected", err)
	}
}

func TestStartAuthLogin_RejectsForeignLoginHost(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"state":"s","authUrl":"https://evil.example.com/login?state=s"}}`))
	}))
	defer srv.Close()

	client := NewFromAccount(&store.Account{}, nil)
	client.baseURL = srv.URL

	_, _, err := client.StartAuthLogin(context.Background(), "")
	if err == nil {
		t.Fatal("StartAuthLogin() accepted a foreign login host")
	}
	if !errors.Is(err, ErrAuthRejected) {
		t.Fatalf("error = %v, want ErrAuthRejected", err)
	}
}

func TestStartAuthLogin_AppendsClientVersion(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.CheckEqual(t, r.Method, http.MethodPost)
		testutil.CheckEqual(t, r.URL.Query().Get("platform"), "workbuddy-ai")
		_, _ = w.Write([]byte(`{"code":0,"data":{"state":"state-1","authUrl":"https://www.workbuddy.ai/login?platform=workbuddy-ai&state=state-1"}}`))
	}))
	defer srv.Close()

	client := NewFromAccount(&store.Account{}, nil)
	client.baseURL = srv.URL

	state, authURL, err := client.StartAuthLogin(context.Background(), "5.5.2")
	if err != nil {
		t.Fatalf("StartAuthLogin() error = %v", err)
	}
	testutil.Equal(t, state, "state-1")
	testutil.MustContain(t, authURL, "version=5.5.2")
}
