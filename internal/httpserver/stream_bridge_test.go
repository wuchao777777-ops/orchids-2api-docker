package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orchids-api/internal/testutil"
)

// The committed header must be captured at the first WriteHeader: a later
// mutation must not change what the client already received.
func TestStreamingChatWriterFreezesCommittedHeaders(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	stream := newStreamingChatWriter(writer)
	stream.Header().Set("X-Test", "initial")
	stream.WriteHeader(http.StatusBadRequest)
	stream.Header().Set("X-Test", "later")
	stream.WriteHeader(http.StatusOK)
	testutil.False(t, stream.status != http.StatusBadRequest || stream.committedHeader.Get("X-Test") != "initial", "committed response changed")
}

func TestStreamThroughChatPropagatesStatusAndClosesPipe(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{"))
	var saved io.Reader
	StreamThroughChat(req, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid json"))
	}, func(status int, header http.Header, reader io.Reader) {
		saved = reader
		body, err := io.ReadAll(reader)
		if err != nil || status != http.StatusBadRequest || string(body) != "invalid json" {
			t.Fatalf("status=%d err=%v body=%q", status, err, string(body))
		}
	})
	testutil.False(t, saved == nil, "bridge did not forward response")
	if _, err := saved.Read(make([]byte, 1)); err != io.ErrClosedPipe {
		t.Fatal("bridge left reader open", err)
	}
}
