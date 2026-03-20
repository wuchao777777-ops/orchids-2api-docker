package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type headerFlags []string

func (h *headerFlags) String() string {
	return strings.Join(*h, ", ")
}

func (h *headerFlags) Set(value string) error {
	*h = append(*h, value)
	return nil
}

type result struct {
	index          int
	statusCode     int
	headers        time.Duration
	firstBodyByte  time.Duration
	firstFrame     time.Duration
	total          time.Duration
	err            error
	contentType    string
	responseBytes  int64
	firstFrameLine string
}

type summary struct {
	Values []float64 `json:"values,omitempty"`
	Min    float64   `json:"min"`
	P50    float64   `json:"p50"`
	P90    float64   `json:"p90"`
	P95    float64   `json:"p95"`
	Max    float64   `json:"max"`
	Avg    float64   `json:"avg"`
}

func main() {
	var (
		targetURL        string
		model            string
		prompt           string
		requests         int
		concurrency      int
		warmup           int
		timeout          time.Duration
		bodyFile         string
		outputJSON       bool
		disableKeepAlive bool
	)
	headers := headerFlags{}

	flag.StringVar(&targetURL, "url", "http://127.0.0.1:3002/grok/v1/chat/completions", "target URL")
	flag.StringVar(&model, "model", "grok-3", "model name used when body-file is not provided")
	flag.StringVar(&prompt, "prompt", "Reply with OK only.", "prompt used when body-file is not provided")
	flag.IntVar(&requests, "requests", 5, "number of measured requests")
	flag.IntVar(&concurrency, "concurrency", 1, "number of concurrent workers")
	flag.IntVar(&warmup, "warmup", 1, "number of warmup requests before measuring")
	flag.DurationVar(&timeout, "timeout", 90*time.Second, "per-request timeout")
	flag.StringVar(&bodyFile, "body-file", "", "optional path to a raw JSON request body")
	flag.BoolVar(&outputJSON, "json", false, "emit machine-readable JSON summary")
	flag.BoolVar(&disableKeepAlive, "disable-keepalive", false, "disable HTTP keep-alive reuse")
	flag.Var(&headers, "header", "extra header in 'Key: Value' format; can be repeated")
	flag.Parse()

	if requests <= 0 {
		exitf("requests must be > 0")
	}
	if concurrency <= 0 {
		exitf("concurrency must be > 0")
	}
	if warmup < 0 {
		exitf("warmup must be >= 0")
	}

	payload, err := buildPayload(targetURL, model, prompt, bodyFile)
	if err != nil {
		exitf("build payload: %v", err)
	}

	u, err := url.Parse(targetURL)
	if err != nil {
		exitf("parse url: %v", err)
	}

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DisableKeepAlives:   disableKeepAlive,
			MaxIdleConns:        max(100, concurrency*2),
			MaxIdleConnsPerHost: max(100, concurrency*2),
			IdleConnTimeout:     90 * time.Second,
		},
	}

	reqHeaders := http.Header{}
	reqHeaders.Set("Content-Type", "application/json")
	for _, raw := range headers {
		key, value, ok := strings.Cut(raw, ":")
		if !ok {
			exitf("invalid header %q, expected 'Key: Value'", raw)
		}
		reqHeaders.Add(strings.TrimSpace(key), strings.TrimSpace(value))
	}

	for i := 0; i < warmup; i++ {
		res := runOnce(client, targetURL, payload, reqHeaders)
		if res.err != nil {
			exitf("warmup request %d failed: %v", i+1, res.err)
		}
		if res.statusCode < 200 || res.statusCode >= 300 {
			exitf("warmup request %d returned HTTP %d", i+1, res.statusCode)
		}
	}

	results := runBenchmark(client, targetURL, payload, reqHeaders, requests, concurrency)
	statusCounts := make(map[int]int)
	failures := make([]string, 0)
	headersValues := make([]float64, 0, len(results))
	firstByteValues := make([]float64, 0, len(results))
	firstFrameValues := make([]float64, 0, len(results))
	totalValues := make([]float64, 0, len(results))

	for _, res := range results {
		if res.statusCode != 0 {
			statusCounts[res.statusCode]++
		}
		if res.err != nil {
			failures = append(failures, fmt.Sprintf("request #%d: %v", res.index, res.err))
			continue
		}
		headersValues = append(headersValues, durationMillis(res.headers))
		firstByteValues = append(firstByteValues, durationMillis(res.firstBodyByte))
		firstFrameValues = append(firstFrameValues, durationMillis(res.firstFrame))
		totalValues = append(totalValues, durationMillis(res.total))
		if res.statusCode < 200 || res.statusCode >= 300 {
			failures = append(failures, fmt.Sprintf("request #%d: HTTP %d", res.index, res.statusCode))
		}
	}

	report := map[string]any{
		"url":         u.String(),
		"requests":    requests,
		"concurrency": concurrency,
		"warmup":      warmup,
		"statuses":    statusCounts,
		"errors":      failures,
		"metrics_ms": map[string]any{
			"response_headers": summarize(headersValues),
			"first_body_byte":  summarize(firstByteValues),
			"first_sse_frame":  summarize(firstFrameValues),
			"total_duration":   summarize(totalValues),
		},
	}

	if outputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			exitf("encode report: %v", err)
		}
		return
	}

	fmt.Printf("TTFB benchmark\n")
	fmt.Printf("URL: %s\n", u.String())
	fmt.Printf("Requests: %d  Concurrency: %d  Warmup: %d\n", requests, concurrency, warmup)
	fmt.Printf("Statuses:")
	if len(statusCounts) == 0 {
		fmt.Printf(" none\n")
	} else {
		keys := make([]int, 0, len(statusCounts))
		for code := range statusCounts {
			keys = append(keys, code)
		}
		sort.Ints(keys)
		for _, code := range keys {
			fmt.Printf(" %d=%d", code, statusCounts[code])
		}
		fmt.Printf("\n")
	}
	if len(failures) > 0 {
		fmt.Printf("Errors:\n")
		for _, msg := range failures {
			fmt.Printf("  - %s\n", msg)
		}
	}

	printSummary("response_headers", summarize(headersValues))
	printSummary("first_body_byte", summarize(firstByteValues))
	printSummary("first_sse_frame", summarize(firstFrameValues))
	printSummary("total_duration", summarize(totalValues))
}

