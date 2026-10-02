package auth

import (
	"context"
	"orchids-api/internal/testutil"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisSessionBackendForTest(t *testing.T) (*miniredis.Miniredis, SessionBackend) {
	t.Helper()
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	backend := NewRedisSessionBackend(client, "test")
	testutil.False(t, backend == nil, "NewRedisSessionBackend() returned nil for a live client")
	return mini, backend
}

func TestNewRedisSessionBackendRequiresClient(t *testing.T) {
	backend := NewRedisSessionBackend(nil, "test")
	testutil.False(t, backend != nil, "NewRedisSessionBackend(nil) must return nil so the in-process store stays in place")
}

func TestRedisSessionBackendRoundTrip(t *testing.T) {
	_, backend := newRedisSessionBackendForTest(t)
	ctx := context.Background()

	ok, err := backend.HasSession(ctx, "unknown-token")
	testutil.NoError(t, err, "HasSession(unknown) error = %v")
	testutil.False(t, ok, "an unknown token must not be valid")

	testutil.NoError(t, backend.SaveSession(ctx, "token-1", time.Now().Add(time.Hour)), "SaveSession() error = %v")
	ok, err = backend.HasSession(ctx, "token-1")
	testutil.Falsef(t, err != nil || !ok, "HasSession(saved) = %v, %v; want true, nil", ok, err)

	backend.DeleteSession(ctx, "token-1")
	ok, err = backend.HasSession(ctx, "token-1")
	testutil.Falsef(t, err != nil || ok, "HasSession(deleted) = %v, %v; want false, nil", ok, err)
}

func TestRedisSessionBackendExpiresWithTheSession(t *testing.T) {
	mini, backend := newRedisSessionBackendForTest(t)
	ctx := context.Background()

	testutil.NoError(t, backend.SaveSession(ctx, "token-1", time.Now().Add(90*time.Second)), "SaveSession() error = %v")
	mini.FastForward(3 * time.Minute)

	ok, err := backend.HasSession(ctx, "token-1")
	testutil.NoError(t, err, "HasSession() error = %v")
	testutil.False(t, ok, "an expired session must not validate")
}

// Persisting an already expired session must fail loudly: reporting success
// would let the caller keep a token in its process-local mirror that the durable
// store never accepted, leaving a session that is valid here and nowhere else.
func TestRedisSessionBackendRejectsAlreadyExpiredSessions(t *testing.T) {
	mini, backend := newRedisSessionBackendForTest(t)
	ctx := context.Background()

	err := backend.SaveSession(ctx, "token-1", time.Now().Add(-time.Minute))
	testutil.Error(t, err)
	testutil.Equal(t, len(mini.Keys()), 0)
}

// A Redis dump must never hand out a usable cookie, so the raw token stays out
// of both the key and the value.
func TestRedisSessionBackendStoresOnlyADigest(t *testing.T) {
	mini, backend := newRedisSessionBackendForTest(t)
	const token = "plain-token-that-must-not-be-stored"

	testutil.NoError(t, backend.SaveSession(context.Background(), token, time.Now().Add(time.Hour)), "SaveSession() error = %v")

	keys := mini.Keys()
	testutil.Equal(t, len(keys), 1)
	if !strings.HasPrefix(keys[0], "test:admin:sessions:") {
		t.Fatalf("key = %q, want the configured prefix", keys[0])
	}
	testutil.MustNotContain(t, keys[0], token)
	value, err := mini.Get(keys[0])
	if err != nil {
		t.Fatalf("Get(%q) error = %v", keys[0], err)
	}
	testutil.MustNotContain(t, value, token)
}
