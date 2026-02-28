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
	Timestamp time.Time              `json:"timestamp"`
	Action    string                 `json:"action"`
	AccountID int64                  `json:"account_id,omitempty"`
	Model     string                 `json:"model,omitempty"`
	Channel   string                 `json:"channel,omitempty"`
	ClientIP  string                 `json:"client_ip,omitempty"`
	UserAgent string                 `json:"user_agent,omitempty"`
	Duration  int64                  `json:"duration_ms,omitempty"`
	Status    string                 `json:"status"`
	Error     string                 `json:"error,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// QueryOpts controls audit log queries.
type QueryOpts struct {
	Start  time.Time
	End    time.Time
	Action string
	Limit  int64
}

// Logger is the audit logging interface.
type Logger interface {
	Log(ctx context.Context, event Event)
	Query(ctx context.Context, opts QueryOpts) ([]Event, error)
	Close()
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

func (l *RedisLogger) Query(ctx context.Context, opts QueryOpts) ([]Event, error) {
	start := "-"
	end := "+"
	if !opts.Start.IsZero() {
		start = opts.Start.Format(time.RFC3339Nano)
	}
	if !opts.End.IsZero() {
		end = opts.End.Format(time.RFC3339Nano)
	}

	count := opts.Limit
	if count <= 0 {
		count = 100
	}

	msgs, err := l.client.XRevRangeN(ctx, l.streamKey, end, start, count).Result()
	if err != nil {
		return nil, err
	}

	events := make([]Event, 0, len(msgs))
	for _, msg := range msgs {
		data, ok := msg.Values["data"].(string)
		if !ok {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		events = append(events, ev)
	}
	return events, nil
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

func NewNopLogger() *NopLogger                                             { return &NopLogger{} }
func (l *NopLogger) Log(_ context.Context, _ Event)                        {}
func (l *NopLogger) Query(_ context.Context, _ QueryOpts) ([]Event, error) { return nil, nil }
func (l *NopLogger) Close()                                                {}
