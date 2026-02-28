package grok

import (
	"github.com/goccy/go-json"
	"net/http"
	"strconv"
	"strings"
)

func (h *Handler) HandleAdminVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
	})
}

func (h *Handler) HandleAdminStorage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	storageType := "redis"
	if h != nil && h.cfg != nil && strings.TrimSpace(h.cfg.StoreMode) != "" {
		storageType = strings.ToLower(strings.TrimSpace(h.cfg.StoreMode))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"type": storageType,
	})
}

func (h *Handler) HandleAdminVoiceToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	voice := strings.TrimSpace(r.URL.Query().Get("voice"))
	if voice == "" {
		voice = "ara"
	}
	personality := strings.TrimSpace(r.URL.Query().Get("personality"))
	if personality == "" {
		personality = "assistant"
	}
	speed := 1.0
	if raw := strings.TrimSpace(r.URL.Query().Get("speed")); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v > 0 {
			speed = v
		}
	}

	acc, token, err := h.selectAccount(r.Context())
	if err != nil {
		http.Error(w, "no available grok token: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	data, err := h.client.getVoiceToken(r.Context(), token, voice, personality, speed)
	if err != nil {
		h.markAccountStatus(r.Context(), acc, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	respToken, _ := data["token"].(string)
	respToken = strings.TrimSpace(respToken)
	if respToken == "" {
		http.Error(w, "upstream returned no voice token", http.StatusBadGateway)
		return
	}

	out := map[string]interface{}{
		"token":            respToken,
		"url":              "wss://livekit.grok.com",
		"participant_name": "",
		"room_name":        "",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
