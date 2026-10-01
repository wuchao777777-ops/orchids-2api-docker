package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"encoding/json"

	"orchids-api/internal/config"
	"orchids-api/internal/testutil"
)

// TestHandleKeyByIDRotatesSecret covers the only supported way back to a usable
// secret. The store keeps just the hash, so a key whose secret was not copied
// at creation can never be displayed again; before rotation existed the list
// put a copy button over the masked string, which handed clients "sk-****1234"
// and a request that could never authenticate.
func TestHandleKeyByIDRotatesSecret(t *testing.T) {
	s, _ := newTestStore(t, "api-keys-rotate:")
	a := New(s, "admin", "pass", &config.Config{})
	ctx := context.Background()

	createReq := httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(`{"name":"client"}`))
	createRec := httptest.NewRecorder()
	a.HandleKeys(createRec, createReq)
	testutil.Equal(t, createRec.Code, http.StatusCreated)
	var created CreateKeyResponse
	testutil.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &created), "decode create response: %v")
	if _, err := s.AuthorizeApiKey(ctx, created.Key); err != nil {
		t.Fatalf("freshly created key does not authorize: %v", err)
	}

	rotateReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/keys/%d/rotate", created.ID), nil)
	rotateRec := httptest.NewRecorder()
	a.HandleKeyByID(rotateRec, rotateReq)
	testutil.Equal(t, rotateRec.Code, http.StatusOK)
	var rotated CreateKeyResponse
	testutil.NoError(t, json.Unmarshal(rotateRec.Body.Bytes(), &rotated), "decode rotate response: %v")
	if rotated.Key == "" || rotated.Key == created.Key {
		t.Fatalf("rotated key = %q, want a new secret distinct from %q", rotated.Key, created.Key)
	}
	if rotated.ID != created.ID || rotated.Name != created.Name {
		t.Fatalf("rotation changed the key identity: %#v vs %#v", rotated, created)
	}
	testutil.Equal(t, rotated.KeySuffix, rotated.Key[len(rotated.Key)-4:])

	if _, err := s.AuthorizeApiKey(ctx, rotated.Key); err != nil {
		t.Fatalf("rotated key does not authorize: %v", err)
	}
	if _, err := s.AuthorizeApiKey(ctx, created.Key); err == nil {
		t.Fatal("the retired secret still authorizes after rotation")
	}

	// The list must keep withholding the secret; rotation is the reveal path,
	// not the listing endpoint.
	listReq := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	listRec := httptest.NewRecorder()
	a.HandleKeys(listRec, listReq)
	testutil.Equal(t, listRec.Code, http.StatusOK)
	body := listRec.Body.String()
	testutil.MustNotContainAny(t, body, rotated.Key, created.Key, "key_full")
	testutil.MustContain(t, body, rotated.KeySuffix)
}

// TestHandleKeyByIDRejectsUnknownAction pins that the trailing action segment
// is parsed rather than ignored: only reset-usage and rotate dispatch, an
// unknown suffix leaves an unparsable id behind (400), and a bare POST without
// any action is refused outright (405) instead of silently rotating the key.
func TestHandleKeyByIDRejectsUnknownAction(t *testing.T) {
	s, _ := newTestStore(t, "api-keys-action:")
	a := New(s, "admin", "pass", &config.Config{})

	createReq := httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(`{"name":"client"}`))
	createRec := httptest.NewRecorder()
	a.HandleKeys(createRec, createReq)
	testutil.Equal(t, createRec.Code, http.StatusCreated)
	var created CreateKeyResponse
	testutil.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &created), "decode create response: %v")

	for _, tc := range []struct {
		path string
		want int
	}{
		{fmt.Sprintf("/api/keys/%d/nonsense", created.ID), http.StatusBadRequest},
		{fmt.Sprintf("/api/keys/%d", created.ID), http.StatusMethodNotAllowed},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, nil)
		rec := httptest.NewRecorder()
		a.HandleKeyByID(rec, req)
		testutil.Equal(t, rec.Code, tc.want)
	}

	// Neither refused request may have replaced the secret.
	if _, err := s.AuthorizeApiKey(context.Background(), created.Key); err != nil {
		t.Fatalf("refused requests rotated the key anyway: %v", err)
	}
}
