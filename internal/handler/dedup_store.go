package handler

import (
	"context"
	"github.com/goccy/go-json"
	"time"

	"github.com/redis/go-redis/v9"
)

// DedupStore abstracts request deduplication.
type DedupStore interface {
	// Register checks if a request hash is a duplicate. Returns isDuplicate and hasInFlight.
	Register(ctx context.Context, hash string) (isDuplicate bool, hasInFlight bool)
	// Finish marks a request as completed (decrements in-flight count).
	Finish(ctx context.Context, hash string)
}

// --- Redis Implementation ---

// RedisDedupStore uses Lua scripts for atomic dedup checks with auto-expiring keys.
type RedisDedupStore struct {
	client         *redis.Client
	prefix         string
	window         time.Duration
	registerScript *redis.Script
	finishScript   *redis.Script
}

func NewRedisDedupStore(client *redis.Client, prefix string, window time.Duration) *RedisDedupStore {
	s := &RedisDedupStore{
		client: client,
		prefix: prefix + "dedup:",
		window: window,
	}

	// Register: atomically check duplicate and increment in-flight
	s.registerScript = redis.NewScript(`
		local key = KEYS[1]
		local now = tonumber(ARGV[1])
		local window_ms = tonumber(ARGV[2])
		local val = redis.call("GET", key)
		if val then
			local data = cjson.decode(val)
			if now - data.last <= window_ms then
				local has_inflight = false
				if data.inflight > 0 then has_inflight = true end
				return cjson.encode({dup=true, inflight=has_inflight})
			end
			data.last = now
			data.inflight = data.inflight + 1
			redis.call("SET", key, cjson.encode(data), "PX", 10000)
			return cjson.encode({dup=false, inflight=false})
		end
		redis.call("SET", key, cjson.encode({last=now, inflight=1}), "PX", 10000)
		return cjson.encode({dup=false, inflight=false})
	`)

	// Finish: atomically decrement in-flight and update timestamp
	s.finishScript = redis.NewScript(`
		local key = KEYS[1]
		local now = tonumber(ARGV[1])
		local val = redis.call("GET", key)
		if not val then return "OK" end
		local data = cjson.decode(val)
		if data.inflight > 0 then
			data.inflight = data.inflight - 1
		end
		data.last = now
		redis.call("SET", key, cjson.encode(data), "PX", 10000)
		return "OK"
	`)

	return s
}

type dedupResult struct {
	Dup      bool `json:"dup"`
	InFlight bool `json:"inflight"`
}

func (s *RedisDedupStore) Register(ctx context.Context, hash string) (bool, bool) {
	nowMs := time.Now().UnixMilli()
	windowMs := s.window.Milliseconds()
	result, err := s.registerScript.Run(ctx, s.client, []string{s.prefix + hash}, nowMs, windowMs).Result()
	if err != nil {
		return false, false
	}
	var dr dedupResult
	if err := json.Unmarshal([]byte(result.(string)), &dr); err != nil {
		return false, false
	}
	return dr.Dup, dr.InFlight
}

func (s *RedisDedupStore) Finish(ctx context.Context, hash string) {
	nowMs := time.Now().UnixMilli()
	s.finishScript.Run(ctx, s.client, []string{s.prefix + hash}, nowMs)
}

// --- Memory Implementation ---

// MemoryDedupStore wraps in-memory ShardedMap for backward compatibility.
type MemoryDedupStore struct {
	requests *ShardedMap[recentRequest]
	window   time.Duration
	cleaner  *AsyncCleaner
}

func NewMemoryDedupStore(window, cleanupWindow time.Duration) *MemoryDedupStore {
	s := &MemoryDedupStore{
		requests: NewShardedMap[recentRequest](),
		window:   window,
	}
	s.cleaner = NewAsyncCleaner(5 * time.Second)
	s.cleaner.Start(func() {
		now := time.Now()
		s.requests.RangeDelete(func(_ string, rec recentRequest) bool {
			return rec.inFlight == 0 && now.Sub(rec.last) > cleanupWindow
		})
	})
	return s
}

func (s *MemoryDedupStore) Register(_ context.Context, hash string) (bool, bool) {
	now := time.Now()
	var isDup, hasInFlight bool
	s.requests.Compute(hash, func(cur recentRequest, exists bool) (recentRequest, bool) {
		if exists && now.Sub(cur.last) <= s.window {
			isDup = true
			hasInFlight = cur.inFlight > 0
			return cur, true
		}
		if exists {
			cur.last = now
			cur.inFlight++
			return cur, true
		}
		return recentRequest{last: now, inFlight: 1}, true
	})
	return isDup, hasInFlight
}

func (s *MemoryDedupStore) Finish(_ context.Context, hash string) {
	now := time.Now()
	s.requests.Compute(hash, func(cur recentRequest, exists bool) (recentRequest, bool) {
		if !exists {
			return cur, false
		}
		if cur.inFlight > 0 {
			cur.inFlight--
		}
		cur.last = now
		return cur, true
	})
}
