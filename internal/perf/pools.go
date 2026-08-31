// Package perf provides performance optimization utilities including object pools.
package perf

import (
	"strings"
	"sync"
)

// StringBuilderPool provides reusable strings.Builder instances.
var StringBuilderPool = sync.Pool{
	New: func() any { return &strings.Builder{} },
}

// AcquireStringBuilder gets a strings.Builder from the pool.
func AcquireStringBuilder() *strings.Builder {
	return StringBuilderPool.Get().(*strings.Builder)
}

// ReleaseStringBuilder returns a strings.Builder to the pool after resetting it.
func ReleaseStringBuilder(sb *strings.Builder) {
	if sb == nil {
		return
	}
	sb.Reset()
	StringBuilderPool.Put(sb)
}
