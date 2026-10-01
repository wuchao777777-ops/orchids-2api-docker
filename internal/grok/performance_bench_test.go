package grok

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"orchids-api/internal/config"
	"orchids-api/internal/perfprobe"
	"orchids-api/internal/store"
	"testing"
	"time"
)

// BenchmarkProviderLocalHTTP preserves the earlier HTTP-only measurement.
func BenchmarkProviderLocalHTTP(b *testing.B) {
	cfg := &config.Config{}
	c := NewCLIClient(cfg)
	c.httpClient = &http.Client{Transport: perfprobe.Transport{Body: perfprobe.OpenAI}}
	token := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"local","team_id":"local"}`)) + ".fake"
	acc := &store.Account{OAuthAccessToken: token, OAuthExpiresAt: time.Now().Add(time.Hour), UserID: "local", TeamID: "local"}
	if perfprobe.Load(func() error {
		resp, err := c.request(context.Background(), acc, http.MethodPost, "http://mock.invalid/v1/responses", []byte(`{"model":"grok-4.7","input":"hello"}`), nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}) {
		return
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := c.request(context.Background(), acc, http.MethodPost, "http://mock.invalid/v1/responses", []byte(`{"model":"grok-4.7","input":"hello"}`), nil)
			if err != nil {
				b.Fatal(err)
			}
			_, err = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkProviderLocal includes chat normalization, signed Build request,
// response SSE parsing, protocol conversion, and downstream writes. It excludes
// network, Redis, public-route authentication and per-account admission, as do
// the other provider adapter benchmarks.
func BenchmarkProviderLocal(b *testing.B) {
	cfg := &config.Config{}
	c := NewCLIClient(cfg)
	const stream = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"local\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	c.httpClient = &http.Client{Transport: perfprobe.Transport{Body: stream}}
	token := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"local","team_id":"local"}`)) + ".fake"
	acc := &store.Account{OAuthAccessToken: token, OAuthExpiresAt: time.Now().Add(time.Hour), UserID: "local", TeamID: "local"}
	h := &Handler{cfg: cfg}
	call := func() error {
		req := &ChatCompletionsRequest{Model: "grok-4.7", Stream: true, Messages: []ChatMessage{{Role: "user", Content: "hello"}}, account: acc}
		payload, err := h.responsesPayloadFromChat(ModelSpec{ID: "grok-4.7", UpstreamModel: "grok-4.7"}, req, true)
		if err != nil {
			return err
		}
		resp, err := c.doResponsesOnceAt(context.Background(), acc, "/responses", payload)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		writer := &performanceWriter{header: make(http.Header)}
		out := h.streamBuildChatHolding(writer, req, resp.Body, nil)
		if out.Err != nil {
			return out.Err
		}
		if !out.SawText || writer.bytes == 0 {
			return fmt.Errorf("stream produced no text")
		}
		return nil
	}
	if perfprobe.Load(call) {
		return
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := call(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

type performanceWriter struct {
	header http.Header
	bytes  int
}

func (w *performanceWriter) Header() http.Header         { return w.header }
func (w *performanceWriter) WriteHeader(int)             {}
func (w *performanceWriter) Write(p []byte) (int, error) { w.bytes += len(p); return len(p), nil }
func (w *performanceWriter) Flush()                      {}
