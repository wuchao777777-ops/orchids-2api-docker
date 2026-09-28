package store

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newBatchReadStore returns a store over an in-process Redis. The account read
// path is one MGET, so it needs no live server and no credential cipher: the
// seeded rows below carry plaintext markers only.
func newBatchReadStore(t *testing.T) *redisStore {
	t.Helper()
	mini := miniredis.RunT(t)
	s := &redisStore{
		client: redis.NewClient(&redis.Options{Addr: mini.Addr()}),
		prefix: "test:",
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestGetAccountsByIDsReadsEveryRowInOneBatch pins the batch read's contract:
// every requested row is decoded, in the requested order.
func TestGetAccountsByIDsReadsEveryRowInOneBatch(t *testing.T) {
	s := newBatchReadStore(t)
	ctx := context.Background()

	for id, body := range map[int64]string{
		1: `{"id":1,"name":"test1","enabled":true}`,
		2: `{"id":2,"name":"test2","enabled":true}`,
		3: `{"id":3,"name":"test3","enabled":false}`,
	} {
		if err := s.client.Set(ctx, s.accountsKey(id), body, 0).Err(); err != nil {
			t.Fatalf("seed account %d: %v", id, err)
		}
	}

	accounts, err := s.getAccountsByIDs(ctx, []string{"1", "2", "3"}, false)
	if err != nil {
		t.Fatalf("getAccountsByIDs() error = %v", err)
	}
	if len(accounts) != 3 {
		t.Fatalf("got %d accounts, want 3", len(accounts))
	}
	for i, want := range []string{"test1", "test2", "test3"} {
		if accounts[i].Name != want {
			t.Fatalf("account %d = %q, want %q (row order lost)", i, accounts[i].Name, want)
		}
	}
}

// TestGetAccountsByIDsSkipsMissingAndDisabledRows covers the two rows the result
// must omit: an id with no stored row and a row the caller filtered out.
func TestGetAccountsByIDsSkipsMissingAndDisabledRows(t *testing.T) {
	s := newBatchReadStore(t)
	ctx := context.Background()

	if err := s.client.Set(ctx, s.accountsKey(1), `{"id":1,"name":"enabled","enabled":true}`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := s.client.Set(ctx, s.accountsKey(3), `{"id":3,"name":"disabled","enabled":false}`, 0).Err(); err != nil {
		t.Fatal(err)
	}

	// id 2 has no row at all.
	accounts, err := s.getAccountsByIDs(ctx, []string{"1", "2", "3"}, false)
	if err != nil {
		t.Fatalf("getAccountsByIDs() error = %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2 (the missing row must be skipped)", len(accounts))
	}

	enabled, err := s.getAccountsByIDs(ctx, []string{"1", "3"}, true)
	if err != nil {
		t.Fatalf("getAccountsByIDs(onlyEnabled) error = %v", err)
	}
	if len(enabled) != 1 || enabled[0].ID != 1 {
		t.Fatalf("onlyEnabled result = %#v, want just account 1", enabled)
	}
}

// TestGetAccountsByIDsEmptySelectionDoesNotReachRedis proves the empty and
// unparseable selections short-circuit instead of issuing a batch of zero keys.
func TestGetAccountsByIDsEmptySelectionDoesNotReachRedis(t *testing.T) {
	s := &redisStore{
		client: redis.NewClient(&redis.Options{Addr: "localhost:9999"}),
		prefix: "test:",
	}
	defer s.Close()
	ctx := context.Background()

	for _, ids := range [][]string{nil, {}, {"not-an-id"}} {
		accounts, err := s.getAccountsByIDs(ctx, ids, false)
		if err != nil {
			t.Fatalf("getAccountsByIDs(%v) error = %v, want no round trip", ids, err)
		}
		if accounts != nil {
			t.Fatalf("getAccountsByIDs(%v) = %#v, want nil", ids, accounts)
		}
	}
}

// TestGetAccountsByIDsReportsTransportFailure keeps the failure contract: an
// unreachable Redis is reported, never silently read as an empty pool.
func TestGetAccountsByIDsReportsTransportFailure(t *testing.T) {
	s := &redisStore{
		client: redis.NewClient(&redis.Options{Addr: "localhost:9999"}),
		prefix: "test:",
	}
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := s.getAccountsByIDs(ctx, []string{"1"}, false); err == nil {
		t.Fatal("expected an error from an unreachable Redis, got nil")
	}
}
