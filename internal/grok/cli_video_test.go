package grok

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
)

func TestCLIVideoRequestsUseBuildProtocolHeaders(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("Authorization=%q", got)
		}
		if got := r.Header.Get("x-grok-model-override"); got != "grok-imagine-video-1.5" {
			t.Errorf("x-grok-model-override=%q", got)
		}
		switch calls {
		case 1:
			if r.Method != http.MethodPost || r.URL.Path != "/v1/videos/generations" {
				t.Fatalf("create request=%s %s", r.Method, r.URL.Path)
			}
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type=%q", got)
			}
			var payload map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			if payload["model"] != "grok-imagine-video-1.5" {
				t.Errorf("model=%v", payload["model"])
			}
			_, _ = io.WriteString(w, `{"request_id":"video_1"}`)
		case 2:
			if r.Method != http.MethodGet || r.URL.Path != "/v1/videos/video_1" {
				t.Fatalf("poll request=%s %s", r.Method, r.URL.Path)
			}
			_, _ = io.WriteString(w, `{"status":"pending"}`)
		default:
			t.Fatalf("unexpected request #%d", calls)
		}
	}))
	defer upstream.Close()

	client := NewCLIClient(&config.Config{GrokCLIBaseURL: upstream.URL + "/v1"})
	client.httpClient = upstream.Client()
	client.oauth.httpClient = upstream.Client()
	account := &store.Account{
		OAuthAccessToken: "access-token",
		OAuthExpiresAt:   time.Now().Add(time.Hour),
	}

	create, err := client.doResponsesAt(context.Background(), account, "/videos/generations", map[string]interface{}{
		"model": "grok-imagine-video-1.5", "prompt": "animate",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = create.Body.Close()
	poll, err := client.doResponseResource(context.Background(), account, http.MethodGet, "/videos/video_1", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = poll.Body.Close()
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}
