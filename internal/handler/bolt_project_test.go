package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"orchids-api/internal/debug"
	"orchids-api/internal/store"
	"orchids-api/internal/upstream"
)

type fakeBoltProjectClient struct {
	projectIDs []string
	err        error
	calls      int
}

func (f *fakeBoltProjectClient) SendRequest(context.Context, string, []interface{}, string, func(upstream.SSEMessage), *debug.Logger) error {
	return nil
}

func (f *fakeBoltProjectClient) CreateEmptyProject(context.Context) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	if len(f.projectIDs) == 0 {
		return "", nil
	}
	idx := f.calls - 1
	if idx >= len(f.projectIDs) {
		idx = len(f.projectIDs) - 1
	}
	return f.projectIDs[idx], nil
}

func TestResolveBoltProjectID_CachesPerWorkdir(t *testing.T) {
	h := &Handler{sessionStore: NewMemorySessionStore(30*time.Minute, 100)}
	acc := &store.Account{ID: 6, ProjectID: "sb1-old"}
	client := &fakeBoltProjectClient{projectIDs: []string{"sb1-new"}}

	got, err := h.resolveBoltProjectID(context.Background(), acc, client, `C:\Users\Test\Repo`, false)
	if err != nil {
		t.Fatalf("resolveBoltProjectID() error = %v", err)
	}
	if got != "sb1-new" {
		t.Fatalf("projectID=%q want sb1-new", got)
	}
	if client.calls != 1 {
		t.Fatalf("calls=%d want 1", client.calls)
	}

	got, err = h.resolveBoltProjectID(context.Background(), acc, client, `c:/users/test/repo`, false)
	if err != nil {
		t.Fatalf("resolveBoltProjectID() second error = %v", err)
	}
	if got != "sb1-new" {
		t.Fatalf("projectID=%q want cached sb1-new", got)
	}
	if client.calls != 1 {
		t.Fatalf("calls=%d want cached result", client.calls)
	}
}

func TestResolveBoltProjectID_CreatesNewProjectForDifferentWorkdir(t *testing.T) {
	h := &Handler{sessionStore: NewMemorySessionStore(30*time.Minute, 100)}
	acc := &store.Account{ID: 6, ProjectID: "sb1-old"}
	client := &fakeBoltProjectClient{projectIDs: []string{"sb1-first", "sb1-second"}}

	first, err := h.resolveBoltProjectID(context.Background(), acc, client, `/tmp/repo-a`, false)
	if err != nil {
		t.Fatalf("resolveBoltProjectID() first error = %v", err)
	}
	second, err := h.resolveBoltProjectID(context.Background(), acc, client, `/tmp/repo-b`, false)
	if err != nil {
		t.Fatalf("resolveBoltProjectID() second error = %v", err)
	}

	if first != "sb1-first" || second != "sb1-second" {
		t.Fatalf("projectIDs=(%q,%q) want (sb1-first,sb1-second)", first, second)
	}
	if client.calls != 2 {
		t.Fatalf("calls=%d want 2", client.calls)
	}
}

func TestResolveBoltProjectID_ReturnsCreationError(t *testing.T) {
	h := &Handler{sessionStore: NewMemorySessionStore(30*time.Minute, 100)}
	acc := &store.Account{ID: 6, ProjectID: "sb1-old"}
	client := &fakeBoltProjectClient{err: errors.New("boom")}

	_, err := h.resolveBoltProjectID(context.Background(), acc, client, `/tmp/repo-a`, false)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("error=%v want boom", err)
	}
}

func TestResolveBoltProjectID_ForceNewReplacesCachedProjectForSameWorkdir(t *testing.T) {
	h := &Handler{sessionStore: NewMemorySessionStore(30*time.Minute, 100)}
	acc := &store.Account{ID: 6, ProjectID: "sb1-old"}
	client := &fakeBoltProjectClient{projectIDs: []string{"sb1-first", "sb1-second"}}

	first, err := h.resolveBoltProjectID(context.Background(), acc, client, `/tmp/repo-a`, false)
	if err != nil {
		t.Fatalf("resolveBoltProjectID() first error = %v", err)
	}
	second, err := h.resolveBoltProjectID(context.Background(), acc, client, `/tmp/repo-a`, true)
	if err != nil {
		t.Fatalf("resolveBoltProjectID() force-new error = %v", err)
	}
	third, err := h.resolveBoltProjectID(context.Background(), acc, client, `/tmp/repo-a`, false)
	if err != nil {
		t.Fatalf("resolveBoltProjectID() cached-after-force-new error = %v", err)
	}

	if first != "sb1-first" || second != "sb1-second" || third != "sb1-second" {
		t.Fatalf("projectIDs=(%q,%q,%q) want (sb1-first,sb1-second,sb1-second)", first, second, third)
	}
	if client.calls != 2 {
		t.Fatalf("calls=%d want 2", client.calls)
	}
}
