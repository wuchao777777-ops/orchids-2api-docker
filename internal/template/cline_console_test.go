package template

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orchids-api/internal/config"
	"orchids-api/internal/testutil"
)

func TestAccountModalOffersClineLogin(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/?tab=accounts", nil)
	testutil.NoError(t, renderer.RenderIndex(recorder, request, &config.Config{AdminPath: "/admin"}, nil), "RenderIndex() error = %v")
	page := recorder.Body.String()
	for _, want := range []string{
		`id="clineLoginGroup"`,
		`id="clineLoginButton"`,
		`ClineLogin.start()`,
		`js/cline-auth.js`,
	} {
		testutil.CheckContain(t, page, want)
	}
}

func TestModelModalOffersClineChannel(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/?tab=models", nil)
	testutil.NoError(t, renderer.RenderIndex(recorder, request, &config.Config{AdminPath: "/admin"}, nil), "RenderIndex() error = %v")
	if page := recorder.Body.String(); !strings.Contains(page, `provider-registry.js`) || !strings.Contains(page, `id="modelChannel"`) {
		t.Error("the model modal is not wired to the shared provider registry")
	}
}
