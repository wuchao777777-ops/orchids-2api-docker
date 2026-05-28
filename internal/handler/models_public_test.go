package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orchids-api/internal/store"
)

func TestHandleModelByID_HidesOfflineModel(t *testing.T) {
	h, s, mini := setupModelValidationHandler(t)
	defer func() {
		_ = s.Close()
		mini.Close()
	}()

	if err := s.CreateModel(context.Background(), &store.Model{
		Channel: "Orchids",
		ModelID: "offline-only-model",
		Name:    "Offline Only",
		Status:  store.ModelStatusOffline,
	}); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/orchids/v1/models/offline-only-model", nil)
	rec := httptest.NewRecorder()

	h.HandleModelByID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestHandleModelByID_HidesUnsupportedGrokModel(t *testing.T) {
	h, s, mini := setupModelValidationHandler(t)
	defer func() {
		_ = s.Close()
		mini.Close()
	}()

	req := httptest.NewRequest(http.MethodGet, "http://example.com/grok/v1/models/grok-4.1", nil)
	rec := httptest.NewRecorder()

	h.HandleModelByID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestHandleModelByID_ReturnsVisibleModel(t *testing.T) {
	h, s, mini := setupModelValidationHandler(t)
	defer func() {
		_ = s.Close()
		mini.Close()
	}()

	if err := s.CreateAccount(context.Background(), &store.Account{
		AccountType:  "grok",
		ClientCookie: "sso=super-token",
		Subscription: "super",
		Enabled:      true,
	}); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/grok/v1/models/grok-4.3-beta", nil)
	rec := httptest.NewRecorder()

	h.HandleModelByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestHandleModelByID_ReturnsVerifiedDynamicGrokModel(t *testing.T) {
	h, s, mini := setupModelValidationHandler(t)
	defer func() {
		_ = s.Close()
		mini.Close()
	}()

	if err := s.CreateModel(context.Background(), &store.Model{
		Channel:  "Grok",
		ModelID:  "grok-5",
		Name:     "grok-5",
		Status:   store.ModelStatusAvailable,
		Verified: true,
	}); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}
	if err := s.CreateAccount(context.Background(), &store.Account{
		AccountType:  "grok",
		ClientCookie: "sso=basic-token",
		Subscription: "basic",
		Enabled:      true,
	}); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/grok/v1/models/grok-5", nil)
	rec := httptest.NewRecorder()

	h.HandleModelByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestHandleModels_FiltersGrokModelsByAvailablePools(t *testing.T) {
	h, s, mini := setupModelValidationHandler(t)
	defer func() {
		_ = s.Close()
		mini.Close()
	}()

	if err := s.CreateAccount(context.Background(), &store.Account{
		AccountType:  "grok",
		ClientCookie: "sso=basic-token",
		Subscription: "basic",
		Enabled:      true,
	}); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/grok/v1/models", nil)
	rec := httptest.NewRecorder()

	h.HandleModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "grok-4.20-0309-non-reasoning") {
		t.Fatalf("expected basic model in body=%s", body)
	}
	if strings.Contains(body, "grok-4.20-0309-super") || strings.Contains(body, "grok-imagine-video") {
		t.Fatalf("super/video models should be hidden without super or heavy accounts, body=%s", body)
	}
}

func TestHandleModelByID_HidesGrokModelWithoutRequiredPool(t *testing.T) {
	h, s, mini := setupModelValidationHandler(t)
	defer func() {
		_ = s.Close()
		mini.Close()
	}()

	if err := s.CreateAccount(context.Background(), &store.Account{
		AccountType:  "grok",
		ClientCookie: "sso=basic-token",
		Subscription: "basic",
		Enabled:      true,
	}); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/grok/v1/models/grok-imagine-video", nil)
	rec := httptest.NewRecorder()

	h.HandleModelByID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}
