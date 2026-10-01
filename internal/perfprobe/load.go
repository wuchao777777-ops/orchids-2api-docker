package perfprobe

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type Result struct {
	TargetRPS                                                     int
	Offered, Accepted, Completed, Errors, Dropped                 int64
	OfferSeconds, TotalSeconds, CompletedRPS, P50ms, P95ms, P99ms float64
	GCPercent, MemoryLimit                                        string
	GCCycles                                                      uint32
	Warmup                                                        bool
	GCPauseMs, HeapInuseMiB, HeapSysMiB, AllocBytesPerAccepted    float64
}

// Load sends bounded open-loop traffic; missed scheduled arrivals count as drops.
func Load(call func() error) bool {
	if os.Getenv("PROVIDER_LOCAL_LOAD") != "1" {
		return false
	}
	const rate = 100000
	warmup := os.Getenv("PROVIDER_LOCAL_WARMUP") == "1"
	if warmup {
		for i := 0; i < 1000; i++ {
			if err := call(); err != nil {
				panic(fmt.Sprintf("provider warmup failed: %v", err))
			}
		}
	}
	seconds := 5
	if value, err := strconv.Atoi(os.Getenv("PROVIDER_LOCAL_LOAD_SECONDS")); err == nil && value >= 1 && value <= 60 {
		seconds = value
	}
	const workers = 256
	jobs := make(chan time.Time, workers)
	var wg sync.WaitGroup
	var done, errs atomic.Int64
	samples := make([][]int64, workers)
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for scheduled := range jobs {
				err := call()
				if err != nil {
					errs.Add(1)
				} else {
					done.Add(1)
				}
				samples[i] = append(samples[i], time.Since(scheduled).Nanoseconds())
			}
		}(i)
	}
	var offered, accepted int64
	total := int64(rate) * int64(seconds)
	for offered < total {
		now := time.Now()
		due := int64(now.Sub(start)) * rate / int64(time.Second)
		if due > total {
			due = total
		}
		if due <= offered {
			time.Sleep(50 * time.Microsecond)
			continue
		}
		for offered < due {
			scheduled := start.Add(time.Duration(offered) * time.Second / rate)
			select {
			case jobs <- scheduled:
				accepted++
			default:
			}
			offered++
		}
	}
	offer := time.Since(start)
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	all := []int64{}
	for _, s := range samples {
		all = append(all, s...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	pct := func(q float64) float64 {
		if len(all) == 0 {
			return 0
		}
		return float64(all[int(float64(len(all)-1)*q)]) / 1e6
	}
	r := Result{TargetRPS: rate, Offered: offered, Accepted: accepted, Completed: done.Load(), Errors: errs.Load(), Dropped: offered - accepted, OfferSeconds: offer.Seconds(), TotalSeconds: elapsed.Seconds(), CompletedRPS: float64(done.Load()) / elapsed.Seconds(), P50ms: pct(.5), P95ms: pct(.95), P99ms: pct(.99), GCPercent: os.Getenv("GOGC"), MemoryLimit: os.Getenv("GOMEMLIMIT"), GCCycles: after.NumGC - before.NumGC, GCPauseMs: float64(after.PauseTotalNs-before.PauseTotalNs) / 1e6, HeapInuseMiB: float64(after.HeapInuse) / (1024 * 1024), HeapSysMiB: float64(after.HeapSys) / (1024 * 1024)}
	r.Warmup = warmup
	if accepted > 0 {
		r.AllocBytesPerAccepted = float64(after.TotalAlloc-before.TotalAlloc) / float64(accepted)
	}
	raw, _ := json.Marshal(r)
	fmt.Println("LOCAL_OPEN_LOOP " + string(raw))
	if os.Getenv("PROVIDER_LOCAL_REQUIRE_TARGET") == "1" && (r.Dropped != 0 || r.Errors != 0 || r.Completed != r.Offered || r.CompletedRPS < float64(rate)*0.99) {
		panic("provider did not meet open-loop target: require all arrivals completed, zero errors/drops and at least 99% nominal RPS")
	}
	return true
}
