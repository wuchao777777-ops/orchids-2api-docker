package tokencache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"
)

type PromptCache interface {
	CheckPromptCache(strategy string, systemTokens, toolsTokens int, systemText, toolsText string) (readTokens int, creationTokens int)
	GetStats(ctx context.Context) (int64, int64, error)
	Clear(ctx context.Context) error
	SetTTL(ttl time.Duration)
}

type MemoryPromptCache struct {
	mu          sync.RWMutex
	ttl         time.Duration
	maxEntries  int
	items       map[string]promptCacheItem
	sizeBytes   int64
	done        chan struct{}
	accessCount atomic.Uint64
}

type promptCacheItem struct {
	expiresAt  time.Time
	accessedAt time.Time
	size       int64
}

func NewMemoryPromptCache(ttl time.Duration, maxEntries ...int) *MemoryPromptCache {
	if ttl < 0 {
		ttl = 0
	}
	limit := 0
	if len(maxEntries) > 0 && maxEntries[0] > 0 {
		limit = maxEntries[0]
	}
	c := &MemoryPromptCache{
		ttl:        ttl,
		maxEntries: limit,
		items:      make(map[string]promptCacheItem),
		done:       make(chan struct{}),
	}
	go c.cleanupLoop()
	return c
}

func (c *MemoryPromptCache) cleanupLoop() {
	ticker := time.NewTicker(time.Minute * 5)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			c.mu.Lock()
			c.pruneExpiredLocked(time.Now())
			c.mu.Unlock()
		}
	}
}

func (c *MemoryPromptCache) SetTTL(ttl time.Duration) {
	if c == nil {
		return
	}
	if ttl < 0 {
		ttl = 0
	}
	c.mu.Lock()
	if c.ttl != ttl {
		c.ttl = ttl
		c.items = make(map[string]promptCacheItem)
		c.sizeBytes = 0
	}
	c.mu.Unlock()
}

func (c *MemoryPromptCache) CheckPromptCache(strategy string, systemTokens, toolsTokens int, systemText, toolsText string) (readTokens int, creationTokens int) {
	if c == nil {
		return 0, systemTokens + toolsTokens
	}

	// Strategy parsing
	// 0: System + Tools together
	// 1: Split (System and Tools separate)
	// 2: System only
	// 3: Tools only
	now := time.Now()
	expiresAt := time.Time{}
	if c.ttl > 0 {
		expiresAt = now.Add(c.ttl)
	}

	checkCache := func(key string, tokens int) (int, int) {
		if tokens <= 0 || key == "" {
			return 0, 0
		}

		c.mu.RLock()
		item, ok := c.items[key]
		if ok && (c.ttl == 0 || item.expiresAt.IsZero() || !now.After(item.expiresAt)) {
			c.mu.RUnlock()
			if c.accessCount.Add(1)%8 == 0 {
				c.mu.Lock()
				if it, ok := c.items[key]; ok {
					it.accessedAt = time.Now()
					c.items[key] = it
				}
				c.mu.Unlock()
			}
			return tokens, 0
		}
		c.mu.RUnlock()

		// Cache Miss - Need to Put
		size := int64(len(key)) + 8
		c.mu.Lock()
		if existing, ok := c.items[key]; ok {
			c.sizeBytes -= existing.size
		} else if c.maxEntries > 0 && len(c.items) >= c.maxEntries {
			c.evictLRULocked()
		}
		c.items[key] = promptCacheItem{
			expiresAt:  expiresAt,
			accessedAt: now,
			size:       size,
		}
		c.sizeBytes += size
		c.mu.Unlock()

		return 0, tokens
	}

	hash := func(text string) string {
		if text == "" {
			return ""
		}
		h := sha256.Sum256([]byte(text))
		return hex.EncodeToString(h[:])
	}
	checkPart := func(prefix, text string, tokens int) {
		if text == "" || tokens <= 0 {
			return
		}
		read, created := checkCache(hash(prefix+text), tokens)
		readTokens += read
		creationTokens += created
	}

	switch strategy {
	case "0": // Together
		readTokens, creationTokens = checkCache(hash("sys:"+systemText+"|tools:"+toolsText), systemTokens+toolsTokens)
	case "2": // System only
		checkPart("sys:", systemText, systemTokens)
		creationTokens += toolsTokens // Untracked
	case "3": // Tools only
		checkPart("tools:", toolsText, toolsTokens)
		creationTokens += systemTokens // Untracked
	default: // Split
		checkPart("sys:", systemText, systemTokens)
		checkPart("tools:", toolsText, toolsTokens)
	}

	return readTokens, creationTokens
}

func (c *MemoryPromptCache) evictLRULocked() {
	var lruKey string
	var lruTime time.Time
	first := true
	for k, item := range c.items {
		if first || item.accessedAt.Before(lruTime) {
			lruKey = k
			lruTime = item.accessedAt
			first = false
		}
	}
	if !first {
		c.sizeBytes -= c.items[lruKey].size
		delete(c.items, lruKey)
	}
}

func (c *MemoryPromptCache) GetStats(ctx context.Context) (int64, int64, error) {
	if c == nil {
		return 0, 0, nil
	}
	c.mu.Lock()
	c.pruneExpiredLocked(time.Now())
	count := int64(len(c.items))
	size := c.sizeBytes
	c.mu.Unlock()
	return count, size, nil
}

func (c *MemoryPromptCache) Clear(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	c.items = make(map[string]promptCacheItem)
	c.sizeBytes = 0
	c.mu.Unlock()
	return nil
}

func (c *MemoryPromptCache) pruneExpiredLocked(now time.Time) {
	if c.ttl <= 0 {
		return
	}
	for key, item := range c.items {
		if !item.expiresAt.IsZero() && now.After(item.expiresAt) {
			c.sizeBytes -= item.size
			delete(c.items, key)
		}
	}
}
