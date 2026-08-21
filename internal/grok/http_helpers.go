package grok

import (
	"net/http"

	"github.com/goccy/go-json"
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
