package store

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestReasoningReplayAndSessionAffinityLifecycle(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := New(Options{RedisAddr: mini.Addr(), RedisPrefix: "grok-session-test:"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = s.Close()
		mini.Close()
	}()
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

func TestPuterReasoningReplayIsEncryptedAndModelScoped(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := New(Options{
		RedisAddr:               mini.Addr(),
		RedisPrefix:             "puter-replay-test:",
		CredentialEncryptionKey: bytes.Repeat([]byte{7}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	const reasoning = "private reasoning that must not be stored as plaintext"
	if err := s.SavePuterReasoningReplay(ctx, &StoredPuterReasoningReplay{
		Model: "deepseek-v4-flash", ToolCallID: "call-1", ReasoningContent: reasoning,
	}, time.Hour); err != nil {
		t.Fatalf("SavePuterReasoningReplay() error = %v", err)
	}

	var stored string
	for _, key := range mini.Keys() {
		if strings.Contains(key, "puter:reasoning_replay:") {
			stored, err = mini.Get(key)
			if err != nil {
				t.Fatalf("read stored replay: %v", err)
			}
			break
		}
	}
	if stored == "" || strings.Contains(stored, reasoning) || !strings.Contains(stored, encryptedCredentialPrefix) {
		t.Fatalf("reasoning replay was not encrypted at rest: %q", stored)
	}

	replay, err := s.GetPuterReasoningReplay(ctx, "deepseek-v4-flash", "call-1")
	if err != nil || replay.ReasoningContent != reasoning {
		t.Fatalf("GetPuterReasoningReplay() = %#v, %v", replay, err)
	}
	if _, err := s.GetPuterReasoningReplay(ctx, "deepseek-v4-pro", "call-1"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("cross-model replay err=%v", err)
	}
}
