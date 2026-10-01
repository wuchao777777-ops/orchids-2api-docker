package util

import (
	"net/http"
	"orchids-api/internal/testutil"
	"testing"
	"time"
)

func TestParseRetryAfterProviderBudgets(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value       string
		limit, want time.Duration
	}{
		{" 12 ", 0, 12 * time.Second},
		{"120", 30 * time.Second, 30 * time.Second},
		{"120", 0, 120 * time.Second},
		{now.Add(time.Minute).Format(http.TimeFormat), 30 * time.Second, 30 * time.Second},
		{now.Add(time.Minute).Format(http.TimeFormat), 0, time.Minute},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0, 0},
		{"-1", 0, 0},
		{"invalid", 0, 0},
		{"9223372036854775807", 30 * time.Second, 30 * time.Second},
		{"9223372036854775807", 0, time.Duration(1<<63 - 1)},
	} {
		testutil.CheckEqual(t, ParseRetryAfter(tc.value, now, tc.limit), tc.want)
	}
}
