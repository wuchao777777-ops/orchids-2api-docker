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
		err := s.CreateModel(context.Background(), m)
		testutil.CheckNoError(t, err)
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
	err := json.Unmarshal(rec.Body.Bytes(), &bare)
	testutil.CheckNoError(t, err)
	testutil.Equal(t, len(bare), 1)
	testutil.Equal(t, bare[0].ModelID, "grok-4.6")
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
	err := json.Unmarshal(rec.Body.Bytes(), &envelope)
	testutil.CheckNoError(t, err)
	testutil.Falsef(t, envelope.Total != 3 || envelope.Page != 1 || envelope.PageSize != 2 || len(envelope.Items) != 2, "envelope=%+v", envelope)

	rec = httptest.NewRecorder()
	a.HandleModels(rec, httptest.NewRequest(http.MethodGet, "/api/models?page=2&pageSize=2&search=grok", nil))
	testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "filtered envelope decode failed: %v")
	testutil.Equal(t, envelope.Total, 2)
	testutil.Equal(t, len(envelope.Items), 0)

	// pageSize is clamped so one request cannot dump the whole table.
	rec = httptest.NewRecorder()
	a.HandleModels(rec, httptest.NewRequest(http.MethodGet, "/api/models?pageSize=100000", nil))
	testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "clamped envelope decode failed: %v")
	testutil.Equal(t, envelope.PageSize, maxAdminModelPageSize)
}
