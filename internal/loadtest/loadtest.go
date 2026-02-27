package loadtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Mode string

const (
	ModeExternal Mode = "external"
	ModeSelf     Mode = "self"
)

type Channel string

const (
	ChannelOrchids Channel = "orchids"
	ChannelWarp    Channel = "warp"
	ChannelBoth    Channel = "both"
)

type Scenario string

const (
	ScenarioSimple         Scenario = "simple"
	ScenarioMultiTurn      Scenario = "multi_turn"
	ScenarioLargeFile      Scenario = "large_file"
	ScenarioComplexHistory Scenario = "complex_history"
	ScenarioWithTools      Scenario = "with_tools"
)

type Config struct {
	BaseURL        string
	Mode           Mode
	Channel        Channel
	Model          string
	Duration       time.Duration
	Concurrency    int
	TargetRPM      float64
	StreamRatio    float64 // 0..1
	RequestTimeout time.Duration
	Seed           int64

	// Payload sizing
	LargeBytes int

	// Scenario weights
	Weights map[Scenario]float64
}

type Result struct {
	StartedAt time.Time
	EndedAt   time.Time

	Total    int64
	Success  int64
	Empty    int64
	Errors   int64
	HTTP5xx  int64
	HTTP4xx  int64
	HTTP429  int64
	Canceled int64

	LatenciesMs []int64

	ByScenario map[Scenario]*ScenarioResult
}

type ScenarioResult struct {
	Total   int64
	Success int64
	Empty   int64
	Errors  int64
}

func DefaultWeights() map[Scenario]float64 {
	return map[Scenario]float64{
		ScenarioSimple:         0.30,
		ScenarioMultiTurn:      0.25,
		ScenarioLargeFile:      0.25,
		ScenarioComplexHistory: 0.10,
		ScenarioWithTools:      0.10,
	}
}

func DefaultConfig() Config {
	return Config{
		Mode:           ModeExternal,
		Channel:        ChannelBoth,
		Model:          "claude-sonnet-4-6",
		Duration:       60 * time.Second,
		Concurrency:    20,
		TargetRPM:      600, // ~10 RPS
		StreamRatio:    0.6,
		RequestTimeout: 180 * time.Second,
		Seed:           time.Now().UnixNano(),
		LargeBytes:     256 * 1024,
		Weights:        DefaultWeights(),
	}
}

type runner struct {
	cfg   Config
	hc    *http.Client
	rngMu sync.Mutex
	rng   *rand.Rand
	res   *Result
	latMu sync.Mutex
	nonce uint64
}

func Run(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.Duration <= 0 {
		cfg.Duration = 30 * time.Second
	}
	if cfg.TargetRPM <= 0 {
		cfg.TargetRPM = 60
	}
	if cfg.StreamRatio < 0 {
		cfg.StreamRatio = 0
	}
	if cfg.StreamRatio > 1 {
		cfg.StreamRatio = 1
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 120 * time.Second
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = "claude-sonnet-4-6"
	}
	if cfg.LargeBytes <= 0 {
		cfg.LargeBytes = 64 * 1024
	}
	if cfg.Weights == nil {
		cfg.Weights = DefaultWeights()
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		return nil, errors.New("baseURL is required")
	}

	r := &runner{
		cfg: cfg,
		hc:  &http.Client{Timeout: cfg.RequestTimeout},
		rng: rand.New(rand.NewSource(cfg.Seed)),
		res: &Result{StartedAt: time.Now(), ByScenario: make(map[Scenario]*ScenarioResult)},
	}
	for sc := range cfg.Weights {
		r.res.ByScenario[sc] = &ScenarioResult{}
	}

	endCtx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()

	var wg sync.WaitGroup
	jobs := make(chan struct{}, cfg.Concurrency*2)

	// rate controller
	interval := time.Duration(float64(time.Minute) / cfg.TargetRPM)
	if interval < 5*time.Millisecond {
		interval = 5 * time.Millisecond
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-endCtx.Done():
				close(jobs)
				return
			case <-t.C:
				select {
				case jobs <- struct{}{}:
				default:
					// queue full: skip tick to avoid unbounded backlog
				}
			}
		}
	}()

	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				r.doOne(endCtx)
			}
		}()
	}

	wg.Wait()
	r.res.EndedAt = time.Now()
	return r.res, nil
}

