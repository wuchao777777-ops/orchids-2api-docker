package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orchids-api/internal/config"
	"orchids-api/internal/testutil"
)

func TestHandleLogin_SecureCookieDependsOnHTTPS(t *testing.T) {
	cfg := &config.Config{AdminUser: "admin", AdminPass: "pass"}
	a := &API{adminUser: "admin", adminPass: "pass"}
	a.config.Store(cfg)

	body := []byte(`{"username":"admin","password":"pass"}`)

	// HTTP: should not set Secure
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://example.com/api/login", bytes.NewReader(body))
		a.HandleLogin(rec, req)
		set := rec.Header().Get("Set-Cookie")
		testutil.NotEqual(t, set, "")
		testutil.MustNotContain(t, strings.ToLower(set), "secure")
	}

	// Proxy HTTPS via X-Forwarded-Proto: should set Secure
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://example.com/api/login", bytes.NewReader(body))
		req.Header.Set("X-Forwarded-Proto", "https")
		a.HandleLogin(rec, req)
		set := rec.Header().Get("Set-Cookie")
		testutil.MustContain(t, strings.ToLower(set), "secure")
	}
}

func TestHandleLogin_UsesUpdatedConfigCredentials(t *testing.T) {
	cfg := &config.Config{AdminUser: "admin", AdminPass: "new-pass"}
	a := &API{adminUser: "admin", adminPass: "old-pass"}
	a.config.Store(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/login", bytes.NewReader([]byte(`{"username":"admin","password":"new-pass"}`)))
	a.HandleLogin(rec, req)

	testutil.Equal(t, rec.Code, http.StatusOK)
}
