package grok

import (
	"net/http"

	"github.com/goccy/go-json"

	"orchids-api/internal/debug"
)

// requireMethod writes the standard 405 response and returns false when the
// request method does not match. Handlers use it as:
//
//	if !requireMethod(w, r, http.MethodGet) {
//		return
//	}
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// writeJSON writes v to w as a JSON response with the application/json content
// type.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSONBody decodes the request body into v and writes the standard
// 400 response on failure.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return false
	}
	return true
}

// streamResponseHeaders writes the standard SSE headers and returns the
// response flusher (possibly nil).
func streamResponseHeaders(w http.ResponseWriter) http.Flusher {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	return flusher
}

// writeSSE sends an SSE frame and flushes when the writer supports it.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, data []byte) {
	writeSSEBytes(w, event, data)
	if flusher != nil {
		flusher.Flush()
	}
}

// writeSSELog sends an SSE frame, mirrors it to the debug logger, and flushes.
func writeSSELog(w http.ResponseWriter, flusher http.Flusher, logger *debug.Logger, raw []byte) {
	writeSSEBytes(w, "", raw)
	if logger != nil {
		logger.LogOutputSSE("", string(raw))
	}
	if flusher != nil {
		flusher.Flush()
	}
}

// writeSSEStreamError sends the SSE error frame, the [DONE] terminator, and
// flushes, mirroring both to the debug logger when one is set.
func writeSSEStreamError(w http.ResponseWriter, flusher http.Flusher, logger *debug.Logger, msg string) {
	writeSSEError(w, msg, "server_error", "stream_error")
	writeSSEBytes(w, "", []byte("[DONE]"))
	if logger != nil {
		logger.LogOutputSSE("error", msg)
		logger.LogOutputSSE("", "[DONE]")
	}
	if flusher != nil {
		flusher.Flush()
	}
}
