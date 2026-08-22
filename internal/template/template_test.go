package template

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"orchids-api/internal/config"
)

func TestRendererParsesAndRendersEmbeddedTemplates(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/?tab=accounts", nil)
	if err := renderer.RenderIndex(recorder, request, &config.Config{AdminPath: "/admin"}, nil); err != nil {
		t.Fatalf("RenderIndex() error = %v", err)
	}
}
