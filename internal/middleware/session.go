package middleware

import (
	"net/http"
	"strings"

	"github.com/goccy/go-json"

	"orchids-api/internal/auth"
	"orchids-api/internal/util"
)

func bearerToken(r *http.Request) string {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return ""
	}
	return strings.TrimSpace(authHeader[len(prefix):])
}

func writeBearerUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"detail": message,
	})
}

func SessionAuthDynamic(credentials func() (adminPass, adminToken string), next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		adminPass, adminToken := credentials()

		cookie, err := r.Cookie("session_token")
		if err == nil && auth.ValidateSessionToken(cookie.Value) {
			next(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		adminHeader := r.Header.Get("X-Admin-Token")
		secrets := [...]string{adminToken, adminPass}
		for _, secret := range secrets {
			if secret != "" && (util.SecureCompare(authHeader, "Bearer "+secret) ||
				util.SecureCompare(authHeader, secret) || util.SecureCompare(adminHeader, secret)) {
				next(w, r)
				return
			}
		}

		queryKeys := []string{
			strings.TrimSpace(r.URL.Query().Get("app_key")),
			strings.TrimSpace(r.URL.Query().Get("public_key")),
		}
		for _, queryKey := range queryKeys {
			if queryKey == "" {
				continue
			}
			for _, secret := range secrets {
				if secret != "" && util.SecureCompare(queryKey, secret) {
					next(w, r)
					return
				}
			}
		}

		_, pass, ok := r.BasicAuth()
		if ok && util.SecureCompare(pass, adminPass) {
			next(w, r)
			return
		}

		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}
}

func SessionAuth(adminPass, adminToken string, next http.HandlerFunc) http.HandlerFunc {
	return SessionAuthDynamic(func() (string, string) {
		return adminPass, adminToken
	}, next)
}

func PublicKeyAuth(publicKey string, next http.HandlerFunc) http.HandlerFunc {
	key := strings.TrimSpace(publicKey)
	return func(w http.ResponseWriter, r *http.Request) {
		if key == "" {
			// Project override: empty public_key means no auth on public APIs.
			next(w, r)
			return
		}

		token := bearerToken(r)
		if token == "" {
			writeBearerUnauthorized(w, "Missing authentication token")
			return
		}
		if !util.SecureCompare(token, key) {
			writeBearerUnauthorized(w, "Invalid authentication token")
			return
		}
		next(w, r)
	}
}

func PublicImagineStreamAuth(publicKey string, next http.HandlerFunc) http.HandlerFunc {
	key := strings.TrimSpace(publicKey)
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
		if taskID != "" {
			next(w, r)
			return
		}

		if key == "" {
			// Project override: empty public_key means no auth on public APIs.
			next(w, r)
			return
		}

		queryKey := strings.TrimSpace(r.URL.Query().Get("public_key"))
		if queryKey == "" {
			if token := bearerToken(r); util.SecureCompare(token, key) {
				next(w, r)
				return
			}
			writeBearerUnauthorized(w, "Missing authentication token")
			return
		}
		if !util.SecureCompare(queryKey, key) {
			writeBearerUnauthorized(w, "Invalid authentication token")
			return
		}
		next(w, r)
	}
}
