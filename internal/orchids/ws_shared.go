package orchids

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"orchids-api/internal/clerk"
	"orchids-api/internal/util"
)

const (
	orchidsWSConnectTimeout = 5 * time.Second // Reduced from 10s for faster retry
	orchidsWSReadTimeout    = 600 * time.Second
	orchidsWSRequestTimeout = 60 * time.Second
	orchidsWSPingInterval   = 10 * time.Second
	orchidsWSUserAgent      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Orchids/0.0.57 Chrome/138.0.7204.251 Electron/37.10.3 Safari/537.36"
	orchidsWSOrigin         = "https://www.orchids.app"
)

type wsFallbackError struct {
	err error
}

func (e wsFallbackError) Error() string {
	return e.err.Error()
}

func (e wsFallbackError) Unwrap() error {
	return e.err
}

func (c *Client) getWSToken() (string, error) {
	if c.config != nil && strings.TrimSpace(c.config.UpstreamToken) != "" {
		return c.config.UpstreamToken, nil
	}

	if c.config != nil && strings.TrimSpace(c.config.ClientCookie) != "" {
		proxyFunc := http.ProxyFromEnvironment
		if c.config != nil {
			proxyFunc = util.ProxyFunc(c.config.ProxyHTTP, c.config.ProxyHTTPS, c.config.ProxyUser, c.config.ProxyPass, c.config.ProxyBypass)
		}
		info, err := clerk.FetchAccountInfoWithProjectAndSessionProxy(c.config.ClientCookie, c.config.SessionCookie, c.config.ProjectID, proxyFunc)
		if err == nil && info.JWT != "" {
			return info.JWT, nil
		}
	}

	return c.GetToken()
}

func extractOrchidsText(msg map[string]interface{}) string {
	if delta, ok := msg["delta"].(string); ok {
		return delta
	}
	if text, ok := msg["text"].(string); ok {
		return text
	}
	if data, ok := msg["data"].(map[string]interface{}); ok {
		if text, ok := data["text"].(string); ok {
			return text
		}
	}
	if chunk, ok := msg["chunk"]; ok {
		if s, ok := chunk.(string); ok {
			return s
		}
		if m, ok := chunk.(map[string]interface{}); ok {
			if text, ok := m["text"].(string); ok {
				return text
			}
			if text, ok := m["content"].(string); ok {
				return text
			}
		}
	}
	return ""
}

func urlEncode(value string) string {
	return url.QueryEscape(value)
}

func truncateTextWithEllipsis(text string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(text) <= maxLen {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return string(runes[:maxLen]) + "…[truncated]"
}
