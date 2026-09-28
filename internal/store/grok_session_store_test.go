package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestReasoningReplayItemsPersistenceAndExpiry(t *testing.T) {
	s, mini := newTestRedisStore(t, "grok-replay-items-test:")
	ctx := context.Background()
	items := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","summary":[],"encrypted_content":"cipher-a"}`),
		json.RawMessage(`{"type":"reasoning","summary":[{"type":"summary_text","text":"step"}],"encrypted_content":"cipher-b"}`),
	}
	original := &StoredReasoningReplay{Model: "grok-4.6", SessionKey: "session-items", Items: items}
	if err := s.SaveReasoningReplay(ctx, original, 2*time.Second); err != nil {
		t.Fatalf("SaveReasoningReplay(items) error = %v", err)
	}
	if !original.ExpiresAt.IsZero() {
		t.Fatal("SaveReasoningReplay mutated caller expiry")
	}
	replay, err := s.GetReasoningReplay(ctx, original.Model, original.SessionKey)
	if err != nil || replay == nil || !reflect.DeepEqual(replay.Items, items) || replay.EncryptedContent != "" {
		t.Fatalf("GetReasoningReplay(items) = %#v, %v", replay, err)
	}
	if replay.ExpiresAt.IsZero() || time.Until(replay.ExpiresAt) <= 0 {
		t.Fatalf("missing future replay expiry: %v", replay.ExpiresAt)
	}
	mini.FastForward(3 * time.Second)
	if replay, err := s.GetReasoningReplay(ctx, original.Model, original.SessionKey); !errors.Is(err, ErrNoRows) || replay != nil {
		t.Fatalf("GetReasoningReplay(expired items) = %#v, %v; want ErrNoRows", replay, err)
	}
}

func TestReasoningReplaySaveValidation(t *testing.T) {
	s, _ := newTestRedisStore(t, "grok-replay-validation-test:")
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		items []json.RawMessage
	}{
		{name: "missing items"},
		{name: "empty item", items: []json.RawMessage{nil}},
		{name: "whitespace", items: []json.RawMessage{json.RawMessage("  \n  ")}},
		{name: "malformed", items: []json.RawMessage{json.RawMessage(`{"type":`)}},
		{name: "null", items: []json.RawMessage{json.RawMessage(`null`)}},
		{name: "scalar", items: []json.RawMessage{json.RawMessage(`"cipher"`)}},
		{name: "number", items: []json.RawMessage{json.RawMessage(`42`)}},
		{name: "array", items: []json.RawMessage{json.RawMessage(`[{"type":"reasoning"}]`)}},
		{name: "mixed valid and invalid", items: []json.RawMessage{json.RawMessage(`{"type":"reasoning"}`), json.RawMessage(`false`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := "invalid-" + tc.name
			if err := s.SaveReasoningReplay(ctx, &StoredReasoningReplay{
				Model: "grok-4.6", SessionKey: key, Items: tc.items,
			}, time.Hour); err == nil {
				t.Fatal("SaveReasoningReplay() accepted invalid items")
			}
			if replay, err := s.GetReasoningReplay(ctx, "grok-4.6", key); !errors.Is(err, ErrNoRows) || replay != nil {
				t.Fatalf("GetReasoningReplay(invalid) = %#v, %v; want ErrNoRows", replay, err)
			}
		})
	}
	// An existing legacy cipher remains writable, but invalid modern items
	// must not be silently discarded even if a legacy fallback is present.
	if err := s.SaveReasoningReplay(ctx, &StoredReasoningReplay{
		Model: "grok-4.6", SessionKey: "invalid-with-legacy", EncryptedContent: "opaque",
		Items: []json.RawMessage{json.RawMessage(`null`)},
	}, time.Hour); err == nil {
		t.Fatal("SaveReasoningReplay() accepted invalid items with legacy cipher")
	}
}

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
