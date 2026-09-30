package api

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"orchids-api/internal/opsagg"
	"testing"
	"time"
)

func TestMergedMetricsKeepUsageAndPricing(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer client.Close()
	agg := opsagg.New(client, "merge:")
	now := time.Now()
	for _, channel := range []string{"grok", "qoder"} {
		agg.Observe(context.Background(), opsagg.Outcome{Channel: channel, OK: true, At: now, UsageReported: true, Priced: true, InputTokens: 10, CachedTokens: 4, OutputTokens: 5, ReasoningTokens: 3, TotalTokens: 15, CostInUSDTicks: 100})
	}
	a := &API{opsAggregator: agg}
	buckets, dur, ttft, err := a.opsBucketsWithSamples(context.Background(), "all", now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	summary := agg.SummarizeWith(context.Background(), opsagg.SummaryInput{Channel: "all", Buckets: buckets, Durations: dur, FirstTokenMS: ttft, SamplesProvided: true, WindowMinutes: 1})
	if summary.Requests != 2 || summary.CachedInputTokens != 8 || summary.ReasoningTokens != 6 || summary.TotalTokens != 30 || summary.CostInUSDTicks != 200 || summary.PricedRequests != 2 || summary.PricedTokens != 30 {
		t.Fatalf("merged usage lost: %+v", summary)
	}
}
