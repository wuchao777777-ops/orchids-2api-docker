package util

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// HttpClientCache provides a thread-safe cache for http.Client instances
// based on their proxy configuration. This ensures that we reuse TCP connections
// (Keep-Alive) instead of exhausting ephemeral ports and paying the TLS handshake
// penalty on every upstream request.
var httpClientCache struct {
	mu      sync.RWMutex
	clients map[string]*http.Client
}

func init() {
	httpClientCache.clients = make(map[string]*http.Client)
}

// GetSharedHTTPClient returns a shared http.Client.
// The proxyKey should uniquely identify the proxy configuration (e.g., the Proxy URL or "direct").
// Transport configuration (like timeouts) should be uniform per proxyKey.
func GetSharedHTTPClient(proxyKey string, timeout time.Duration, proxyFunc func(*http.Request) (*url.URL, error)) *http.Client {
	if proxyKey == "" {
		proxyKey = "direct"
	}

	httpClientCache.mu.RLock()
	client, ok := httpClientCache.clients[proxyKey]
	httpClientCache.mu.RUnlock()
	if ok {
		// Just ensure timeout matches (though we generally expect it to be consistent per application)
		if client.Timeout != timeout {
			// If timeout differs (rare), we return a shallow copy of the client with the new timeout,
			// sharing the same underlying Transport (which holds the connection pool).
			clone := *client
			clone.Timeout = timeout
			return &clone
		}
		return client
	}

	httpClientCache.mu.Lock()
	defer httpClientCache.mu.Unlock()

	// Double check
	if client, ok = httpClientCache.clients[proxyKey]; ok {
		if client.Timeout != timeout {
			clone := *client
			clone.Timeout = timeout
			return &clone
		}
		return client
	}

	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       200, // Important for High concurrency
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		Proxy:                 proxyFunc,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: false},
	}

	newClient := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	httpClientCache.clients[proxyKey] = newClient
	return newClient
}

// generateProxyKey generates a string key based on the proxy config.
func GenerateProxyKey(proxyHTTP, proxyHTTPS, proxyUser string) string {
	if proxyHTTP == "" && proxyHTTPS == "" {
		return "direct"
	}
	// Combine to strictly separate different proxy configurations
	return proxyHTTP + "|" + proxyHTTPS + "|" + proxyUser
}
