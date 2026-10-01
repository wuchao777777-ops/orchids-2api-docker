package opsagg

import (
	"context"
	"orchids-api/internal/testutil"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestUsageDimensionsAggregateAndTotalDrivesTPS(t *testing.T) {
	agg, _ := newAggregator(t)
	ctx := context.Background()
	at := time.Now().Truncate(time.Minute)
	agg.Observe(ctx, Outcome{Channel: "grok", At: at, OK: true, UsageReported: true, InputTokens: 10, CachedTokens: 4, OutputTokens: 5, ReasoningTokens: 3, TotalTokens: 18})
	buckets, err := agg.Range(ctx, "grok", at, at)
	if err != nil || len(buckets) != 1 {
		t.Fatalf("Range: buckets=%v err=%v", buckets, err)
	}
	summary := agg.SummarizeWith(ctx, SummaryInput{Channel: "grok", Buckets: buckets, WindowMinutes: 1, SamplesProvided: true})
	if summary.CachedInputTokens != 4 || summary.ReasoningTokens != 3 || summary.TotalTokens != 18 || summary.TPS != 0.3 || summary.UnpricedRequests != 1 || summary.UnpricedTokens != 18 {
		t.Fatalf("summary=%+v", summary)
	}
}

func newRedisClient(t *testing.T, addr string) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func newAggregator(t *testing.T) (*Aggregator, *miniredis.Miniredis) {
	t.Helper()
	mini := miniredis.RunT(t)
	client := newRedisClient(t, mini.Addr())
	return New(client, "ops-test:"), mini
}

// TestObserve_RollsUpIntoOneMinuteBucket covers the basic counters the overview
// renders.
func TestObserve_RollsUpIntoOneMinuteBucket(t *testing.T) {
	agg, _ := newAggregator(t)
	ctx := context.Background()
	at := time.Now().Truncate(time.Minute)

	agg.Observe(ctx, Outcome{Channel: "grok", Model: "grok-4.6", OK: true, DurationMS: 1200, FirstTokenMS: 300, At: at})
	agg.Observe(ctx, Outcome{Channel: "grok", Model: "grok-4.6", OK: true, DurationMS: 800, FirstTokenMS: 200, At: at})
	agg.Observe(ctx, Outcome{Channel: "grok", Model: "grok-4.6", OK: false, DurationMS: 5000, At: at})

	buckets, err := agg.Range(ctx, "grok", at, at)
	if err != nil || len(buckets) != 1 {
		t.Fatalf("buckets = %v err = %v", buckets, err)
	}
	bucket := buckets[0]
	if bucket.Requests != 3 || bucket.Success != 2 || bucket.Failed != 1 {
		t.Fatalf("bucket = %+v", bucket)
	}

	durations, ttfts, err := agg.SamplesForChecked(ctx, "grok", buckets)
	if err != nil {
		t.Fatalf("SamplesForChecked() error = %v", err)
	}
	summary := agg.SummarizeWith(ctx, SummaryInput{Channel: "grok", Buckets: buckets, Durations: durations, FirstTokenMS: ttfts, SamplesProvided: true})
	testutil.Equal(t, summary.Requests, 3)
	if got, want := summary.SuccessRate, 2.0/3.0; got < want-0.001 || got > want+0.001 {
		t.Fatalf("success rate = %v, want %v", got, want)
	}
	if summary.DurationP95MS == 0 || summary.FirstTokenP95MS == 0 {
		t.Fatalf("percentiles missing: %+v", summary)
	}
	testutil.Equal(t, summary.Samples, 3)
}

// TestSummarize_NoSamplesIsZero is what makes the UI able to say "暂无样本"
// instead of drawing a green, zero-traffic channel.
func TestSummarize_NoSamplesIsZero(t *testing.T) {
	agg, _ := newAggregator(t)
	buckets, err := agg.Range(context.Background(), "workbuddy", time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("Range() error = %v", err)
	}
	testutil.Equal(t, len(buckets), 0)
	ctx := context.Background()
	durations, ttfts, err := agg.SamplesForChecked(ctx, "workbuddy", buckets)
	if err != nil {
		t.Fatalf("SamplesForChecked() error = %v", err)
	}
	summary := agg.SummarizeWith(ctx, SummaryInput{Channel: "workbuddy", Buckets: buckets, Durations: durations, FirstTokenMS: ttfts, SamplesProvided: true})
	if summary.Requests != 0 || summary.Samples != 0 || summary.SuccessRate != 0 || summary.RPM != 0 {
		t.Fatalf("idle summary = %+v, want all zero", summary)
	}
}

// Range uses bounded Redis pipeline batches and includes both endpoint minutes,
// even when their seconds differ. An invalid hash in a later batch must not leak
// partial results from earlier successful batches.
func TestRange_BatchBoundariesAndErrors(t *testing.T) {
	agg, mini := newAggregator(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-600 * time.Minute)
	for _, offset := range []int{0, 255, 256, 299} {
		agg.Observe(ctx, Outcome{Channel: "grok", OK: true, At: base.Add(time.Duration(offset) * time.Minute)})
	}
	from := base.Add(50 * time.Second)
	until := base.Add(299*time.Minute + 10*time.Second)
	buckets, err := agg.Range(ctx, "grok", until, from) // reversed on purpose
	if err != nil || len(buckets) != 4 {
		t.Fatalf("reversed range: buckets=%v err=%v", buckets, err)
	}
	for i, offset := range []int{0, 255, 256, 299} {
		want := base.Add(time.Duration(offset) * time.Minute)
		if !buckets[i].Minute.Equal(want) || buckets[i].Requests != 1 {
			t.Fatalf("bucket[%d]=%+v want minute %s", i, buckets[i], want)
		}
	}
	mini.Del(agg.key(base.Add(256*time.Minute), "grok"))
	testutil.NoError(t, mini.Set(agg.key(base.Add(256*time.Minute), "grok"), "not-a-hash"))
	buckets, err = agg.Range(ctx, "grok", from, until)
	if err == nil || buckets != nil {
		t.Fatalf("wrongtype in second batch: buckets=%v err=%v, want nil and error", buckets, err)
	}
}

func TestRange_TruncatesToLatestMinutes(t *testing.T) {
	agg, _ := newAggregator(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-2 * 24 * time.Hour)
	last := base.Add(time.Duration(MaxTrendMinutes+5) * time.Minute)
	for _, at := range []time.Time{base.Add(5 * time.Minute), base.Add(6 * time.Minute), last} {
		agg.Observe(ctx, Outcome{Channel: "grok", OK: true, At: at})
	}
	buckets, err := agg.Range(ctx, "grok", base, last)
	if err != nil || len(buckets) != 2 {
		t.Fatalf("clamped range: buckets=%v err=%v, want two", buckets, err)
	}
	if !buckets[0].Minute.Equal(base.Add(6*time.Minute)) || !buckets[1].Minute.Equal(last) {
		t.Fatalf("clamped endpoints=%v, %v", buckets[0].Minute, buckets[1].Minute)
	}
}

// TestRange_SpansMinutesInOrder keeps the trend line in chronological order.
func TestRange_SpansMinutesInOrder(t *testing.T) {
	agg, _ := newAggregator(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Minute).Add(-3 * time.Minute)

	for i := 0; i < 3; i++ {
		agg.Observe(ctx, Outcome{Channel: "workbuddy", OK: true, DurationMS: 100, At: base.Add(time.Duration(i) * time.Minute)})
	}
	buckets, err := agg.Range(ctx, "workbuddy", base, base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Range() error = %v", err)
	}
	testutil.Equal(t, len(buckets), 3)
	for i := 1; i < len(buckets); i++ {
		if !buckets[i].Minute.After(buckets[i-1].Minute) {
			t.Fatalf("buckets are not in chronological order: %+v", buckets)
		}
	}
	durations, ttfts, err := agg.SamplesForChecked(ctx, "workbuddy", buckets)
	if err != nil {
		t.Fatalf("SamplesForChecked() error = %v", err)
	}
	if summary := agg.SummarizeWith(ctx, SummaryInput{Channel: "workbuddy", Buckets: buckets, Durations: durations, FirstTokenMS: ttfts, SamplesProvided: true}); summary.RPM <= 0 {
		t.Fatalf("rpm = %v, want > 0 over three minutes", summary.RPM)
	}
}

// TestModelStatsFromBuckets reports per-model quality without extra keys.
func TestModelStatsFromBuckets(t *testing.T) {
	agg, _ := newAggregator(t)
	ctx := context.Background()
	at := time.Now().Truncate(time.Minute)

	for i := 0; i < 3; i++ {
		agg.Observe(ctx, Outcome{Channel: "grok", Model: "grok-4.6", OK: i > 0, DurationMS: int64(100 * (i + 1)), At: at})
	}
	agg.Observe(ctx, Outcome{Channel: "grok", Model: "grok-4.5", OK: true, DurationMS: 50, At: at})

	buckets, err := agg.Range(ctx, "grok", at, at)
	if err != nil {
		t.Fatalf("Range() error = %v", err)
	}
	stats, err := agg.ModelStatsFromBucketsChecked(ctx, "grok", buckets)
	if err != nil {
		t.Fatalf("ModelStatsFromBucketsChecked() error = %v", err)
	}
	testutil.Equal(t, len(stats), 2)
	// Sorted by request count: grok-4.6 leads.
	if stats[0].Model != "grok-4.6" || stats[0].Requests != 3 || stats[0].Success != 2 || stats[0].Failed != 1 {
		t.Fatalf("leading model = %+v", stats[0])
	}
}

// Mixed minutes must read only models present in each minute, while keeping
// model counters and latency samples aggregated across all minutes.
func TestModelStatsFromBuckets_MixedMinutesAndMissingDuration(t *testing.T) {
	agg, mini := newAggregator(t)
	ctx := context.Background()
	at := time.Now().Truncate(time.Minute)
	observe := func(minute int, model string, ok bool, duration, ttft int64) {
		agg.Observe(ctx, Outcome{Channel: "grok", Model: model, OK: ok,
			DurationMS: duration, FirstTokenMS: ttft, At: at.Add(time.Duration(minute) * time.Minute)})
	}
	observe(0, "alpha", true, 100, 10)
	observe(1, "beta", true, 200, 20)
	observe(1, "alpha", false, 300, 30)
	observe(2, "gamma", true, 0, 40)
	observe(2, "alpha", true, 0, 0)

	// A request with no duration must not create a zero-valued model sample.
	if mini.Exists(agg.key(at.Add(2*time.Minute), "grok") + ":model:gamma") {
		t.Fatal("gamma has a duration list despite no duration sample")
	}
	buckets, err := agg.Range(ctx, "grok", at, at.Add(2*time.Minute))
	if err != nil || len(buckets) != 3 {
		t.Fatalf("Range: buckets=%v err=%v", buckets, err)
	}
	before := mini.CommandCount()
	stats, err := agg.ModelStatsFromBucketsChecked(ctx, "grok", buckets)
	if err != nil {
		t.Fatalf("ModelStatsFromBucketsChecked() error = %v", err)
	}
	// Three HGETALLs plus two LRANGEs per present (minute, model) pair:
	// alpha; alpha+beta; alpha+gamma = five pairs, not 3*3 models.
	if got, want := mini.CommandCount()-before, 3+2*5; got != want {
		t.Fatalf("model stats Redis reads=%d, want %d (3 hashes + 10 lists)", got, want)
	}
	testutil.Equal(t, len(stats), 3)
	byName := make(map[string]ModelStats, len(stats))
	for _, stat := range stats {
		byName[stat.Model] = stat
	}
	if got := byName["alpha"]; got.Requests != 3 || got.Success != 2 || got.Failed != 1 ||
		got.Samples != 2 || got.DurationP95MS != 300 || got.FirstTokenSamples != 2 || got.FirstTokenP95MS != 30 ||
		got.SuccessRate != 2.0/3.0 {
		t.Fatalf("alpha=%+v", got)
	}
	if got := byName["beta"]; got.Requests != 1 || got.Success != 1 || got.Samples != 1 ||
		got.DurationP95MS != 200 || got.FirstTokenSamples != 1 || got.FirstTokenP95MS != 20 {
		t.Fatalf("beta=%+v", got)
	}
	if got := byName["gamma"]; got.Requests != 1 || got.Success != 1 || got.Samples != 0 ||
		got.DurationP95MS != 0 || got.FirstTokenSamples != 1 || got.FirstTokenP95MS != 40 {
		t.Fatalf("gamma=%+v", got)
	}
	// Channel-level summary semantics (as consumed by API stats) are unchanged.
	durations, ttfts, err := agg.SamplesForChecked(ctx, "grok", buckets)
	if err != nil {
		t.Fatalf("SamplesForChecked() error = %v", err)
	}
	summary := agg.SummarizeWith(ctx, SummaryInput{Channel: "grok", Buckets: buckets, Durations: durations, FirstTokenMS: ttfts, SamplesProvided: true})
	if summary.Requests != 5 || summary.Success != 4 || summary.Failed != 1 ||
		summary.Samples != 3 || summary.DurationP95MS != 300 || summary.FirstTokenP95MS != 40 {
		t.Fatalf("channel summary=%+v", summary)
	}
}

// TestChannels_ListsOnlyObservedChannels lets the UI offer real options.
func TestChannels_ListsOnlyObservedChannels(t *testing.T) {
	agg, _ := newAggregator(t)
	ctx := context.Background()
	at := time.Now()
	agg.Observe(ctx, Outcome{Channel: "grok", OK: true, At: at})
	agg.Observe(ctx, Outcome{Channel: "workbuddy", OK: true, At: at})

	channels, err := agg.Channels(ctx, at, at)
	if err != nil {
		t.Fatalf("Channels() error = %v", err)
	}
	if len(channels) != 2 || channels[0] != "grok" || channels[1] != "workbuddy" {
		t.Fatalf("channels = %v", channels)
	}
}

func TestChannels_ReversedRangeAndExpiredEmpty(t *testing.T) {
	agg, _ := newAggregator(t)
	ctx := context.Background()
	old := time.Now().Truncate(time.Minute).Add(-2 * time.Minute)
	agg.Observe(ctx, Outcome{Channel: "old", OK: true, At: old})
	newer := old.Add(time.Minute)
	agg.Observe(ctx, Outcome{Channel: "new", OK: true, At: newer})
	channels, err := agg.Channels(ctx, newer, old)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 2 || channels[0] != "new" || channels[1] != "old" {
		t.Fatalf("channels=%v", channels)
	}
	channels, err = agg.Channels(ctx, old.Add(2*time.Minute), old.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	testutil.Equal(t, len(channels), 0)
}

func TestPercentile_NearestRank(t *testing.T) {
	values := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	testutil.Equal(t, percentile(values, 0.95), 100)
	testutil.Equal(t, percentile(values, 0.5), 50)
	testutil.Equal(t, percentile(nil, 0.95), 0)
}

// TestChannels_IgnoresSideLists pins the rule that a percentile list must never
// appear as a channel: the old code read the last colon-separated segment, so
// "...:dur" showed up as a channel named "dur".
func TestChannels_IgnoresSideLists(t *testing.T) {
	agg, _ := newAggregator(t)
	ctx := context.Background()
	at := time.Now()
	agg.Observe(ctx, Outcome{Channel: "grok", Model: "grok-4.6", OK: true, DurationMS: 120, FirstTokenMS: 40, At: at})

	channels, err := agg.Channels(ctx, at, at)
	if err != nil {
		t.Fatalf("Channels() error = %v", err)
	}
	for _, name := range channels {
		if name == "dur" || name == "ttft" || name == "grok-4.6" {
			t.Fatalf("side list leaked into the channel list: %v", channels)
		}
	}
	if len(channels) != 1 || channels[0] != "grok" {
		t.Fatalf("channels = %v, want just grok", channels)
	}
	// The percentile samples must survive for the summary to compute p95.
	buckets, err := agg.Range(ctx, "grok", at, at)
	if err != nil || len(buckets) != 1 {
		t.Fatalf("buckets = %v err = %v", buckets, err)
	}
	durations, ttfts, err := agg.SamplesForChecked(ctx, "grok", buckets)
	if err != nil {
		t.Fatalf("SamplesForChecked() error = %v", err)
	}
	summary := agg.SummarizeWith(ctx, SummaryInput{Channel: "grok", Buckets: buckets, Durations: durations, FirstTokenMS: ttfts, SamplesProvided: true})
	if summary.Samples == 0 || summary.DurationP95MS != 120 {
		t.Fatalf("summary = %+v, want a 120ms p95 sample", summary)
	}
}

// TestSummarizeWith_RateUsesTheWindowNotTheBuckets is the reported RPM bug: a
// single request inside a sixty-minute window reported an RPM of 1, because the
// rate was divided by the number of buckets that happened to exist.
func TestSummarizeWith_RateUsesTheWindowNotTheBuckets(t *testing.T) {
	agg, _ := newAggregator(t)
	ctx := context.Background()
	at := time.Now().Truncate(time.Minute)
	agg.Observe(ctx, Outcome{Channel: "grok", Model: "grok-4.6", OK: true, DurationMS: 100, At: at})

	buckets, err := agg.Range(ctx, "grok", at.Add(-59*time.Minute), at)
	if err != nil {
		t.Fatalf("Range() error = %v", err)
	}
	durations, ttfts, err := agg.SamplesForChecked(ctx, "grok", buckets)
	if err != nil {
		t.Fatalf("SamplesForChecked() error = %v", err)
	}

	summary := agg.SummarizeWith(ctx, SummaryInput{
		Channel:         "grok",
		Buckets:         buckets,
		WindowMinutes:   60,
		Durations:       durations,
		FirstTokenMS:    ttfts,
		SamplesProvided: true,
	})
	testutil.Equal(t, summary.Requests, 1)
	if summary.RPM > 0.02 || summary.RPM < 0.01 {
		t.Fatalf("rpm = %v, want about 0.0167 for one request in an hour", summary.RPM)
	}
}

// TestSummarizeWith_MergedSamplesProducePercentiles is the reported P95 bug: the
// merged scope had no samples of its own, so both percentiles were flat zero while
// the trend showed traffic.
func TestSummarizeWith_MergedSamplesProducePercentiles(t *testing.T) {
	agg, _ := newAggregator(t)
	ctx := context.Background()
	at := time.Now().Truncate(time.Minute)
	for _, duration := range []int64{100, 200, 300} {
		agg.Observe(ctx, Outcome{Channel: "grok", OK: true, DurationMS: duration, FirstTokenMS: duration / 2, At: at})
	}
	for _, duration := range []int64{400, 500} {
		agg.Observe(ctx, Outcome{Channel: "workbuddy", OK: true, DurationMS: duration, FirstTokenMS: duration / 2, At: at})
	}

	grokBuckets, err := agg.Range(ctx, "grok", at, at)
	if err != nil {
		t.Fatalf("Range(grok) error = %v", err)
	}
	workbuddyBuckets, err := agg.Range(ctx, "workbuddy", at, at)
	if err != nil {
		t.Fatalf("Range(workbuddy) error = %v", err)
	}
	merged := append(append([]Bucket(nil), grokBuckets...), workbuddyBuckets...)
	var durations, ttfts []int64
	for _, pair := range []struct {
		channel string
		buckets []Bucket
	}{{"grok", grokBuckets}, {"workbuddy", workbuddyBuckets}} {
		d, tt, err := agg.SamplesForChecked(ctx, pair.channel, pair.buckets)
		if err != nil {
			t.Fatalf("SamplesForChecked(%q) error = %v", pair.channel, err)
		}
		durations = append(durations, d...)
		ttfts = append(ttfts, tt...)
	}

	summary := agg.SummarizeWith(ctx, SummaryInput{
		Channel: "all", Buckets: merged, WindowMinutes: 60,
		Durations: durations, FirstTokenMS: ttfts, SamplesProvided: true,
	})
	testutil.Equal(t, summary.Samples, 5)
	if summary.DurationP95MS == 0 || summary.FirstTokenP95MS == 0 {
		t.Fatalf("merged percentiles are zero: %+v", summary)
	}
	// The merged p95 must reflect the slowest channel, not only the first one.
	if summary.DurationP95MS <= 300 {
		t.Fatalf("duration p95 = %d, want it to include the slower channel", summary.DurationP95MS)
	}
}

func TestDetailedOutcomePreservesUsageCohortsAndTruePercentiles(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	agg := New(client, "details:")
	ctx := context.Background()
	at := time.Now().Truncate(time.Minute)
	for i := 1; i <= 100; i++ {
		agg.Observe(ctx, Outcome{Channel: "grok", Model: "test", At: at, OK: i != 100, Status: "2xx", HTTPStatus: 200, Detailed: true, UsageReported: true, InputTokens: 10, OutputTokens: 20, DurationMS: int64(i * 100), FirstTokenMS: int64(i * 10), AttemptFailures: 1, AccountSwitches: 1})
	}
	buckets, err := agg.Range(ctx, "grok", at, at)
	if err != nil {
		t.Fatal(err)
	}
	durations, ttft, err := agg.SamplesForChecked(ctx, "grok", buckets)
	if err != nil {
		t.Fatalf("SamplesForChecked() error = %v", err)
	}
	summary := agg.SummarizeWith(ctx, SummaryInput{Channel: "grok", Buckets: buckets, WindowMinutes: 5, Durations: durations, FirstTokenMS: ttft, SamplesProvided: true})
	if summary.Duration.P95 != 9500 || summary.Duration.P99 != 9900 {
		t.Fatalf("percentiles=%+v", summary.Duration)
	}
	if summary.InputTokens != 1000 || summary.OutputTokens != 2000 || summary.TPS != 10 || summary.UsageSamples != 100 {
		t.Fatalf("usage=%+v", summary)
	}
	if summary.DurationFailed.Samples != 1 || summary.DurationAttempt.Samples != 100 || summary.AttemptFailures != 100 {
		t.Fatalf("cohorts=%+v", summary)
	}
	channels, err := agg.Channels(ctx, at, at)
	if err != nil || len(channels) != 1 || channels[0] != "grok" {
		t.Fatalf("side lists treated as channels: %v %v", channels, err)
	}
	models, err := agg.ModelStatsFromBucketsChecked(ctx, "grok", buckets)
	if err != nil {
		t.Fatalf("ModelStatsFromBucketsChecked() error = %v", err)
	}
	if len(models) != 1 || models[0].FirstTokenSamples != 100 || models[0].FirstTokenP95MS != 950 {
		t.Fatalf("model ttft=%+v", models)
	}
}

// A priced row contributes its cost to the bucket, the summary and the API view;
// an unpriced row contributes usage but no cost, so a dashboard can show both
// figures side by side instead of presenting one as the whole truth.
func TestObserveAggregatesCostByPricedAndUnpriced(t *testing.T) {
	aggregator, _ := newAggregator(t)
	ctx := context.Background()
	now := time.Now().UTC()

	aggregator.Observe(ctx, Outcome{
		Channel: "grok", Model: "grok-4.6", Status: "2xx", OK: true,
		InputTokens: 100, OutputTokens: 50, TotalTokens: 150,
		UsageReported: true, Priced: true, CostInUSDTicks: 1_500_000, At: now,
	})
	aggregator.Observe(ctx, Outcome{
		Channel: "grok", Model: "future-model", Status: "2xx", OK: true,
		InputTokens: 100, OutputTokens: 50, TotalTokens: 150,
		UsageReported: true, Priced: false, At: now,
	})

	buckets, err := aggregator.Range(ctx, "grok", now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	testutil.Equal(t, len(buckets), 1)
	testutil.Equal(t, buckets[0].CostInUSDTicks, 1_500_000)
	if buckets[0].PricedRequests != 1 || buckets[0].UnpricedRequests != 1 {
		t.Fatalf("priced=%d unpriced=%d", buckets[0].PricedRequests, buckets[0].UnpricedRequests)
	}
	durations, ttfts, err := aggregator.SamplesForChecked(ctx, "grok", buckets)
	if err != nil {
		t.Fatalf("SamplesForChecked: %v", err)
	}
	summary := aggregator.SummarizeWith(ctx, SummaryInput{Channel: "grok", Buckets: buckets, WindowMinutes: 1, Durations: durations, FirstTokenMS: ttfts, SamplesProvided: true})
	testutil.Equal(t, summary.CostInUSDTicks, 1_500_000)
	if summary.PricedRequests != 1 || summary.UnpricedRequests != 1 {
		t.Fatalf("summary priced=%d unpriced=%d", summary.PricedRequests, summary.UnpricedRequests)
	}
}

func TestCheckedSamplesAndWriteHealthDoNotHideRedisFailure(t *testing.T) {
	agg, mini := newAggregator(t)
	now := time.Now().Truncate(time.Minute)
	ctx := context.Background()
	agg.Observe(ctx, Outcome{Channel: "grok", OK: true, DurationMS: 100, At: now})
	buckets, err := agg.Range(ctx, "grok", now, now)
	if err != nil {
		t.Fatal(err)
	}
	mini.Del(agg.key(now, "grok") + ":dur")
	mini.Set(agg.key(now, "grok")+":dur", "invalid")
	if _, _, err := agg.SamplesForChecked(ctx, "grok", buckets); err == nil {
		t.Fatal("unavailable latency was treated as empty samples")
	}
	agg.Observe(ctx, Outcome{Channel: "grok", OK: true, DurationMS: 100, At: now})
	health := agg.Health()
	if health["written"] != uint64(1) || health["write_failed"] != uint64(1) {
		t.Fatalf("health=%+v", health)
	}
}
