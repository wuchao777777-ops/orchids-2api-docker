package util

import (
	"net/http"
	"net/url"
	"testing"

	"orchids-api/internal/config"
)

func TestProxyFunc_NoSchemeDefaultsToHTTP(t *testing.T) {
	proxyFunc := ProxyFunc("proxy.local:3128", "", "", "", nil)
	proxyURL, err := proxyFunc(&http.Request{URL: &url.URL{Scheme: "http", Host: "example.com"}})
	if err != nil {
		t.Fatalf("proxy func failed: %v", err)
	}
	if proxyURL == nil {
		t.Fatal("expected proxy url")
	}
	if proxyURL.Scheme != "http" {
		t.Fatalf("expected http scheme, got %q", proxyURL.Scheme)
	}
	if proxyURL.Host != "proxy.local:3128" {
		t.Fatalf("unexpected proxy host: %s", proxyURL.Host)
	}
}

func TestProxyFunc_WSSUsesHTTPSProxy(t *testing.T) {
	proxyFunc := ProxyFunc("http://proxy.local:3128", "http://secure.proxy:8443", "", "", nil)
	proxyURL, err := proxyFunc(&http.Request{URL: &url.URL{Scheme: "wss", Host: "example.com"}})
	if err != nil {
		t.Fatalf("proxy func failed: %v", err)
	}
	if proxyURL == nil || proxyURL.Host != "secure.proxy:8443" {
		t.Fatalf("unexpected proxy url: %v", proxyURL)
	}
}

func TestProxyFunc_LeadingDotBypass(t *testing.T) {
	proxyFunc := ProxyFunc("http://proxy.local:3128", "", "", "", []string{".example.com"})
	proxyURL, err := proxyFunc(&http.Request{URL: &url.URL{Scheme: "https", Host: "api.example.com"}})
	if err != nil {
		t.Fatalf("proxy func failed: %v", err)
	}
	if proxyURL != nil {
		t.Fatalf("expected bypass, got %v", proxyURL)
	}
}

func TestProxyFuncFromConfig_ProxyURL(t *testing.T) {
	proxyFunc := ProxyFuncFromConfig(&config.Config{
		ProxyURL:    "http://user:pass@proxy.local:3128/",
		ProxyBypass: []string{"internal.local"},
	})

	proxyURL, err := proxyFunc(&http.Request{URL: &url.URL{Scheme: "https", Host: "example.com"}})
	if err != nil {
		t.Fatalf("proxy func failed: %v", err)
	}
	if proxyURL == nil || proxyURL.Host != "proxy.local:3128" {
		t.Fatalf("unexpected proxy url: %v", proxyURL)
	}
	if proxyURL.User == nil || proxyURL.User.Username() != "user" {
		t.Fatalf("unexpected proxy user: %v", proxyURL.User)
	}
	if pass, ok := proxyURL.User.Password(); !ok || pass != "pass" {
		t.Fatalf("unexpected proxy password")
	}
}

func TestProxyFuncFromConfig_EmptyMeansDirect(t *testing.T) {
	proxyFunc := ProxyFuncFromConfig(&config.Config{})
	proxyURL, err := proxyFunc(&http.Request{URL: &url.URL{Scheme: "https", Host: "example.com"}})
	if err != nil {
		t.Fatalf("proxy func failed: %v", err)
	}
	if proxyURL != nil {
		t.Fatalf("expected direct connection, got %v", proxyURL)
	}
}

func TestParseProxyURL_Socks5(t *testing.T) {
	proxyURL, err := ParseProxyURL("socks5://user:pass@127.0.0.1:1080/")
	if err != nil {
		t.Fatalf("ParseProxyURL() error = %v", err)
	}
	if proxyURL == nil {
		t.Fatal("expected proxy url")
	}
	if proxyURL.Scheme != "socks5" {
		t.Fatalf("Scheme=%q want socks5", proxyURL.Scheme)
	}
	if proxyURL.Host != "127.0.0.1:1080" {
		t.Fatalf("Host=%q want 127.0.0.1:1080", proxyURL.Host)
	}
}
