package grok

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamImageGeneration_ParseErrorUsesSSEErrorEvent(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()

	h.streamImageGeneration(rec, strings.NewReader(`{"result":{"response":{"token":"bad"}}`), "", "test prompt", "url", 1, "")

	raw := rec.Body.String()
	if !strings.Contains(raw, "event: error") {
		t.Fatalf("expected SSE error event, raw=%q", raw)
	}
	if !strings.Contains(raw, `"code":"stream_error"`) {
		t.Fatalf("expected stream_error code, raw=%q", raw)
	}
	if !strings.Contains(raw, "[DONE]") {
		t.Fatalf("expected DONE after stream error, raw=%q", raw)
	}
}

func TestStreamImageGeneration_SuccessEndsWithDone(t *testing.T) {
	h := &Handler{client: New(nil)}
	rec := httptest.NewRecorder()

	h.streamImageGeneration(rec, strings.NewReader(`{"result":{"response":{"modelResponse":{"generatedImageUrls":["https://assets.grok.com/users/u-1/generated/a1/image.png"]}}}}`), "", "test prompt", "url", 1, "")

	raw := rec.Body.String()
	if !strings.Contains(raw, "image_generation.completed") {
		t.Fatalf("expected completed image event, raw=%q", raw)
	}
	if !strings.Contains(raw, "[DONE]") {
		t.Fatalf("expected DONE after success, raw=%q", raw)
	}
}

func TestStreamImageGeneration_NoImageUsesSSEErrorEvent(t *testing.T) {
	h := &Handler{client: New(nil)}
	rec := httptest.NewRecorder()

	h.streamImageGeneration(rec, strings.NewReader(`{"result":{"response":{"token":"still working"}}}`), "", "test prompt", "url", 1, "")

	raw := rec.Body.String()
	if !strings.Contains(raw, "event: error") {
		t.Fatalf("expected SSE error event, raw=%q", raw)
	}
	if !strings.Contains(raw, `"code":"no_image_generated"`) {
		t.Fatalf("expected no_image_generated code, raw=%q", raw)
	}
	if !strings.Contains(raw, "[DONE]") {
		t.Fatalf("expected DONE after no image error, raw=%q", raw)
	}
}

func TestStreamImageGeneration_AcceptsAlternateProgressShape(t *testing.T) {
	h := &Handler{client: New(nil)}
	rec := httptest.NewRecorder()

	h.streamImageGeneration(rec, strings.NewReader(
		`{"result":{"response":{"streaming_image_generation_response":{"image_index":1,"percentage":55}}}}`+
			`{"result":{"response":{"model_response":{"generatedImageUrls":["https://assets.grok.com/users/u-1/generated/a1/image.png"]}}}}`,
	), "", "alt prompt", "url", 2, "")

	raw := rec.Body.String()
	if !strings.Contains(raw, `"progress":55`) {
		t.Fatalf("expected alternate progress shape to be parsed, raw=%q", raw)
	}
	if !strings.Contains(raw, `"prompt_tokens":`) {
		t.Fatalf("expected estimated usage to be included, raw=%q", raw)
	}
}
