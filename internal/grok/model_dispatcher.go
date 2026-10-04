package grok

import (
	"context"
	"net/http"

	"orchids-api/internal/dispatch"
)

// ModelDispatcher routes a unified inference endpoint to the handler that owns
// the requested model. The decision is made from the request body alone, so it
// is protocol-neutral and now lives in internal/dispatch.
func ModelDispatcher(native, bridged http.HandlerFunc, isNativeModel func(context.Context, string) (bool, error)) http.HandlerFunc {
	return dispatch.Model(native, bridged, isNativeModel)
}
