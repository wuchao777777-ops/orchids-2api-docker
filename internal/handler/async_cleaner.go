package handler

import (
	"sync"
	"time"
)

// AsyncCleaner 管理后台清理任务
type AsyncCleaner struct {
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// NewAsyncCleaner 创建异步清理器
func NewAsyncCleaner(interval time.Duration) *AsyncCleaner {
	return &AsyncCleaner{
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start 启动后台清理
func (c *AsyncCleaner) Start(cleanFn func()) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()

		for {
			select {
			case <-c.stopCh:
				return
			case <-ticker.C:
				// Double-check stop signal before running cleanup
				select {
				case <-c.stopCh:
					return
				default:
				}

				// 使用 recover 防止 cleanFn panic 导致 goroutine 退出
				func() {
					defer func() {
						if r := recover(); r != nil {
							// 静默恢复，继续执行后续清理
						}
					}()
					cleanFn()
				}()
			}
		}
	}()
}

// Stop 停止清理器
func (c *AsyncCleaner) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
	c.wg.Wait()
}
