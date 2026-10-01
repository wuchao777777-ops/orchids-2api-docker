package qoder

import (
	"context"
	"net/http"
	"orchids-api/internal/perfprobe"
	"orchids-api/internal/prompt"
	"orchids-api/internal/upstream"
	"testing"
)

func BenchmarkProviderLocal(b *testing.B) {
	c := NewFromAccount(signedTestAccount(), nil)
	setTestEndpoints(c, "http://mock.invalid", "http://mock.invalid", "http://mock.invalid")
	c.stream = &http.Client{Transport: perfprobe.Transport{Body: envelope(`{"id":"1","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`) + "event:finish\ndata: {}\n\n"}}
	req := upstream.UpstreamRequest{Model: "Qwen3.7-Max", Messages: []prompt.Message{{Role: "user", Content: prompt.MessageContent{Text: "hello"}}}}
	if perfprobe.Load(func() error {
		return c.SendRequestWithPayload(context.Background(), req, func(upstream.SSEMessage) {}, nil)
	}) {
		return
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := c.SendRequestWithPayload(context.Background(), req, func(upstream.SSEMessage) {}, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
}
