package handler

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupRedisSessionStore(t *testing.T) (*RedisSessionStore, *miniredis.Miniredis) {
	t.Helper()
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	store := NewRedisSessionStore(client, "test:", 30*time.Minute)
	return store, s
}

func TestRedisSessionStoreWorkdir(t *testing.T) {
	store, _ := setupRedisSessionStore(t)
	ctx := context.Background()

	// Miss
	_, ok := store.GetWorkdir(ctx, "session1")
	if ok {
		t.Fatal("expected miss")
	}

	// Set and get
	store.SetWorkdir(ctx, "session1", "/home/user")
	dir, ok := store.GetWorkdir(ctx, "session1")
	if !ok || dir != "/home/user" {
		t.Fatalf("expected /home/user, got %q (ok=%v)", dir, ok)
	}
}

func TestRedisSessionStoreConvID(t *testing.T) {
	store, _ := setupRedisSessionStore(t)
	ctx := context.Background()

	// Miss
	_, ok := store.GetConvID(ctx, "session1")
	if ok {
		t.Fatal("expected miss")
	}

	// Set and get
	store.SetConvID(ctx, "session1", "conv_abc123")
	id, ok := store.GetConvID(ctx, "session1")
	if !ok || id != "conv_abc123" {
		t.Fatalf("expected conv_abc123, got %q (ok=%v)", id, ok)
	}
}

func TestRedisSessionStoreBoltProjectID(t *testing.T) {
	store, _ := setupRedisSessionStore(t)
	ctx := context.Background()

	_, ok := store.GetBoltProjectID(ctx, "bolt:1")
	if ok {
		t.Fatal("expected miss")
	}

	store.SetBoltProjectID(ctx, "bolt:1", "sb1-demo")
	projectID, ok := store.GetBoltProjectID(ctx, "bolt:1")
	if !ok || projectID != "sb1-demo" {
		t.Fatalf("expected sb1-demo, got %q (ok=%v)", projectID, ok)
	}
}

func TestRedisSessionStoreBoltToolNames(t *testing.T) {
	store, _ := setupRedisSessionStore(t)
	ctx := context.Background()

	_, ok := store.GetBoltToolNames(ctx, "bolt:1")
	if ok {
		t.Fatal("expected miss")
	}

	store.SetBoltToolNames(ctx, "bolt:1", []string{"Read", "Write", "Edit"})
	toolNames, ok := store.GetBoltToolNames(ctx, "bolt:1")
	if !ok {
		t.Fatal("expected bolt tool names to be stored")
	}
	if got := len(toolNames); got != 3 {
		t.Fatalf("expected 3 tool names, got %d (%#v)", got, toolNames)
	}
	if toolNames[0] != "Read" || toolNames[1] != "Write" || toolNames[2] != "Edit" {
		t.Fatalf("unexpected tool names %#v", toolNames)
	}
}

func TestRedisSessionStoreDelete(t *testing.T) {
	store, _ := setupRedisSessionStore(t)
	ctx := context.Background()

	store.SetWorkdir(ctx, "session1", "/tmp")
	store.SetConvID(ctx, "session1", "conv_xyz")

	store.DeleteSession(ctx, "session1")

	_, ok := store.GetWorkdir(ctx, "session1")
	if ok {
		t.Fatal("workdir should be deleted")
	}
	_, ok = store.GetConvID(ctx, "session1")
	if ok {
		t.Fatal("convID should be deleted")
	}
}

func TestRedisSessionStoreTTL(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	store := NewRedisSessionStore(client, "test:", 1*time.Second)
	ctx := context.Background()

	store.SetWorkdir(ctx, "session1", "/tmp")
	s.FastForward(2 * time.Second)

	_, ok := store.GetWorkdir(ctx, "session1")
	if ok {
		t.Fatal("session should have expired")
	}
}

func TestRedisSessionStoreTouch(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	store := NewRedisSessionStore(client, "test:", 3*time.Second)
	ctx := context.Background()

	store.SetWorkdir(ctx, "session1", "/tmp")

	// Advance 2 seconds, then touch
	s.FastForward(2 * time.Second)
	store.Touch(ctx, "session1")

	// Advance another 2 seconds (total 4s from creation, but only 2s from touch)
	s.FastForward(2 * time.Second)

	_, ok := store.GetWorkdir(ctx, "session1")
	if !ok {
		t.Fatal("session should still exist after touch")
	}
}

// --- Memory tests ---

func TestMemorySessionStoreWorkdir(t *testing.T) {
	store := NewMemorySessionStore(30*time.Minute, 100)
	ctx := context.Background()

	_, ok := store.GetWorkdir(ctx, "s1")
	if ok {
		t.Fatal("expected miss")
	}

	store.SetWorkdir(ctx, "s1", "/home")
	dir, ok := store.GetWorkdir(ctx, "s1")
	if !ok || dir != "/home" {
		t.Fatalf("expected /home, got %q", dir)
	}
}

func TestMemorySessionStoreCleanup(t *testing.T) {
	store := NewMemorySessionStore(100*time.Millisecond, 100)
	ctx := context.Background()

	store.SetWorkdir(ctx, "s1", "/tmp")
	time.Sleep(150 * time.Millisecond)
	store.Cleanup(ctx)

	_, ok := store.GetWorkdir(ctx, "s1")
	if ok {
		t.Fatal("session should have been cleaned up")
	}
}

func TestMemorySessionStoreBoltProjectID(t *testing.T) {
	store := NewMemorySessionStore(30*time.Minute, 100)
	ctx := context.Background()

	_, ok := store.GetBoltProjectID(ctx, "bolt:1")
	if ok {
		t.Fatal("expected miss")
	}

	store.SetBoltProjectID(ctx, "bolt:1", "sb1-demo")
	projectID, ok := store.GetBoltProjectID(ctx, "bolt:1")
	if !ok || projectID != "sb1-demo" {
		t.Fatalf("expected sb1-demo, got %q (ok=%v)", projectID, ok)
	}
}

func TestMemorySessionStoreBoltToolNames(t *testing.T) {
	store := NewMemorySessionStore(30*time.Minute, 100)
	ctx := context.Background()

	_, ok := store.GetBoltToolNames(ctx, "bolt:1")
	if ok {
		t.Fatal("expected miss")
	}

	store.SetBoltToolNames(ctx, "bolt:1", []string{"Read", "Write", "Edit"})
	toolNames, ok := store.GetBoltToolNames(ctx, "bolt:1")
	if !ok {
		t.Fatal("expected bolt tool names to be stored")
	}
	if got := len(toolNames); got != 3 {
		t.Fatalf("expected 3 tool names, got %d (%#v)", got, toolNames)
	}
	if toolNames[0] != "Read" || toolNames[1] != "Write" || toolNames[2] != "Edit" {
		t.Fatalf("unexpected tool names %#v", toolNames)
	}
}
