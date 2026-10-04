// Package dispatch routes one unified inference endpoint to the handler that
// owns the requested model.
//
// A single base URL serves every channel's models, so /v1/chat/completions,
// /v1/messages and /v1/responses each have to decide whether the model belongs
// to the native Build plane or to a chat-only channel. The decision is made
// from the request body and nothing else, which is why it lives on its own.
package dispatch

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"encoding/json"

	"orchids-api/internal/middleware"
)

// MaxBodyBytes bounds the body the dispatcher will read to find the model.
const MaxBodyBytes = 32 << 20

// Model routes a unified request by model: models the native handler serves
// keep it, every other model goes to the bridged handler, which resolves its
// channel from the model store.
//
// Only POST bodies are inspected, and only to read the model: the body is
// handed to the chosen handler untouched. The model that decided the routing is
// published on the context, so the chosen handler and anything it calls can read
// the same resolution instead of looking it up a second time.
//
// isNativeModel errors rather than guessing. A lookup failure means the channel
// is unknown, and sending an unknown model to the native handler answers with
// "model does not exist" — a 400 that hides the real problem (a store outage,
// or a model that belongs to another channel). Such a request goes to the
// bridged handler, which resolves channels properly and reports a
// channel-aware error.
func Model(native, bridged http.HandlerFunc, isNativeModel func(context.Context, string) (bool, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			native(w, r)
			return
		}
		if r.ContentLength > MaxBodyBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "request body too large") {
				writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			// The body is already half-read; neither handler can produce a
			// meaningful answer, so fail where the fault is.
			writeError(w, http.StatusBadRequest, "failed to read request body")
			return
		}
		// The body is only inspected to pick a provider; both handlers parse it
		// themselves, so it is handed back untouched.
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))

		var probe struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(body, &probe); err != nil {
			// A malformed body has no model to route on. The native handler owns
			// the error response for its own wire format.
			native(w, r)
			return
		}
		if isNativeModel != nil {
			nativeModel, lookupErr := isNativeModel(r.Context(), probe.Model)
			if lookupErr == nil {
				// Publish the model only when the routing decision is trustworthy;
				// otherwise the bridged handler must resolve the channel itself.
				r = r.WithContext(middleware.WithRequestModel(r.Context(), probe.Model))
				if nativeModel {
					native(w, r)
					return
				}
			}
		}
		bridged(w, r)
	}
}
