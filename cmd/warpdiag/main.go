package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/warp"
)

func main() {
	var refreshToken string
	var proxyURL string
	var useUTLS bool
	var model string
	var prompt string
	var deviceID string
	var requestID string
	var timeoutSeconds int

	flag.StringVar(&refreshToken, "refresh-token", "", "Warp refresh token")
	flag.StringVar(&proxyURL, "proxy-url", "", "Proxy URL, e.g. http://user:pass@host:port/ or socks5://...")
	flag.BoolVar(&useUTLS, "utls", false, "Use uTLS Chrome-like TLS fingerprint for diagnostic requests")
	flag.StringVar(&model, "model", "warp-basic", "Warp model for AI probe")
	flag.StringVar(&prompt, "prompt", "hello from warpdiag", "Prompt used for AI probe")
	flag.StringVar(&deviceID, "device-id", "", "Optional fixed device id")
	flag.StringVar(&requestID, "request-id", "", "Optional fixed request id")
	flag.IntVar(&timeoutSeconds, "timeout", 120, "Per-run timeout in seconds")
	flag.Parse()

	if refreshToken == "" {
		fmt.Fprintln(os.Stderr, "missing required flag: -refresh-token")
		os.Exit(2)
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	result, err := warp.RunDiagnostic(ctx, warp.DiagnosticOptions{
		RefreshToken: refreshToken,
		ProxyURL:     proxyURL,
		UseUTLS:      useUTLS,
		Model:        model,
		Prompt:       prompt,
		DeviceID:     deviceID,
		RequestID:    requestID,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
