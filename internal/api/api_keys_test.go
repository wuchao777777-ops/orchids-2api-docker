package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
)

func TestHandleKeysCreatesAndUpdatesPolicy(t *testing.T) {
	s, mini := newTestStore(t, "api-keys-policy:")
	defer mini.Close()
	defer s.Close()
	a := New(s, "admin", "pass", &config.Config{})

	expiresAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	createBody := fmt.Sprintf("{\"name\":\"client\",\"allowed_models\":[\" GROK-4.6 \",\"grok-4.6\",\"grok-imagine-video\"],\"rpm_limit\":12,\"expires_at\":%q}", expiresAt.Format(time.RFC3339))
	createReq := httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(createBody))
	createRec := httptest.NewRecorder()
	a.HandleKeys(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var created CreateKeyResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Key == "" || created.RPMLimit != 12 || len(created.AllowedModels) != 2 || created.ExpiresAt == nil {
		t.Fatalf("created=%#v", created)
	}

	patchReq := httptest.NewRequest(
		http.MethodPatch,
		fmt.Sprintf("/api/keys/%d", created.ID),
		strings.NewReader("{\"allowed_models\":[],\"rpm_limit\":0,\"expires_at\":null}"),
	)
	patchRec := httptest.NewRecorder()
	a.HandleKeyByID(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", patchRec.Code, patchRec.Body.String())
	}
	var updated store.ApiKey
	if err := json.Unmarshal(patchRec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if updated.RPMLimit != 0 || len(updated.AllowedModels) != 0 || updated.ExpiresAt != nil {
		t.Fatalf("updated=%#v", updated)
	}
}

func TestHandleKeysRejectsInvalidLimits(t *testing.T) {
	s, mini := newTestStore(t, "api-keys-invalid:")
	defer mini.Close()
	defer s.Close()
	a := New(s, "admin", "pass", &config.Config{})

	for _, body := range []string{
		"{\"name\":\"negative\",\"rpm_limit\":-1}",
		"{\"name\":\"expired\",\"expires_at\":\"2020-01-01T00:00:00Z\"}",
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(body))
		rec := httptest.NewRecorder()
		a.HandleKeys(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, rec.Code, rec.Body.String())
		}
	}
}
