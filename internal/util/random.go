package util

import (
	"crypto/rand"
	"encoding/hex"
)

// RandomHex returns size random bytes as lowercase hex. It is used for the
// synthetic identifiers the gateway mints, so a collision is not a correctness
// question but the output is still unpredictable.
func RandomHex(size int) string {
	if size <= 0 {
		return ""
	}
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}
