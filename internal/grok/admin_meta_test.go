package grok

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"orchids-api/internal/config"
)

func TestHandleAdminVerify(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/verify", nil)
	rec := httptest.NewRecorder()
	h.HandleAdminVerify(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d", rec.Code, http.StatusOK)
	}
}

func TestHandleAdminStorage(t *testing.T) {
	h := &Handler{cfg: &config.Config{StoreMode: "redis"}}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/storage", nil)
	rec := httptest.NewRecorder()
	h.HandleAdminStorage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d", rec.Code, http.StatusOK)
	}
}

func TestHandleAdminVoiceTokenRejectsGET(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/voice/token?voice=ara", nil)
	rec := httptest.NewRecorder()

	h.HandleAdminVoiceToken(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want=%d", rec.Code, http.StatusMethodNotAllowed)
	}
}