func runBenchmark(client *http.Client, targetURL string, payload []byte, headers http.Header, requests, concurrency int) []result {
	jobs := make(chan int)
	resultsCh := make(chan result, requests)
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				res := runOnce(client, targetURL, payload, headers)
				res.index = idx
				resultsCh <- res
			}
		}()
	}

	go func() {
		for i := 1; i <= requests; i++ {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(resultsCh)
	}()

	results := make([]result, 0, requests)
	for res := range resultsCh {
		results = append(results, res)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].index < results[j].index
	})
	return results
}

func runOnce(client *http.Client, targetURL string, payload []byte, headers http.Header) result {
	start := time.Now()
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return result{err: err}
	}
	req.Header = headers.Clone()

	resp, err := client.Do(req)
	if err != nil {
		return result{err: err}
	}
	defer resp.Body.Close()

	res := result{
		statusCode:  resp.StatusCode,
		headers:     time.Since(start),
		contentType: strings.ToLower(resp.Header.Get("Content-Type")),
	}

	br := bufio.NewReader(resp.Body)
	if _, err := br.ReadByte(); err != nil {
		if err != io.EOF {
			res.err = err
		}
		res.total = time.Since(start)
		return res
	}
	res.firstBodyByte = time.Since(start)
	if err := br.UnreadByte(); err != nil {
		res.err = err
		res.total = time.Since(start)
		return res
	}

	if strings.Contains(res.contentType, "text/event-stream") {
		firstLine, totalBytes, err := readSSE(br, start, &res)
		res.firstFrameLine = firstLine
		res.responseBytes = totalBytes
		if err != nil && err != io.EOF {
			res.err = err
		}
	} else {
		res.firstFrame = res.firstBodyByte
		n, err := io.Copy(io.Discard, br)
		res.responseBytes = n
		if err != nil {
			res.err = err
		}
	}

	res.total = time.Since(start)
	return res
}

func readSSE(br *bufio.Reader, start time.Time, res *result) (string, int64, error) {
	var (
		totalBytes int64
		firstLine  string
	)
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			totalBytes += int64(len(line))
			trimmed := strings.TrimSpace(line)
			if res.firstFrame == 0 && trimmed != "" && !strings.HasPrefix(trimmed, ":") {
				res.firstFrame = time.Since(start)
				firstLine = trimmed
			}
		}
		if err != nil {
			if err == io.EOF {
				return firstLine, totalBytes, io.EOF
			}
			return firstLine, totalBytes, err
		}
	}
}

func buildPayload(targetURL, model, prompt, bodyFile string) ([]byte, error) {
	if strings.TrimSpace(bodyFile) != "" {
		return os.ReadFile(bodyFile)
	}

	body := map[string]any{
		"model":  model,
		"stream": true,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": prompt,
			},
		},
	}

	if strings.Contains(strings.ToLower(targetURL), "/messages") {
		body["system"] = []any{}
	}

	return json.Marshal(body)
}

func summarize(values []float64) summary {
	if len(values) == 0 {
		return summary{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	sum := 0.0
	for _, v := range sorted {
		sum += v
	}
	return summary{
		Values: sorted,
		Min:    sorted[0],
		P50:    percentile(sorted, 0.50),
		P90:    percentile(sorted, 0.90),
		P95:    percentile(sorted, 0.95),
		Max:    sorted[len(sorted)-1],
		Avg:    sum / float64(len(sorted)),
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}

	pos := p * float64(len(sorted)-1)
	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))
	if lower == upper {
		return sorted[lower]
	}
	weight := pos - float64(lower)
	return sorted[lower] + (sorted[upper]-sorted[lower])*weight
}

func durationMillis(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func printSummary(name string, s summary) {
	if len(s.Values) == 0 {
		fmt.Printf("%-18s no data\n", name)
		return
	}
	fmt.Printf(
		"%-18s min=%7.2f  p50=%7.2f  p90=%7.2f  p95=%7.2f  max=%7.2f  avg=%7.2f ms\n",
		name, s.Min, s.P50, s.P90, s.P95, s.Max, s.Avg,
	)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
