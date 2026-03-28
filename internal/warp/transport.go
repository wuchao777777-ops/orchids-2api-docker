package warp

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"orchids-api/internal/logutil"
)

// warpTransport keeps the proxy function inspectable in tests while delegating
// actual I/O to the standard library transport. This mirrors CodeFreeMax's
// plain gclient usage while keeping the transport path minimal.
type warpTransport struct {
	base      *http.Transport
	proxyFunc func(*http.Request) (*url.URL, error)
}

func newWarpTransport(proxyFunc func(*http.Request) (*url.URL, error)) *warpTransport {
	if proxyFunc == nil {
		proxyFunc = http.ProxyFromEnvironment
	}

	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = proxyFunc
	base.MaxIdleConns = 100
	base.MaxIdleConnsPerHost = 20
	base.MaxConnsPerHost = 50
	base.IdleConnTimeout = 90 * time.Second

	return &warpTransport{
		base:      base,
		proxyFunc: proxyFunc,
	}
}

func (t *warpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req != nil && req.URL != nil && logutil.VerboseDiagnosticsEnabled() {
		proxyURL, proxyErr := t.resolveProxy(req)
		fields := []any{
			"target", req.URL.String(),
			"method", req.Method,
			"proxy", maskProxyURL(proxyURL),
		}
		if proxyErr != nil {
			fields = append(fields, "proxy_error", proxyErr.Error())
		}
		slog.Debug("warp proxy dispatch", fields...)
	}
	return t.base.RoundTrip(req)
}

func (t *warpTransport) resolveProxy(req *http.Request) (*url.URL, error) {
	if t == nil || t.proxyFunc == nil || req == nil {
		return nil, nil
	}
	return t.proxyFunc(req)
}

func maskProxyURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	masked := *u
	if masked.User != nil {
		username := masked.User.Username()
		if strings.TrimSpace(username) != "" {
			masked.User = url.UserPassword(username, "****")
		} else {
			masked.User = url.User("****")
		}
	}
	return masked.String()
}

func (t *warpTransport) CloseIdleConnections() {
	if t != nil && t.base != nil {
		t.base.CloseIdleConnections()
	}
}