func (r *runner) doOne(ctx context.Context) {
	sc := r.pickScenario()
	stream := r.pickStream()
	path := r.pickPath()

	body := r.buildRequest(sc, stream)
	start := time.Now()

	atomic.AddInt64(&r.res.Total, 1)
	atomic.AddInt64(&r.res.ByScenario[sc].Total, 1)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.BaseURL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.hc.Do(req)
	lat := time.Since(start).Milliseconds()
	r.recordLatency(lat)

	if err != nil {
		atomic.AddInt64(&r.res.Errors, 1)
		atomic.AddInt64(&r.res.ByScenario[sc].Errors, 1)
		if strings.Contains(strings.ToLower(err.Error()), "context canceled") {
			atomic.AddInt64(&r.res.Canceled, 1)
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		atomic.AddInt64(&r.res.HTTP5xx, 1)
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		atomic.AddInt64(&r.res.HTTP4xx, 1)
		if resp.StatusCode == 429 {
			atomic.AddInt64(&r.res.HTTP429, 1)
		}
	}

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))

	if resp.StatusCode != 200 {
		atomic.AddInt64(&r.res.Errors, 1)
		atomic.AddInt64(&r.res.ByScenario[sc].Errors, 1)
		return
	}

	nonEmpty := false
	if stream {
		// Anthropic SSE typically contains "\"type\":\"text_delta\"" and a non-empty "text".
		// We keep this intentionally loose so it works across slight format variations.
		str := string(data)
		nonEmpty = strings.Contains(str, "\"text_delta\"") || strings.Contains(str, "event: content_block_delta")
	} else {
		nonEmpty = jsonHasNonEmptyText(data)
	}

	if nonEmpty {
		atomic.AddInt64(&r.res.Success, 1)
		atomic.AddInt64(&r.res.ByScenario[sc].Success, 1)
		return
	}
	atomic.AddInt64(&r.res.Empty, 1)
	atomic.AddInt64(&r.res.ByScenario[sc].Empty, 1)
}

func (r *runner) recordLatency(ms int64) {
	r.latMu.Lock()
	r.res.LatenciesMs = append(r.res.LatenciesMs, ms)
	r.latMu.Unlock()
}

func (r *runner) pickPath() string {
	switch r.cfg.Channel {
	case ChannelOrchids:
		return "/orchids/v1/messages"
	case ChannelWarp:
		return "/warp/v1/messages"
	default:
		if r.randFloat() < 0.5 {
			return "/orchids/v1/messages"
		}
		return "/warp/v1/messages"
	}
}

func (r *runner) pickStream() bool {
	return r.randFloat() < r.cfg.StreamRatio
}

func (r *runner) pickScenario() Scenario {
	// weighted random
	total := 0.0
	for _, w := range r.cfg.Weights {
		if w > 0 {
			total += w
		}
	}
	if total <= 0 {
		return ScenarioSimple
	}
	x := r.randFloat() * total
	acc := 0.0
	for sc, w := range r.cfg.Weights {
		if w <= 0 {
			continue
		}
		acc += w
		if x <= acc {
			return sc
		}
	}
	return ScenarioSimple
}

func (r *runner) randFloat() float64 {
	r.rngMu.Lock()
	defer r.rngMu.Unlock()
	return r.rng.Float64()
}

func (r *runner) buildRequest(sc Scenario, stream bool) []byte {
	model := strings.TrimSpace(r.cfg.Model)
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	convID := ""
	messages := make([]map[string]any, 0, 32)

	mkMsg := func(role, text string) map[string]any {
		return map[string]any{"role": role, "content": text}
	}

	switch sc {
	case ScenarioSimple:
		messages = append(messages, mkMsg("user", "hi"))
	case ScenarioMultiTurn:
		convID = "conv_lt_" + r.randID()
		messages = append(messages, mkMsg("user", "hi"))
		messages = append(messages, mkMsg("assistant", "hello"))
		messages = append(messages, mkMsg("user", "please answer with a short sentence"))
	case ScenarioLargeFile:
		blob := strings.Repeat("A", r.cfg.LargeBytes)
		messages = append(messages, mkMsg("user", "Here is a large payload:\n"+blob))
	case ScenarioComplexHistory:
		convID = "conv_lt_" + r.randID()
		for i := 0; i < 10; i++ {
			messages = append(messages, mkMsg("user", fmt.Sprintf("turn %d: question about something with details", i)))
			messages = append(messages, mkMsg("assistant", fmt.Sprintf("turn %d: response with some details", i)))
		}
		messages = append(messages, mkMsg("user", "summarize the previous conversation in one line"))
	case ScenarioWithTools:
		// This only exercises proxy parsing; upstream may choose to ignore.
		messages = append(messages, mkMsg("user", "Please list files in the current directory."))
	default:
		messages = append(messages, mkMsg("user", "hi"))
	}

	nonce := r.nextNonce()
	messages = addNonceToLastUser(messages, nonce)

	payload := map[string]any{
		"model":           model,
		"messages":        messages,
		"system":          []any{},
		"stream":          stream,
		"conversation_id": convID,
	}
	b, _ := json.Marshal(payload)
	return b
}

