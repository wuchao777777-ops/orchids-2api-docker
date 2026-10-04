package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

// MaxJSONBodyBytes bounds the request body of every JSON endpoint: an unbounded
// Decode lets one client allocate arbitrary memory inside the gateway.
const MaxJSONBodyBytes = 32 << 20

// DecodeJSONBody decodes the request body into v and writes the standard error
// response on failure: 415 for a non-JSON content type, 413 for an oversized
// body and 400 for malformed JSON.
func DecodeJSONBody(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if r == nil {
		WriteError(w, http.StatusBadRequest, "invalid request")
		return false
	}
	if raw := strings.TrimSpace(r.Header.Get("Content-Type")); raw != "" {
		mediaType, _, err := mime.ParseMediaType(raw)
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			WriteErrorCode(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
			return false
		}
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, MaxJSONBodyBytes)
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			WriteErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the configured limit")
			return false
		}
		WriteError(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}

// ReadBoundedJSONBody reads a JSON request body under the shared limit. It
// writes the 413/400 response itself and returns an error so the caller only
// has to return: an unbounded io.ReadAll lets one client allocate arbitrary
// memory inside the gateway.
func ReadBoundedJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r == nil || r.Body == nil {
		WriteError(w, http.StatusBadRequest, "invalid json")
		return nil, errors.New("empty body")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxJSONBodyBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			WriteErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the configured limit")
			return nil, err
		}
		WriteError(w, http.StatusBadRequest, "invalid json")
		return nil, err
	}
	return body, nil
}
