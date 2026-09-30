package selfupdate

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	w.Header().Set("Allow", want)
	respond(w, 405, map[string]string{"message": "Method not allowed"})
	return false
}
func (m *Manager) HandleVersion(w http.ResponseWriter, r *http.Request) {
	if method(w, r, "GET") {
		respond(w, 200, map[string]any{"current": m.Info, "can_update": m.Reason == "", "reason": m.Reason})
	}
}
func (m *Manager) HandleCheck(w http.ResponseWriter, r *http.Request) {
	if method(w, r, "GET") {
		respond(w, 200, m.Check(r.Context(), r.URL.Query().Get("force") == "true"))
	}
}
func (m *Manager) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	op, err := m.Status()
	if err != nil {
		if !os.IsNotExist(err) {
			respond(w, 500, map[string]string{"message": "无法读取升级状态，请检查服务日志"})
			return
		}
		respond(w, 200, map[string]any{"operation": nil})
		return
	}
	respond(w, 200, map[string]any{"operation": op})
}
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	if raw := r.Header.Get("Origin"); raw != "" {
		u, e := url.Parse(raw)
		return e == nil && strings.EqualFold(u.Host, r.Host) && (u.Scheme == "https" || u.Scheme == "http")
	}
	return true // CLI bearer/basic admin clients may omit Origin.
}
func (m *Manager) HandleAction(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, "POST") {
			return
		}
		if !sameOrigin(r) {
			respond(w, 403, map[string]string{"message": "跨站升级请求被拒绝"})
			return
		}
		if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			respond(w, 415, map[string]string{"message": "需要 application/json"})
			return
		}
		var body struct {
			Version string `json:"version"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			respond(w, 400, map[string]string{"message": "无效请求"})
			return
		}
		op, err := m.Start(kind, body.Version, r.Header.Get("Idempotency-Key"))
		if err != nil {
			respond(w, 409, map[string]string{"message": err.Error()})
			return
		}
		respond(w, 202, map[string]any{"operation": op})
	}
}
