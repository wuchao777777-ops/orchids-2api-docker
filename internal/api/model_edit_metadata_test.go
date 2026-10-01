package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

func TestModelAdminEditPreservesDiscoveredRoutingMetadata(t *testing.T) {
	s, _ := newTestStore(t, "model_edit:")
	model := &store.Model{Channel: "Grok", ModelID: "grok-4.7", Name: "old", Status: store.ModelStatusAvailable, Verified: true, Provider: "build", UpstreamModel: "grok-4.7", Capabilities: []string{store.CapabilityChat, store.CapabilityResponses}, Origin: "discovery", BoundAccountIDs: []int64{7}, CreatedAt: time.Now().UTC()}
	testutil.NoError(t, s.CreateModel(context.Background(), model))
	a := New(s, "admin", "pass", &config.Config{})
	req := httptest.NewRequest(http.MethodPut, "/api/models/"+model.ID, strings.NewReader(`{"channel":"Grok","model_id":"grok-4.7","name":"renamed","status":"maintenance","sort_order":9,"is_default":true}`))
	rec := httptest.NewRecorder()
	a.HandleModelByID(rec, req)
	testutil.Equal(t, rec.Code, http.StatusOK)
	got, err := s.GetModel(context.Background(), model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Verified || got.Provider != "build" || got.UpstreamModel != "grok-4.7" || got.Origin != "discovery" || len(got.Capabilities) != 2 || len(got.BoundAccountIDs) != 1 || got.CreatedAt.IsZero() {
		t.Fatalf("metadata was lost: %+v", got)
	}
	if got.Name != "renamed" || got.Status != store.ModelStatusMaintenance || !got.IsDefault || got.SortOrder != 9 {
		t.Fatalf("editable fields not applied: %+v", got)
	}
}
