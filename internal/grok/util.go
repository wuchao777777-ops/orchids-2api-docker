package grok

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"encoding/json"

	"orchids-api/internal/util"
)

var (
	allowedMessageRoles = map[string]struct{}{
		"developer": {},
		"system":    {},
		"user":      {},
		"assistant": {},
		"tool":      {},
	}
	userContentTypes = map[string]struct{}{
		"text":        {},
		"image_url":   {},
		"input_audio": {},
		"file":        {},
		// The Anthropic Messages front end lowers its blocks onto this same
		// validator, so the Responses-shaped parts it produces are valid here
		// too. Without them a document or a multi-part tool_result is rejected
		// before the request ever reaches the upstream.
		"input_text":  {},
		"input_image": {},
		"input_file":  {},
	}
)

func randomHex(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

func randomUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4],
		buf[4:6],
		buf[6:8],
		buf[8:10],
		buf[10:16],
	)
}

// firstNonEmpty delegates to the shared implementation in internal/util so the
// package keeps its short local name without duplicating the logic.
func firstNonEmpty(values ...string) string { return util.FirstNonEmpty(values...) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func uniqueStrings(input []string) []string { return util.UniqueStrings(input) }

func interfaceToInt(v interface{}) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return i
		}
	}
	return 0
}

func interfaceSlice(v interface{}) []interface{} {
	switch x := v.(type) {
	case []interface{}:
		return x
	default:
		return nil
	}
}
