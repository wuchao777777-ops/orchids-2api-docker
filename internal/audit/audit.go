package audit

import (
	"context"
	"github.com/goccy/go-json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Event represents a single audit log entry.
type Event struct {
	Timestamp         time.Time              `json:"timestamp"`
	RequestID         string                 `json:"request_id,omitempty"`
	Action            string                 `json:"action"`
	APIKeyID          int64                  `json:"api_key_id,omitempty"`
	AccountID         int64                  `json:"account_id,omitempty"`
	Model             string                 `json:"model,omitempty"`
	Channel           string                 `json:"channel,omitempty"`
	Provider          string                 `json:"provider,omitempty"`
	Attempt           int                    `json:"attempt,omitempty"`
	InputTokens       int                    `json:"input_tokens,omitempty"`
	OutputTokens      int                    `json:"output_tokens,omitempty"`
	CachedInputTokens int                    `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   int                    `json:"reasoning_tokens,omitempty"`
	ClientIP          string                 `json:"client_ip,omitempty"`
	UserAgent         string                 `json:"user_agent,omitempty"`
	Duration          int64                  `json:"duration_ms,omitempty"`
	Status            string                 `json:"status"`
	Error             string                 `json:"error,omitempty"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

// Logger is the audit logging interface.
type Logger interface {
	Log(ctx context.Context, event Event)
}

// --- Redis Stream Implementation ---

// RedisLogger writes audit events to a Redis Stream with async buffering.
type RedisLogger struct {
	client    *redis.Client
	streamKey string
	maxLen    int64
	eventCh   chan Event
	done      chan struct{}
}

// NewRedisLogger creates an audit logger backed by Redis Streams.
func NewRedisLogger(client *redis.Client, prefix string, maxLen int64) *RedisLogger {
	if maxLen <= 0 {
		maxLen = 10000
	}
	l := &RedisLogger{
		client:    client,
		streamKey: prefix + "audit:log",
		maxLen:    maxLen,
		eventCh:   make(chan Event, 256),
		done:      make(chan struct{}),
	}
	go l.writeLoop()
	return l
}

func (l *RedisLogger) Log(_ context.Context, event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	select {
	case l.eventCh <- event:
	default:
		// Channel full, drop event to avoid blocking request path
		slog.Warn("Audit log buffer full, dropping event", "action", event.Action)
	}
}

func (l *RedisLogger) Close() {
	close(l.eventCh)
	<-l.done
}

func (l *RedisLogger) writeLoop() {
	defer close(l.done)
	for event := range l.eventCh {
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		l.client.XAdd(ctx, &redis.XAddArgs{
			Stream: l.streamKey,
			MaxLen: l.maxLen,
			Approx: true,
			Values: map[string]interface{}{
				"data":   string(data),
				"action": event.Action,
				"status": event.Status,
			},
		}).Err()
		cancel()
	}
}

// --- Nop Implementation ---

// NopLogger discards all audit events.
type NopLogger struct{}

func NewNopLogger() *NopLogger                      { return &NopLogger{} }
func (l *NopLogger) Log(_ context.Context, _ Event) {}
