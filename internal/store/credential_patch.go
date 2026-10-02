package store

import (
	"fmt"
	"strings"
	"time"
)

// Credential patches share optimistic rotation checks but retain provider-specific
// identity, runtime, quota, and cookie side effects in their adapters.
func applyCredentialPatch(channel, expected, access, refresh string, expires time.Time, accessTarget, refreshTarget *string, expiresTarget *time.Time) error {
	expected = strings.TrimSpace(expected)
	refresh = strings.TrimSpace(refresh)
	if expected != "" && *refreshTarget != expected && *refreshTarget != refresh {
		return fmt.Errorf("%s credential changed concurrently", channel)
	}
	patchString(accessTarget, access)
	patchString(refreshTarget, refresh)
	if !expires.IsZero() {
		*expiresTarget = expires
	}
	return nil
}

// Blank values mean no update; accepted identity/credential values are trimmed.
func patchString(target *string, incoming string) {
	if value := strings.TrimSpace(incoming); value != "" {
		*target = value
	}
}
