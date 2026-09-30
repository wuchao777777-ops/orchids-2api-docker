package qoder

import (
	"strings"
	"testing"
	"time"

	"orchids-api/internal/upstream"
)

// TestParseQuotaNoticeReadsTheExhaustionFrame pins the frame the gateway used to
// throw away. It counted NOTIFICATIONS frames and discarded the payload, so an
// account the upstream had already told us was spent kept its stale "has
// credits" snapshot until the next periodic sync — which is how a request stayed
// routable onto an account whose window had already closed.
func TestParseQuotaNoticeReadsTheExhaustionFrame(t *testing.T) {
	t.Parallel()

	payload := `{"notifications":[{"extras":{"pricingUrl":"https://qoder.com/pricing?client=qoder","nextResetAt":1791397354935},"isHighestTier":false,"notificationType":"quota_exceeded"}]}`
	notice := parseQuotaNotice("NOTIFICATIONS", payload)
	if notice == nil {
		t.Fatal("parseQuotaNotice returned nil for a quota_exceeded frame")
	}
	if !notice.Exhausted {
		t.Error("Exhausted = false; a quota_exceeded frame is the account's own verdict that it is spent")
	}
	if !notice.NextResetAt.Equal(time.Unix(1791397354, 0)) {
		t.Errorf("NextResetAt = %v, want %v", notice.NextResetAt, time.Unix(1791397354, 0))
	}
	if notice.Kind != "NOTIFICATIONS" {
		t.Errorf("Kind = %q, want NOTIFICATIONS", notice.Kind)
	}
	if notice.UpgradeURL != "" {
		t.Errorf("UpgradeURL = %q, want it empty when the frame omits one", notice.UpgradeURL)
	}
}

// TestParseQuotaNoticeIgnoresAdvisoryOnlyFrames guards the other direction: a
// quota_low notice is advice, not a verdict, and treating it as exhaustion would
// park a healthy account on the strength of a warning.
func TestParseQuotaNoticeIgnoresAdvisoryOnlyFrames(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		kind    string
		payload string
	}{
		"quota_low is not exhaustion": {
			"NOTIFICATIONS",
			`{"notifications":[{"extras":{"nextResetAt":1791397354935},"notificationType":"quota_low"}]}`,
		},
		"another kind is not a quota notice": {
			"HEARTBEAT",
			`{"notifications":[{"extras":{},"notificationType":"quota_exceeded"}]}`,
		},
		"an empty payload is not a notice":              {"NOTIFICATIONS", ""},
		"a malformed payload is not a notice":           {"NOTIFICATIONS", `{"notifications":`},
		"a frame with no notifications is not a notice": {"NOTIFICATIONS", `{"notifications":[]}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := parseQuotaNotice(tc.kind, tc.payload); got != nil {
				t.Fatalf("parseQuotaNotice(%q, %.40q) = %#v, want nil", tc.kind, tc.payload, got)
			}
		})
	}
}

// TestParseQuotaNoticeToleratesAFrameTerminator covers the split: the control
// frame occasionally keeps its own trailing '#', and a payload that is one byte
// too long must not read as malformed — that would silently lose the one
// authoritative statement of when the window reopens.
func TestParseQuotaNoticeToleratesAFrameTerminator(t *testing.T) {
	t.Parallel()

	payload := `{"notifications":[{"extras":{"nextResetAt":1791397354935},"notificationType":"quota_exceeded"}]}#`
	notice := parseQuotaNotice("NOTIFICATIONS", payload)
	if notice == nil {
		t.Fatal("parseQuotaNotice dropped a frame with a trailing terminator")
	}
	if !notice.NextResetAt.Equal(time.Unix(1791397354, 0)) {
		t.Errorf("NextResetAt = %v, want %v", notice.NextResetAt, time.Unix(1791397354, 0))
	}
}

// TestQuotaExceededFrameReachesTheAttemptResult is the end-to-end half: the
// notice has to survive consumeStreamWithTools, because that is the only place
// the account-facing record is written from.
func TestQuotaExceededFrameReachesTheAttemptResult(t *testing.T) {
	t.Parallel()

	notice := `{"headers":{},"body":"[NOTIFICATIONS]#{\"notifications\":[{\"extras\":{\"pricingUrl\":\"https://qoder.com/pricing\",\"nextResetAt\":1791397354935},\"isHighestTier\":false,\"notificationType\":\"quota_exceeded\"}]}","statusCodeValue":200,"statusCode":"OK"}`
	answer := `{"headers":{"Content-Type":["application/json"]},"body":"{\"choices\":[{\"delta\":{\"content\":\"OK\",\"role\":\"assistant\"},\"index\":0}],\"created\":1,\"id\":\"chatcmpl-1\",\"model\":\"auto\",\"object\":\"chat.completion.chunk\"}","statusCodeValue":200,"statusCode":"OK"}`
	stream := "data:" + notice + "\n\n" + "data:" + answer + "\n\n" + "event:finish\n\n"

	var text strings.Builder
	res, err := consumeStreamWithTools(strings.NewReader(stream), false, func(m upstream.SSEMessage) {
		if m.Type == "model.text-delta" {
			if delta, ok := m.Event["delta"].(string); ok {
				text.WriteString(delta)
			}
		}
	})
	if err != nil {
		t.Fatalf("a quota_exceeded control frame aborted the stream: %v", err)
	}
	if text.String() != "OK" {
		t.Fatalf("text = %q, want the answer that followed the notice", text.String())
	}
	if res.QuotaNotice == nil {
		t.Fatal("QuotaNotice = nil; the account never learns its window closed")
	}
	if !res.QuotaNotice.Exhausted {
		t.Error("QuotaNotice.Exhausted = false, want true")
	}
	if !res.QuotaNotice.NextResetAt.Equal(time.Unix(1791397354, 0)) {
		t.Errorf("QuotaNotice.NextResetAt = %v, want %v", res.QuotaNotice.NextResetAt, time.Unix(1791397354, 0))
	}
	if res.QuotaNotice.PricingURL != "https://qoder.com/pricing" {
		t.Errorf("PricingURL = %q", res.QuotaNotice.PricingURL)
	}
}
