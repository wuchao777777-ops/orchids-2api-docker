package util

import "sync"

// ReadSnapshot copies a field under its owner's read lock. Passing the field
// address does not read its value before locking; no getter closure is needed.
func ReadSnapshot[T any](mu *sync.RWMutex, value *T) T {
	mu.RLock()
	defer mu.RUnlock()
	return *value
}
