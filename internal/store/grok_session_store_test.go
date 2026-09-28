package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReasoningReplayAndSessionAffinityLifecycle(t *testing.T) {
	s, _ := newTestRedisStore(t, "grok-session-test:")
	ctx := context.Background()
	if err := s.SaveReasoningReplay(ctx, &StoredReasoningReplay{
		Model: "grok-4.6", SessionKey: "session-a", EncryptedContent: "opaque",
	}, time.Hour); err != nil {
		t.Fatalf("SaveReasoningReplay() error = %v", err)
	}
	replay, err := s.GetReasoningReplay(ctx, "grok-4.6", "session-a")
	if err != nil || replay.EncryptedContent != "opaque" {
		t.Fatalf("GetReasoningReplay() = %#v,%v", replay, err)
	}
	if _, err := s.GetReasoningReplay(ctx, "grok-4.5", "session-a"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("cross-model replay err=%v", err)
	}

	if err := s.SaveSessionAffinity(ctx, &StoredSessionAffinity{
		Provider: "build", Model: "grok-4.6", SessionKey: "session-a", AccountID: 42,
	}, time.Hour); err != nil {
		t.Fatalf("SaveSessionAffinity() error = %v", err)
	}
	affinity, err := s.GetSessionAffinity(ctx, "build", "grok-4.6", "session-a")
	if err != nil || affinity.AccountID != 42 {
		t.Fatalf("GetSessionAffinity() = %#v,%v", affinity, err)
	}
	if _, err := s.GetSessionAffinity(ctx, "console", "grok-4.6", "session-a"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("cross-provider affinity err=%v", err)
	}
}