func (r *runner) nextNonce() string {
	n := atomic.AddUint64(&r.nonce, 1)
	return fmt.Sprintf("lt_nonce:%d", n)
}

func addNonceToLastUser(messages []map[string]any, nonce string) []map[string]any {
	nonce = strings.TrimSpace(nonce)
	if nonce == "" {
		return messages
	}
	for i := len(messages) - 1; i >= 0; i-- {
		role, _ := messages[i]["role"].(string)
		if !strings.EqualFold(role, "user") {
			continue
		}
		if content, ok := messages[i]["content"].(string); ok {
			if strings.TrimSpace(content) == "" {
				messages[i]["content"] = nonce
			} else {
				messages[i]["content"] = content + "\n\n" + nonce
			}
			return messages
		}
		messages[i]["content"] = fmt.Sprintf("%v\n\n%s", messages[i]["content"], nonce)
		return messages
	}
	return append(messages, map[string]any{"role": "user", "content": nonce})
}

func (r *runner) randID() string {
	// cheap
	rng := r.randFloat()
	return fmt.Sprintf("%08x", int64(rng*math.MaxInt32))
}

func jsonHasNonEmptyText(b []byte) bool {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	content, _ := m["content"].([]any)
	for _, blk := range content {
		bm, ok := blk.(map[string]any)
		if !ok {
			continue
		}
		t, _ := bm["type"].(string)
		if t != "text" {
			continue
		}
		text, _ := bm["text"].(string)
		if strings.TrimSpace(text) != "" {
			return true
		}
	}
	return false
}

// DetectBaseURL tries to auto-detect a base URL.
// Priority:
// 1) ORCHIDS_BASE_URL
// 2) config.json port => http://127.0.0.1:<port>
// 3) default http://127.0.0.1:8080
func DetectBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("ORCHIDS_BASE_URL")); v != "" {
		return v
	}
	// try config.json in cwd
	data, err := os.ReadFile("config.json")
	if err == nil {
		var cfg map[string]any
		if json.Unmarshal(data, &cfg) == nil {
			if p, ok := cfg["port"].(string); ok {
				p = strings.TrimSpace(p)
				if p != "" {
					return "http://127.0.0.1:" + p
				}
			}
			if pf, ok := cfg["port"].(float64); ok && pf > 0 {
				return fmt.Sprintf("http://127.0.0.1:%d", int(pf))
			}
		}
	}
	return "http://127.0.0.1:8080"
}

type Summary struct {
	Duration      time.Duration
	Total         int64
	RPS           float64
	AvgMs         float64
	P50Ms         int64
	P90Ms         int64
	P99Ms         int64
	Success       int64
	Empty         int64
	Errors        int64
	ByScenarioOut map[Scenario]ScenarioResult
}

func Summarize(r *Result) Summary {
	dur := r.EndedAt.Sub(r.StartedAt)
	lats := append([]int64(nil), r.LatenciesMs...)
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })

	p := func(q float64) int64 {
		if len(lats) == 0 {
			return 0
		}
		idx := int(math.Ceil(q*float64(len(lats)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(lats) {
			idx = len(lats) - 1
		}
		return lats[idx]
	}

	sum := int64(0)
	for _, v := range lats {
		sum += v
	}
	avg := 0.0
	if len(lats) > 0 {
		avg = float64(sum) / float64(len(lats))
	}

	rps := 0.0
	if dur > 0 {
		rps = float64(r.Total) / dur.Seconds()
	}

	by := make(map[Scenario]ScenarioResult, len(r.ByScenario))
	for sc, sr := range r.ByScenario {
		by[sc] = *sr
	}

	return Summary{
		Duration:      dur,
		Total:         r.Total,
		RPS:           rps,
		AvgMs:         avg,
		P50Ms:         p(0.50),
		P90Ms:         p(0.90),
		P99Ms:         p(0.99),
		Success:       r.Success,
		Empty:         r.Empty,
		Errors:        r.Errors,
		ByScenarioOut: by,
	}
}
