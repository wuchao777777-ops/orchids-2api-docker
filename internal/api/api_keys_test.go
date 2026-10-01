package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"encoding/json"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

func TestHandleKeysCreatesAndUpdatesPolicy(t *testing.T) {
	s, _ := newTestStore(t, "api-keys-policy:")
	a := New(s, "admin", "pass", &config.Config{})

	expiresAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	createBody := fmt.Sprintf("{\"name\":\"client\",\"allowed_models\":[\" GROK-4.6 \",\"grok-4.6\",\"grok-imagine-video\"],\"rpm_limit\":12,\"expires_at\":%q}", expiresAt.Format(time.RFC3339))
	createReq := httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(createBody))
	createRec := httptest.NewRecorder()
	a.HandleKeys(createRec, createReq)
	testutil.Equal(t, createRec.Code, http.StatusCreated)
	var created CreateKeyResponse
	testutil.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &created), "decode create response: %v")
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
	testutil.Equal(t, patchRec.Code, http.StatusOK)
	var updated store.ApiKey
	testutil.NoError(t, json.Unmarshal(patchRec.Body.Bytes(), &updated), "decode patch response: %v")
	if updated.RPMLimit != 0 || len(updated.AllowedModels) != 0 || updated.ExpiresAt != nil {
		t.Fatalf("updated=%#v", updated)
	}
}

// TestHandleKeysRejectsInvalidPolicyLimits keeps every invalid policy value out
// of the ledger: a negative rate limit, an already-expired window, and a
// negative or overflow-prone budget. All four inputs reach the same create
// handler, so they share one table.
func TestHandleKeysRejectsInvalidPolicyLimits(t *testing.T) {
	s, _ := newTestStore(t, "api-keys-invalid:")
	a := New(s, "admin", "pass", &config.Config{})

	for _, tt := range []struct{ name, body string }{
		{"negative rpm", `{"name":"negative","rpm_limit":-1}`},
		{"expired window", `{"name":"expired","expires_at":"2020-01-01T00:00:00Z"}`},
		{"negative billing budget", `{"name":"negative","billing_limit_usd_ticks":-1}`},
		{"overflowing billing budget", `{"name":"overflow","billing_limit_usd_ticks":9000000000000001}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			a.HandleKeys(rec, req)
			testutil.Equal(t, rec.Code, http.StatusBadRequest)
		})
	}
}
