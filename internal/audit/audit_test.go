package audit

import (
	"context"
	"orchids-api/internal/testutil"
	"testing"
	"time"

	"encoding/json"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestUsageAccountingFieldsRoundTrip(t *testing.T) {
	logger, _ := setupRedisLogger(t)
	logger.Log(context.Background(), Event{Action: "usage", Status: "ok", InputTokens: 10, CachedInputTokens: 4, OutputTokens: 5, ReasoningTokens: 3, TotalTokens: 15, UsageSource: UsageSourceUpstream})
	logger.Close()
	events := readLoggedEvents(t, logger, 1)
	testutil.Falsef(t, len(events) != 1 || events[0].UsageSource != UsageSourceUpstream || events[0].TotalTokens != 15 || events[0].CachedInputTokens != 4 || events[0].ReasoningTokens != 3, "event=%+v", events)
}

func setupRedisLogger(t *testing.T) (*RedisLogger, *miniredis.Miniredis) {
	t.Helper()
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	logger := NewRedisLogger(client, "test:", 1000)
	return logger, s
}

func readLoggedEvents(t *testing.T, logger *RedisLogger, count int64) []Event {
	t.Helper()
	msgs, err := logger.client.XRevRangeN(context.Background(), logger.streamKey, "+", "-", count).Result()
	testutil.NoError(t, err)
	events := make([]Event, 0, len(msgs))
	for _, msg := range msgs {
		data, ok := msg.Values["data"].(string)
		testutil.True(t, ok, "audit event data has type %T")
		var event Event
		testutil.NoError(t, json.Unmarshal([]byte(data), &event))
		events = append(events, event)
	}
	return events
}

func TestRedisLoggerLog(t *testing.T) {
	logger, _ := setupRedisLogger(t)
	ctx := context.Background()

	logger.Log(ctx, Event{
		Action:    "chat_request",
		AccountID: 1,
		Model:     "claude-sonnet-4-5",
		Status:    "success",
		Duration:  150,
	})

	logger.Log(ctx, Event{
		Action:    "image_generate",
		AccountID: 2,
		Status:    "error",
		Error:     "timeout",
	})

	// Close drains the async queue. A fixed sleep races the writer on loaded CI.
	logger.Close()

	events := readLoggedEvents(t, logger, 10)
	testutil.Equal(t, len(events), 2)

	// Results are reverse-chronological
	testutil.Equal(t, events[0].Action, "image_generate")
	testutil.Equal(t, events[1].Action, "chat_request")
}

func TestRedisLoggerTimestamp(t *testing.T) {
	logger, _ := setupRedisLogger(t)
	ctx := context.Background()

	before := time.Now()
	logger.Log(ctx, Event{Action: "test", Status: "success"})
	logger.Close()

	events := readLoggedEvents(t, logger, 1)
	testutil.Equal(t, len(events), 1)
	testutil.False(t, events[0].Timestamp.Before(before), "timestamp should be after log call")
}

func TestLegacyEventUsageFieldsDefaultEmpty(t *testing.T) {
	var decoded Event
	testutil.NoError(t, json.Unmarshal([]byte(`{"action":"chat_request","status":"success","input_tokens":3,"output_tokens":4}`), &decoded))
	testutil.Equal(t, decoded.TotalTokens, 0)
	testutil.Equal(t, decoded.UsageSource, "")
}

func TestNopLogger(t *testing.T) {
	logger := NewNopLogger()
	ctx := context.Background()

	// Should not panic
	logger.Log(ctx, Event{Action: "test", Status: "success"})
}
