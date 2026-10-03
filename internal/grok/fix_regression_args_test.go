package grok

import (
	"fmt"
	"strings"
	"testing"

	"encoding/json"

	"orchids-api/internal/testutil"
)

// TestNormalizeFunctionArguments covers the B=3 normalization rule: an integral
// argument serialized as a float must become an integer literal, guided by the
// tool schema (Codex's decoder rejects the float form), while a number-typed
// field — or a payload that is not a single JSON value — is left alone.
func TestNormalizeFunctionArguments(t *testing.T) {
	t.Run("integral numbers become integer literals", func(t *testing.T) {
		schema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"timeout_ms": map[string]interface{}{"type": "integer"},
				"count":      map[string]interface{}{"type": "integer"},
				"ratio":      map[string]interface{}{"type": "number"},
				"nested": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{"limit": map[string]interface{}{"type": "integer"}},
				},
				"items": map[string]interface{}{
					"type":  "array",
					"items": map[string]interface{}{"type": "integer"},
				},
			},
		}
		raw := `{"timeout_ms":60000.0,"count":1e3,"ratio":1.5,"nested":{"limit":2.0},"items":[1.0,2e1]}`
		got, changed := normalizeFunctionArguments(raw, schema)
		testutil.True(t, changed, "expected normalization, got %q")
		var decoded map[string]interface{}
		decoder := json.NewDecoder(strings.NewReader(got))
		decoder.UseNumber()
		err := decoder.Decode(&decoded)
		testutil.CheckNoError(t, err)
		check := func(path string, want string) {
			t.Helper()
			parts := strings.Split(path, ".")
			var current interface{} = decoded
			for _, part := range parts {
				asMap, ok := current.(map[string]interface{})
				testutil.True(t, ok, "%s: path not an object in %s")
				current = asMap[part]
			}
			testutil.Equal(t, fmt.Sprint(current), want)
		}
		// json.Number stringifies exactly as written, which is the point: the literal
		// must carry no fraction and no exponent.
		check("timeout_ms", "60000")
		check("count", "1000")
		check("nested.limit", "2")
		check("ratio", "1.5")
		if items, ok := decoded["items"].([]interface{}); !ok || fmt.Sprint(items[1]) != "20" {
			t.Fatalf("items = %v, want [1 20] (in %s)", decoded["items"], got)
		}
	})
	// A number-typed field keeps its float, and a payload that is not a single
	// JSON value is passed through untouched.
	t.Run("non-integral payloads are untouched", func(t *testing.T) {
		schema := map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"ratio": map[string]interface{}{"type": "number"}},
		}
		for _, raw := range []string{`{"ratio":60000.0}`, `{"a":1} trailing`} {
			got, changed := normalizeFunctionArguments(raw, schema)
			testutil.Falsef(t, changed || got != raw, "payload must be untouched: %q (changed=%v)", got, changed)
		}
	})
}

func TestNormalizeIntegralNumberBounds(t *testing.T) {
	_, ok := normalizeIntegralNumber("1.0")
	testutil.False(t, !ok, "1.0 should normalize to 1")
	_, ok = normalizeIntegralNumber("1.5")
	testutil.False(t, ok, "1.5 is not an integer")
	_, ok = normalizeIntegralNumber("1e400")
	testutil.False(t, ok, "1e400 does not fit an int64 and must be left alone")
}

func TestResponsesImagePartsCarryDefaultDetail(t *testing.T) {
	parts := responsesMessageParts([]interface{}{
		map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "https://example.com/a.png"}},
	}, false)
	testutil.Equal(t, len(parts), 1)
	part, _ := parts[0].(map[string]interface{})
	testutil.Equal(t, part["detail"], "auto")
	// An explicit detail is preserved.
	parts = responsesMessageParts([]interface{}{
		map[string]interface{}{"type": "image_url", "detail": "high", "image_url": map[string]interface{}{"url": "https://example.com/a.png"}},
	}, false)
	part, _ = parts[0].(map[string]interface{})
	testutil.Equal(t, part["detail"], "high")
}
