package api

import (
	"net/http"

	"orchids-api/internal/util"
)

// JSON writers are shared with inference endpoints through internal/util.

// requireMethod rejects every method but the one the endpoint serves. It
// reports false when it has already written the 405, so the caller only has to
// return.
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	writeMethodNotAllowed(w)
	return false
}

// writeMethodNotAllowed answers the shared 405 body for the handlers that
// dispatch on the method themselves and so cannot use requireMethod.
func writeMethodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// writeCodeEnvelope answers the {"code":...} envelope the console's settings
// endpoints share: 0 carries the payload, 1 carries an operator-facing message.
// Absent fields are left out rather than sent as null, so the shape a client
// sees is the shape it already parses.
func writeCodeEnvelope(w http.ResponseWriter, code int, data interface{}, msg string) {
	payload := map[string]interface{}{"code": code}
	if data != nil {
		payload["data"] = data
	}
	if msg != "" {
		payload["msg"] = msg
	}
	util.WriteJSON(w, payload)
}
