package util

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-json"
)

// WriteJSON writes the same JSON envelope for admin and inference endpoints.
func WriteJSON(w http.ResponseWriter, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func WriteJSONStatus(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ParseRetryAfter accepts seconds or an HTTP date. A positive limit preserves
// each provider's wait budget; zero leaves the duration uncapped.
func ParseRetryAfter(value string, now time.Time, limit time.Duration) time.Duration {
	value = strings.TrimSpace(value)
	var delay time.Duration
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		if limit > 0 && seconds > int64(limit/time.Second) {
			return limit
		}
		// Saturate before multiplication to avoid negative waits on overflow.
		if seconds > int64((1<<63-1)/time.Second) {
			delay = time.Duration(1<<63 - 1)
		} else {
			delay = time.Duration(seconds) * time.Second
		}
	} else if at, err := http.ParseTime(value); err == nil {
		delay = at.Sub(now)
	}
	if delay <= 0 {
		return 0
	}
	if limit > 0 && delay > limit {
		return limit
	}
	return delay
}
