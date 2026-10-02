package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"orchids-api/internal/audit"
	"orchids-api/internal/testutil"
)

type recordingAuditLogger struct {
	mu     sync.Mutex
	events []audit.Event
}

func (l *recordingAuditLogger) Log(_ context.Context, event audit.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *recordingAuditLogger) snapshot() []audit.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]audit.Event(nil), l.events...)
}

// TestAdminSessionAudit_RecordsMutationsOnly keeps the journal useful: a read
// would bury the changes an operator needs to trace.
func TestAdminSessionAudit_RecordsMutationsOnly(t *testing.T) {
	logger := &recordingAuditLogger{}
	SetOperationAuditLogger(logger)
	t.Cleanup(func() { SetOperationAuditLogger(nil) })

	handler := adminSessionAudit(func(w http.ResponseWriter, r *http.Request) {
		// The handler must still receive the body after it was captured.
		if r.Body != nil {
			buf := make([]byte, 64)
			n, _ := r.Body.Read(buf)
			testutil.CheckFalse(t, n == 0 && r.Method == http.MethodPost, "handler received an empty body")
		}
		w.WriteHeader(http.StatusCreated)
	})

	readReq := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	handler(httptest.NewRecorder(), readReq)

	writeReq := httptest.NewRequest(http.MethodPut, "/api/accounts/42", strings.NewReader(`{"weight":2,"client_cookie":"sso=secret"}`))
	writeReq.Header.Set("Content-Type", "application/json")
	writeReq.AddCookie(&http.Cookie{Name: "session_token", Value: "x"})
	handler(httptest.NewRecorder(), writeReq)

	events := logger.snapshot()
	testutil.Equal(t, len(events), 1)
	event := events[0]
	testutil.Equal(t, event.Kind, audit.KindOperation)
	testutil.Falsef(t, event.Action != "accounts.42.update" && !strings.HasSuffix(event.Action, ".update"), "action = %q", event.Action)
	testutil.Equal(t, event.Status, "success")
	testutil.Equal(t, event.Actor, "admin-session")
	testutil.NotEqual(t, event.Target, "")
	testutil.MustNotContain(t, event.Details, "sso=secret")
	testutil.NotEqual(t, len(event.Redacted), 0)
}

// TestAdminSessionAudit_LoginBodyIsNeverRecorded: the login payload is the admin
// password, so it must not be summarised at all.
func TestAdminSessionAudit_LoginBodyIsNeverRecorded(t *testing.T) {
	logger := &recordingAuditLogger{}
	SetOperationAuditLogger(logger)
	t.Cleanup(func() { SetOperationAuditLogger(nil) })

	handler := adminSessionAudit(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"admin","password":"hunter2"}`))
	req.Header.Set("Content-Type", "application/json")
	handler(httptest.NewRecorder(), req)

	events := logger.snapshot()
	testutil.Equal(t, len(events), 1)
	if strings.Contains(events[0].Details, "hunter2") || events[0].Details != "" {
		t.Fatalf("login body leaked into the journal: %q", events[0].Details)
	}
}

// TestAdminSessionAudit_ErrorResult pinpoints a failed change attempt.
func TestAdminSessionAudit_ErrorResult(t *testing.T) {
	logger := &recordingAuditLogger{}
	SetOperationAuditLogger(logger)
	t.Cleanup(func() { SetOperationAuditLogger(nil) })

	handler := adminSessionAudit(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing sso token", http.StatusBadRequest)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/accounts", strings.NewReader(`{"account_type":"grok"}`))
	handler(httptest.NewRecorder(), req)

	events := logger.snapshot()
	testutil.Equal(t, len(events), 1)
	testutil.Equal(t, events[0].Status, "error")
	if code, _ := events[0].Metadata["code"].(int); code != http.StatusBadRequest {
		t.Fatalf("metadata code = %v, want 400", events[0].Metadata["code"])
	}
}

// TestOperationAction_Naming is the mechanical naming contract.
func TestOperationAction_Naming(t *testing.T) {
	cases := []struct{ method, path, want string }{
		{http.MethodPost, "/api/accounts", "accounts.create"},
		{http.MethodPut, "/api/accounts/7", "accounts.7.update"},
		{http.MethodDelete, "/api/keys/3", "keys.3.delete"},
		{http.MethodPost, "/api/login", "session.create"},
		{http.MethodPost, "/api/config", "config.create"},
		{http.MethodGet, "/api/audit", "audit.read"},
	}
	for _, tc := range cases {
		testutil.Equal(t, operationAction(tc.method, tc.path), tc.want)
	}
}

// TestOperationTarget prefers the addressed object over a query parameter.
func TestOperationTarget(t *testing.T) {
	testutil.Equal(t, operationTarget("/api/accounts/42", nil), "accounts:42")
	testutil.Equal(t, operationTarget("/api/accounts", map[string][]string{"account_id": {"9"}}), "account_id:9")
	testutil.Equal(t, operationTarget("/api/config", nil), "")
}

// TestAdminSessionAudit_LargeBodyReachesTheHandler is the regression the operator
// hit with a batch import: a 40 KB payload arrived at the handler as 32 KB and
// failed mid-parse, because the middleware read only its summary prefix from the
// live body and stitched the rest back.
func TestAdminSessionAudit_LargeBodyReachesTheHandler(t *testing.T) {
	logger := &recordingAuditLogger{}
	SetOperationAuditLogger(logger)
	t.Cleanup(func() { SetOperationAuditLogger(nil) })

	payload := strings.Repeat("x", 40_000)
	body := `{"data":"` + payload + `"}`

	var received int
	var readErr error
	handler := adminSessionAudit(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		received = len(raw)
		readErr = err
		testutil.CheckEqual(t, r.ContentLength, int64(len(body)))
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/accounts/import", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler(httptest.NewRecorder(), req)

	testutil.NoError(t, readErr, "handler failed to read the body: %v")
	testutil.Equal(t, received, len(body))
	// The summary is still bounded, and still a valid prefix of the body.
	events := logger.snapshot()
	testutil.Equal(t, len(events), 1)
	if len(events[0].Details) > 2100 {
		t.Fatalf("summary = %d bytes, want it bounded", len(events[0].Details))
	}
}

// TestAdminSessionAudit_OversizedBodyIsReported keeps a body past the hard ceiling
// from looking like a client mistake: the audit entry says what happened.
func TestAdminSessionAudit_OversizedBodyIsReported(t *testing.T) {
	logger := &recordingAuditLogger{}
	SetOperationAuditLogger(logger)
	t.Cleanup(func() { SetOperationAuditLogger(nil) })

	handler := adminSessionAudit(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	})
	oversized := strings.NewReader(`{"data":"` + strings.Repeat("y", maxCapturedBodyBytes+16) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/import", oversized)
	handler(httptest.NewRecorder(), req)

	events := logger.snapshot()
	testutil.Equal(t, len(events), 1)
	_, ok := events[0].Metadata["body_capture_error"]
	testutil.Falsef(t, !ok, "oversized body not reported: %+v", events[0].Metadata)
}
