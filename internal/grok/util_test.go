package grok

import (
	"errors"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

func TestValidateChatMessages_AcceptsCaseInsensitiveRoleAndType(t *testing.T) {
	messages := []ChatMessage{
		{
			Role: "User",
			Content: []interface{}{
				map[string]interface{}{"type": "Text", "text": "hello"},
				map[string]interface{}{"type": "Image_URL", "image_url": map[string]interface{}{"url": "https://a/b.png"}},
			},
		},
		{
			Role: "ASSISTANT",
			Content: []interface{}{
				map[string]interface{}{"type": "TEXT", "text": "ok"},
			},
		},
	}

	testutil.NoError(t, validateChatMessages(messages), "validateChatMessages() error = %v")
}

func TestApplyQuotaInfo_InfersLiteSubscription(t *testing.T) {
	acc := &store.Account{Subscription: "basic"}
	changed := ApplyQuotaInfo(acc, &RateLimitInfo{
		Limit:        70,
		HasLimit:     true,
		Remaining:    63,
		HasRemaining: true,
		Unit:         "requests",
	})
	testutil.True(t, changed, "ApplyQuotaInfo changed=false, want true")
	testutil.Equal(t, acc.Subscription, "lite")
	if acc.UsageLimit != 70 || acc.UsageCurrent != 63 {
		t.Fatalf("unexpected quota: limit=%v current=%v", acc.UsageLimit, acc.UsageCurrent)
	}
}

func TestApplyQuotaInfo_InfersBasicFromFreeAutoWindow(t *testing.T) {
	acc := &store.Account{}
	changed := ApplyQuotaInfo(acc, &RateLimitInfo{
		Limit:        7,
		HasLimit:     true,
		Remaining:    7,
		HasRemaining: true,
		Unit:         "requests",
	})
	testutil.True(t, changed, "ApplyQuotaInfo changed=false, want true")
	testutil.Equal(t, acc.Subscription, "basic")
	if acc.UsageLimit != 7 || acc.UsageCurrent != 7 {
		t.Fatalf("unexpected quota: limit=%v current=%v", acc.UsageLimit, acc.UsageCurrent)
	}
}

func TestParseRateLimitValue_ComplexFormats(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{in: "100;w=3600", want: 100},
		{in: "23/50", want: 23},
		{in: "remaining=42", want: 42},
		{in: "  7.9 requests", want: 7},
	}

	for _, tt := range tests {
		got, ok := parseRateLimitValue(tt.in)
		if !ok {
			t.Fatalf("parseRateLimitValue(%q) not parsed", tt.in)
		}
		testutil.Equal(t, got, tt.want)
	}
}

func TestParseRateLimitReset_RFC3339(t *testing.T) {
	raw := "2026-03-05T19:00:00Z"
	got := parseRateLimitReset(raw)
	want, _ := time.Parse(time.RFC3339, raw)
	if !got.Equal(want) {
		t.Fatalf("parseRateLimitReset(%q)=%v want=%v", raw, got, want)
	}
}

func TestEncodeJSONBytesDoesNotEscapeHTML(t *testing.T) {
	payload := map[string]interface{}{
		"type": "chunk",
		"data": map[string]interface{}{
			"text": "hello <world>",
			"n":    1,
		},
	}
	got := string(encodeJSONBytes(payload))
	testutil.MustContain(t, got, "hello <world>")
}

func TestWriteSSEBytesWritesEventFrame(t *testing.T) {
	bytesRec := httptest.NewRecorder()
	writeSSEBytes(bytesRec, "demo", []byte(`{"ok":true}`))

	got := bytesRec.Body.String()
	testutil.MustContainAll(t, got, "event: demo\n", `data: {"ok":true}`)
}

func TestWriteSSEBytesPropagatesShortWrite(t *testing.T) {
	writer := grok2apiShortWriter{httptest.NewRecorder()}
	if err := writeSSEBytes(writer, "demo", []byte(`{"ok":true}`)); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("error=%v", err)
	}
}

func TestStreamResponseHeadersMatchSSEProxyContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	recorder.Header().Set("Connection", "keep-alive")
	streamResponseHeaders(recorder)
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Fatalf("content-type=%q", got)
	}
	if recorder.Header().Get("X-Accel-Buffering") != "no" || recorder.Header().Get("Connection") != "" {
		t.Fatalf("headers=%v", recorder.Header())
	}
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
		_ = encodeJSONBytes(payload)
	}
}

func BenchmarkWriteSSE_Bytes(b *testing.B) {
	writer := httptest.NewRecorder()
	data := []byte(`{"ok":true}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		writer.Body.Reset()
		writeSSEBytes(writer, "demo", data)
	}
}
