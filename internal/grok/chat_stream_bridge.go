package grok

import (
	"io"
	"net/http"

	"orchids-api/internal/httpserver"
)

// streamThroughChat delegates to the shared implementation: the pipe lifecycle
// is protocol-neutral, so both the Responses bridge and the Messages bridge use
// the one version.
func streamThroughChat(req *http.Request, chat http.HandlerFunc, consume func(int, http.Header, io.Reader)) {
	httpserver.StreamThroughChat(req, chat, consume)
}

// withChatStream owns the internal request and pipe lifecycle for both public
// protocol bridges. Returning from consume also cancels the upstream producer.
func (h *Handler) withChatStream(req *http.Request, consume func(int, http.Header, io.Reader)) {
	streamThroughChat(req, h.HandleChatCompletions, consume)
}
