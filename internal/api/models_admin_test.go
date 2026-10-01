package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

func seedAdminModels(t *testing.T, s *store.Store, models ...*store.Model) {
	t.Helper()
	for _, m := range models {
		if err := s.CreateModel(context.Background(), m); err != nil {
			t.Fatalf("CreateModel(%s): %v", m.ID, err)
		}
	}
}

// Without a page parameter the endpoint keeps returning the bare array the
// bundled admin UI decodes.
func TestHandleModelsStaysABareArrayWithoutPaging(t *testing.T) {
	a, s, cleanup := newTestAPI(t)
	defer cleanup()
	seedAdminModels(t, s, &store.Model{ID: "grok-4.6", Channel: "grok", ModelID: "grok-4.6", Name: "Grok 4.6"})

	rec := httptest.NewRecorder()
	a.HandleModels(rec, httptest.NewRequest(http.MethodGet, "/api/models", nil))

	testutil.Equal(t, rec.Code, http.StatusOK)
	var bare []store.Model
	if err := json.Unmarshal(rec.Body.Bytes(), &bare); err != nil {
		t.Fatalf("legacy array decode failed: %v (body=%s)", err, rec.Body.String())
	}
	if len(bare) != 1 || bare[0].ModelID != "grok-4.6" {
		t.Fatalf("bare=%+v", bare)
	}
}

// Asking for a page switches to the optional paged envelope.
func TestHandleModelsServesPagedEnvelopeOnRequest(t *testing.T) {
	a, s, cleanup := newTestAPI(t)
	defer cleanup()
	seedAdminModels(t, s,
		&store.Model{ID: "grok-4.6", Channel: "grok", ModelID: "grok-4.6", Name: "Grok 4.6"},
		&store.Model{ID: "grok-4.5", Channel: "grok", ModelID: "grok-4.5", Name: "Grok 4.5"},
		&store.Model{ID: "wb-claude", Channel: "workbuddy", ModelID: "claude", Name: "Claude"},
	)

	rec := httptest.NewRecorder()
	a.HandleModels(rec, httptest.NewRequest(http.MethodGet, "/api/models?page=1&pageSize=2", nil))

	var envelope adminModelListEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("envelope decode failed: %v (body=%s)", err, rec.Body.String())
	}
	if envelope.Total != 3 || envelope.Page != 1 || envelope.PageSize != 2 || len(envelope.Items) != 2 {
		t.Fatalf("envelope=%+v", envelope)
	}

	rec = httptest.NewRecorder()
	a.HandleModels(rec, httptest.NewRequest(http.MethodGet, "/api/models?page=2&pageSize=2&search=grok", nil))
	testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "filtered envelope decode failed: %v")
	if envelope.Total != 2 || len(envelope.Items) != 0 {
		t.Fatalf("search+page envelope=%+v", envelope)
	}

	// pageSize is clamped so one request cannot dump the whole table.
	rec = httptest.NewRecorder()
	a.HandleModels(rec, httptest.NewRequest(http.MethodGet, "/api/models?pageSize=100000", nil))
	testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "clamped envelope decode failed: %v")
	testutil.Equal(t, envelope.PageSize, maxAdminModelPageSize)
}
