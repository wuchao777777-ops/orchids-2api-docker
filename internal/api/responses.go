package api

import (
	"net/http"

	"github.com/goccy/go-json"
)

// Shared response writers for the admin API.
//
// Every console endpoint answers the same two steps — declare the content type,
// then encode — and most of them serve exactly one HTTP method. Those two steps
// used to be spelled out in each handler, so an endpoint added later could
// easily answer with a different content type or a different 405 body than its
// neighbours. One writer each keeps the wire shape in a single place.

// writeJSON answers one JSON body on 200.
func writeJSON(w http.ResponseWriter, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// writeJSONStatus answers one JSON body with an explicit status.
func writeJSONStatus(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

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
	writeJSON(w, payload)
}
