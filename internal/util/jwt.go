package util

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/goccy/go-json"
)

// JWTExpiry parses the exp claim from a JWT token and returns the expiry time.
// If skew is positive, the returned time is shifted earlier by that duration
// to allow for clock skew and proactive refresh.
func JWTExpiry(token string, skew time.Duration) time.Time {
	firstDot := strings.IndexByte(token, '.')
	if firstDot < 0 {
		return time.Time{}
	}
	rest := token[firstDot+1:]
	secondDot := strings.IndexByte(rest, '.')
	if secondDot < 0 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(rest[:secondDot])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp json.Number `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}
	}
	exp, err := claims.Exp.Int64()
	if err != nil || exp <= 0 {
		return time.Time{}
	}
	t := time.Unix(exp, 0)
	if skew > 0 {
		t = t.Add(-skew)
	}
	return t
}
