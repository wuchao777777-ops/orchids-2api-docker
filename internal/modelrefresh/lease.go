package modelrefresh

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
	"orchids-api/internal/util"
)

type refreshFunc func(ctx context.Context, cfg *config.Config, s *store.Store, channel string, concurrency int) (*Result, error)

var runModelRefresh refreshFunc = syncModelsForChannelConcurrent

type Coordinator struct {
	mu      sync.Mutex
	running map[string]struct{}
}

func NewCoordinator() *Coordinator {
	return &Coordinator{running: make(map[string]struct{})}
}

func (c *Coordinator) tryAcquire(channel string) (func(), bool) {
	if c == nil {
		return func() {}, true
	}
	key := strings.ToLower(strings.TrimSpace(channel))
	key = util.FirstNonEmptyUntrimmed(key, "*")
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.running[key]; exists {
		return nil, false
	}
	c.running[key] = struct{}{}
	return func() { c.mu.Lock(); delete(c.running, key); c.mu.Unlock() }, true
}

func acquireDistributedModelRefresh(ctx context.Context, s *store.Store, channel string) (func(), bool) {
	if s == nil || s.RedisClient() == nil {
		return func() {}, true
	}
	key := s.RedisPrefix() + "lease:model_refresh:" + strings.ToLower(strings.TrimSpace(channel))
	token := fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().UTC().Unix())
	acquired, err := s.RedisClient().SetNX(ctx, key, token, 30*time.Minute).Result()
	if err != nil || !acquired {
		return nil, false
	}
	return func() {
		// Delete only our own lease; an expired lease may already belong to a newer run.
		const releaseScript = `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) end return 0`
		_, _ = s.RedisClient().Eval(context.Background(), releaseScript, []string{key}, token).Result()
	}, true
}
