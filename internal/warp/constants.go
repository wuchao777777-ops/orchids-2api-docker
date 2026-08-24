package warp

import (
	"net/http"
	"runtime"
	"strings"
)

const (
	warpAPIBaseURL   = "https://app.warp.dev"
	warpGraphQLURL   = warpAPIBaseURL + "/graphql"
	warpGraphQLV2URL = warpAPIBaseURL + "/graphql/v2"
	warpAIURL        = warpAPIBaseURL + "/ai/multi-agent"
	warpLoginURL     = warpAPIBaseURL + "/client/login"
	// Verified on 2026-03-14 with a real Warp refresh token:
	// this key exchanges refresh_token -> id_token successfully.
	warpFirebaseKey  = "AIzaSyBdy3O3S9hrdayLJxJ7mriBR4qgUaUygAs"
	warpFirebaseURL  = "https://securetoken.googleapis.com/v1/token?key=" + warpFirebaseKey
	clientVersion    = "v0.2026.08.19.08.15.stable_01"
	clientID         = "warp-app"
	identifier       = "cli-agent-auto"
	computerUseModel = "computer-use-agent-auto"
)

const defaultModel = "auto-open"

func canonicalModelID(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func NormalizeModelID(model string) string {
	return canonicalModelID(model)
}

func applyWarpClientHeaders(req *http.Request) {
	if req == nil {
		return
	}
	req.Header.Set("X-Warp-Client-ID", clientID)
	req.Header.Set("X-Warp-Client-Version", clientVersion)
	if category := warpOSCategory(); category != "" {
		req.Header.Set("X-Warp-OS-Category", category)
	}
	if name := warpOSCategory(); name != "" {
		req.Header.Set("X-Warp-OS-Name", name)
	}
	req.Header.Set("User-Agent", "")
}

func warpOSCategory() string {
	switch runtime.GOOS {
	case "darwin":
		return "MacOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return runtime.GOOS
	}
}
