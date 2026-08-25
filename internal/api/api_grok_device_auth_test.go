package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleGrokDeviceAuthorizationStatusRedactsDeviceCode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &API{grokDeviceLogins: map[string]*grokDeviceLogin{
		"login-id": {deviceCode: "must-not-be-exposed", userCode: "ABCD-1234", verifyURI: "https://auth.x.ai/device", expiresAt: time.Now().Add(time.Minute), status: "pending", cancel: cancel},
	}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/grok/device-auth/login-id", nil).WithContext(ctx)
	a.HandleGrokDeviceAuthorization(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "must-not-be-exposed") || !strings.Contains(body, "ABCD-1234") {
		t.Fatalf("unexpected response: %s", body)
	}
}

func TestHandleGrokDeviceAuthorizationDeleteCancelsAndForgets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &API{grokDeviceLogins: map[string]*grokDeviceLogin{
		"login-id": {deviceCode: "device-secret", expiresAt: time.Now().Add(time.Minute), status: "pending", cancel: cancel},
	}}
	rec := httptest.NewRecorder()
	a.HandleGrokDeviceAuthorization(rec, httptest.NewRequest(http.MethodDelete, "/api/grok/device-auth/login-id", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := a.grokDeviceLogins["login-id"]; ok {
		t.Fatal("cancelled login remained in memory")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel function was not called")
	}
}
