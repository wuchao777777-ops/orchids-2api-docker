package util

import (
	"testing"

	"orchids-api/internal/testutil"
)

func TestEncodeJSONBytesDoesNotEscapeHTML(t *testing.T) {
	payload := map[string]interface{}{
		"type": "chunk",
		"data": map[string]interface{}{
			"text": "hello <world>",
			"n":    1,
		},
	}
	got := string(EncodeJSONBytes(payload))
	testutil.MustContain(t, got, "hello <world>")
}

func TestEncodeJSONBytesFallsBackToEmptyObject(t *testing.T) {
	got := string(EncodeJSONBytes(func() {}))
	testutil.Equal(t, got, "{}")
}

func BenchmarkEncodeJSON_Bytes(b *testing.B) {
	payload := map[string]interface{}{
		"id": "msg_1",
		"choices": []map[string]interface{}{{
			"index": 0,
			"delta": map[string]interface{}{"content": "hello world"},
		}},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = EncodeJSONBytes(payload)
	}
}
