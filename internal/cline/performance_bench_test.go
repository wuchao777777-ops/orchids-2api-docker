package cline

import (
	"context"
	"net/http"
	"orchids-api/internal/perfprobe"
	"orchids-api/internal/prompt"
	"orchids-api/internal/store"
	"orchids-api/internal/upstream"
	"testing"
)

func BenchmarkProviderLocal(b *testing.B) {
	c := &Client{apiBase: "http://mock.invalid", stream: &http.Client{Transport: perfprobe.Transport{Body: perfprobe.OpenAI}}, creds: Credentials{AccessToken: "fake"}, account: &store.Account{ClineModelIDs: []string{"model-a"}}}
	req := upstream.UpstreamRequest{Model: "model-a", Messages: []prompt.Message{{Role: "user", Content: prompt.MessageContent{Text: "hello"}}}}
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
