package main

import (
	"context"
	"orchids-api/internal/opsagg"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestAlertSnapshotRejectsPartialEvidence(t *testing.T) {
	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{RedisAddr: mini.Addr(), RedisPrefix: "alert-health:"})
	testutil.NoError(t, err)
	defer s.Close()
	agg := opsagg.New(s.RedisClient(), s.RedisPrefix())
	now := time.Now()
	agg.Observe(context.Background(), opsagg.Outcome{Channel: "grok", OK: true, At: now})
	mini.Set(s.RedisPrefix()+"accounts:ids", "invalid")
	_, err = buildAlertSnapshot(context.Background(), agg, s)
	testutil.Error(t, err)
	mini.Del(s.RedisPrefix() + "accounts:ids")
	mini.Del(aggKeyForAlertTest(s.RedisPrefix(), now) + ":dur")
	mini.Set(aggKeyForAlertTest(s.RedisPrefix(), now)+":dur", "invalid")
	_, err = buildAlertSnapshot(context.Background(), agg, s)
	testutil.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = buildAlertSnapshot(ctx, agg, s)
	testutil.Error(t, err)
}

func aggKeyForAlertTest(prefix string, at time.Time) string {
	return prefix + "ops:agg:" + strconv.FormatInt(at.Unix()/60, 10) + ":grok"
}
