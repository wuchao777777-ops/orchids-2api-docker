package dispatch

import (
	"net/http"

	"orchids-api/internal/responses"
)

// writeError answers an oversized or unreadable body. It uses the Responses
// error envelope because the unified endpoints the dispatcher guards all speak
// Responses to the client.
func writeError(w http.ResponseWriter, status int, message string) {
	responses.WriteAPIError(w, status, "invalid_request_error", message)
}
