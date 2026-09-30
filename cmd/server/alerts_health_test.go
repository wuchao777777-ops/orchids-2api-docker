package main

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"orchids-api/internal/opsagg"
	"orchids-api/internal/store"
	"strconv"
	"testing"
	"time"
)

func TestAlertSnapshotRejectsPartialEvidence(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{RedisAddr: mini.Addr(), RedisPrefix: "alert-health:"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	agg := opsagg.New(s.RedisClient(), s.RedisPrefix())
	now := time.Now()
	agg.Observe(context.Background(), opsagg.Outcome{Channel: "grok", OK: true, At: now})
	mini.Set(s.RedisPrefix()+"accounts:ids", "invalid")
	if _, err := buildAlertSnapshot(context.Background(), agg, s); err == nil {
		t.Fatal("broken account evidence accepted")
	}
	mini.Del(s.RedisPrefix() + "accounts:ids")
	mini.Del(aggKeyForAlertTest(s.RedisPrefix(), now) + ":dur")
	mini.Set(aggKeyForAlertTest(s.RedisPrefix(), now)+":dur", "invalid")
	if _, err := buildAlertSnapshot(context.Background(), agg, s); err == nil {
		t.Fatal("broken latency evidence accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buildAlertSnapshot(ctx, agg, s); err == nil {
		t.Fatal("cancelled evidence accepted")
	}
}

func aggKeyForAlertTest(prefix string, at time.Time) string {
	return prefix + "ops:agg:" + strconv.FormatInt(at.Unix()/60, 10) + ":grok"
}
