package perf

import (
	"sync"
	"time"
)

type CacheItem struct {
	Value      interface{}
	Error      string // Cached error message (empty if no error)
	Expiration int64
}

type TTLCache struct {
	items      map[string]CacheItem
	mu         sync.RWMutex
	ttl        time.Duration
	maxEntries int
	done       chan struct{}
}

func NewTTLCache(ttl time.Duration, maxEntries ...int) *TTLCache {
	max := 0
	if len(maxEntries) > 0 && maxEntries[0] > 0 {
		max = maxEntries[0]
	}
	c := &TTLCache{
		items:      make(map[string]CacheItem),
		ttl:        ttl,
		maxEntries: max,
		done:       make(chan struct{}),
	}
	go c.cleanupLoop()
	return c
}

func (c *TTLCache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; !ok && c.maxEntries > 0 && len(c.items) >= c.maxEntries {
		c.evictOldestLocked()
	}
	c.items[key] = CacheItem{
		Value:      value,
		Error:      "",
		Expiration: time.Now().Add(c.ttl).UnixNano(),
	}
}

func (c *TTLCache) SetError(key string, errMsg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; !ok && c.maxEntries > 0 && len(c.items) >= c.maxEntries {
		c.evictOldestLocked()
	}
	c.items[key] = CacheItem{
		Value:      nil,
		Error:      errMsg,
		Expiration: time.Now().Add(c.ttl).UnixNano(),
	}
}

func (c *TTLCache) evictOldestLocked() {
	var oldestKey string
	var oldestExp int64
	first := true
	for k, item := range c.items {
		if first || item.Expiration < oldestExp {
			oldestKey = k
			oldestExp = item.Expiration
			first = false
		}
	}
	if !first {
		delete(c.items, oldestKey)
	}
}

func (c *TTLCache) Get(key string) (interface{}, string, bool) {
	c.mu.RLock()
	item, ok := c.items[key]
	c.mu.RUnlock()

	if !ok {
		return nil, "", false
	}

	// Check expiration
	if time.Now().UnixNano() > item.Expiration {
		// Lazily delete expired item
		c.mu.Lock()
		if current, ok := c.items[key]; ok && current.Expiration == item.Expiration {
			delete(c.items, key)
		}
		c.mu.Unlock()
		return nil, "", false
	}

	return item.Value, item.Error, true
}

func (c *TTLCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]CacheItem)
}

func (c *TTLCache) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now().UnixNano()
			c.mu.Lock()
			for key, item := range c.items {
				if now > item.Expiration {
					delete(c.items, key)
				}
			}
			c.mu.Unlock()
		case <-c.done:
			return
		}
	}
}

// Close 停止后台清理 goroutine
func (c *TTLCache) Close() {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}
