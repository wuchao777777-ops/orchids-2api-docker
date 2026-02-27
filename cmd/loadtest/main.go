package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"orchids-api/internal/loadtest"
)

func main() {
	cfg := loadtest.DefaultConfig()

	baseURL := flag.String("base", "", "Base URL (default: auto-detect via ORCHIDS_BASE_URL/config.json)")
	mode := flag.String("mode", string(loadtest.ModeExternal), "Mode: external|self (self not yet implemented)")
	channel := flag.String("channel", string(loadtest.ChannelBoth), "Channel: orchids|warp|both")
	model := flag.String("model", cfg.Model, "Model ID")
	dur := flag.Duration("duration", cfg.Duration, "Run duration")
	conc := flag.Int("c", cfg.Concurrency, "Concurrency")
	rpm := flag.Float64("rpm", cfg.TargetRPM, "Target RPM")
	stream := flag.Float64("stream", cfg.StreamRatio, "Stream ratio 0..1")
	timeout := flag.Duration("timeout", cfg.RequestTimeout, "Per-request timeout")
	largeKB := flag.Int("large_kb", cfg.LargeBytes/1024, "Large payload size (KB)")
	seed := flag.Int64("seed", cfg.Seed, "RNG seed")

	flag.Parse()

	cfg.Mode = loadtest.Mode(strings.ToLower(strings.TrimSpace(*mode)))
	cfg.Channel = loadtest.Channel(strings.ToLower(strings.TrimSpace(*channel)))
	cfg.Model = strings.TrimSpace(*model)
	cfg.Duration = *dur
	cfg.Concurrency = *conc
	cfg.TargetRPM = *rpm
	cfg.StreamRatio = *stream
	cfg.RequestTimeout = *timeout
	cfg.LargeBytes = *largeKB * 1024
	cfg.Seed = *seed

	if strings.TrimSpace(*baseURL) != "" {
		cfg.BaseURL = *baseURL
	} else {
		cfg.BaseURL = loadtest.DetectBaseURL()
	}

	var self *loadtest.SelfServer
	if cfg.Mode == loadtest.ModeSelf {
		self = loadtest.StartSelfServer()
		defer self.Close()
		cfg.BaseURL = self.BaseURL
	}
	if cfg.Mode != loadtest.ModeExternal && cfg.Mode != loadtest.ModeSelf {
		fmt.Fprintln(os.Stderr, "Invalid -mode; use external or self")
		os.Exit(2)
	}

	ctx := context.Background()
	res, err := loadtest.Run(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadtest error:", err)
		os.Exit(1)
	}

	s := loadtest.Summarize(res)

	fmt.Println("============================")
	fmt.Printf("Base URL: %s\n", cfg.BaseURL)
	fmt.Printf("Duration: %s\n", s.Duration.Round(time.Millisecond))
	fmt.Printf("Total: %d\n", s.Total)
	fmt.Printf("RPS: %.2f (≈ %.0f RPM)\n", s.RPS, s.RPS*60)
	fmt.Println()
	fmt.Println("Latency (ms):")
	fmt.Printf("  avg: %.0f\n", s.AvgMs)
	fmt.Printf("  p50: %d\n", s.P50Ms)
	fmt.Printf("  p90: %d\n", s.P90Ms)
	fmt.Printf("  p99: %d\n", s.P99Ms)
	fmt.Println()
	fmt.Println("Results:")
	fmt.Printf("  success(non-empty): %d\n", s.Success)
	fmt.Printf("  empty: %d\n", s.Empty)
	fmt.Printf("  errors: %d\n", s.Errors)
	fmt.Println()
	fmt.Println("Scenarios:")
	keys := make([]string, 0, len(s.ByScenarioOut))
	for sc := range s.ByScenarioOut {
		keys = append(keys, string(sc))
	}
	sort.Strings(keys)
	for _, k := range keys {
		sc := loadtest.Scenario(k)
		r := s.ByScenarioOut[sc]
		fmt.Printf("  %-15s  total=%d success=%d empty=%d errors=%d\n", k, r.Total, r.Success, r.Empty, r.Errors)
	}
	fmt.Println("============================")
}
