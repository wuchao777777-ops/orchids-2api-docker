package grok

import (
	"encoding/base64"
	"strings"

	"github.com/goccy/go-json"

	"orchids-api/internal/store"
)

// ApplyCLIOAuthIdentity copies only non-secret identity claims from an OAuth
// access-token JWT into the account. Claims are used as request metadata, not
// as authentication evidence; the xAI CLI endpoint remains authoritative.
func ApplyCLIOAuthIdentity(acc *store.Account) bool {
	if acc == nil {
		return false
	}
	parts := strings.Split(strings.TrimSpace(acc.OAuthAccessToken), ".")
	if len(parts) != 3 || strings.TrimSpace(parts[1]) == "" {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
		TeamID  string `json:"team_id"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return false
	}
	changed := false
	if value := strings.TrimSpace(claims.Subject); value != "" && strings.TrimSpace(acc.UserID) != value {
		acc.UserID = value
		changed = true
	}
	if value := strings.TrimSpace(claims.Email); value != "" && strings.TrimSpace(acc.Email) != value {
		acc.Email = value
		changed = true
	}
	if value := strings.TrimSpace(claims.TeamID); value != "" && strings.TrimSpace(acc.TeamID) != value {
		acc.TeamID = value
		changed = true
	}
	return changed
}
