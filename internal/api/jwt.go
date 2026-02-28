package api

import (
	"encoding/base64"
	"github.com/goccy/go-json"
	"strings"
)

// isLikelyJWT returns true when the input looks like a JWT (three dot-separated parts).
// We keep this intentionally loose to accept most JWTs pasted by users.
func isLikelyJWT(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if len(p) < 10 {
			return false
		}
	}
	return true
}

// jwtHasRotatingToken tries to detect Clerk "__client" JWTs.
// In the reference project (AIClient-2-API), the __client cookie JWT may contain a
// "rotating_token" claim. That JWT is NOT the bearer token used for upstream calls;
// it should be treated as a client cookie input.
func jwtHasRotatingToken(jwt string) bool {
	claims, ok := decodeJWTPayload(jwt)
	if !ok {
		return false
	}
	_, exists := claims["rotating_token"]
	return exists
}

func decodeJWTPayload(jwt string) (map[string]any, bool) {
	jwt = strings.TrimSpace(jwt)
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil, false
	}
	payload := parts[1]
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		// Some JWTs include padding; fall back to standard URL encoding if needed.
		if b2, err2 := base64.URLEncoding.DecodeString(payload); err2 == nil {
			b = b2
		} else {
			return nil, false
		}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false
	}
	return m, true
}
