package orchids

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"orchids-api/internal/clerk"
	"orchids-api/internal/config"
	"orchids-api/internal/util"
)

func orchidsProxyFunc(cfg *config.Config) func(*http.Request) (*url.URL, error) {
	if cfg == nil {
		return http.ProxyFromEnvironment
	}
	return util.ProxyFunc(cfg.ProxyHTTP, cfg.ProxyHTTPS, cfg.ProxyUser, cfg.ProxyPass, cfg.ProxyBypass)
}

func orchidsClerkCookieHeader(clientCookie, sessionCookie, clientUat, sessionID string) string {
	clientCookie = strings.TrimSpace(clientCookie)
	sessionCookie = strings.TrimSpace(sessionCookie)
	clientUat = strings.TrimSpace(clientUat)
	sessionID = strings.TrimSpace(sessionID)

	parts := make([]string, 0, 4)
	if clientCookie != "" {
		parts = append(parts, "__client="+clientCookie)
	}
	if sessionCookie != "" {
		parts = append(parts, "__session="+sessionCookie)
	}
	if clientUat != "" {
		parts = append(parts, "__client_uat="+clientUat)
	}
	if sessionID != "" {
		parts = append(parts, "clerk_active_context="+sessionID+":")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}

func (c *Client) buildUpstreamCookieHeader() string {
	if c == nil || c.config == nil {
		return ""
	}
	return orchidsClerkCookieHeader(c.config.ClientCookie, c.config.SessionCookie, c.config.ClientUat, c.config.SessionID)
}

func (c *Client) applyClerkResponseCookies(cookies []*http.Cookie) bool {
	if c == nil || c.config == nil || len(cookies) == 0 {
		return false
	}

	updated := false
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		value := strings.TrimSpace(cookie.Value)
		if value == "" {
			continue
		}
		switch cookie.Name {
		case "__client":
			if c.config.ClientCookie != value {
				c.config.ClientCookie = value
				updated = true
			}
			if c.account != nil {
				c.account.ClientCookie = value
			}
		case "__client_uat":
			if c.config.ClientUat != value {
				c.config.ClientUat = value
				updated = true
			}
			if c.account != nil {
				c.account.ClientUat = value
			}
		}
	}
	return updated
}

func (c *Client) bootstrapClientCookieFromSession() error {
	if c == nil || c.config == nil {
		return errors.New("missing config")
	}

	c.syncConfigFromStoredAccount()
	if strings.TrimSpace(c.config.ClientCookie) != "" {
		return nil
	}
	if strings.TrimSpace(c.config.SessionCookie) == "" {
		return fmt.Errorf("signed out: missing orchids session cookie")
	}

	ctx, cancel := util.WithDefaultTimeout(context.Background(), c.requestTimeout())
	defer cancel()

	reqURL := fmt.Sprintf("%s/v1/client?__clerk_api_version=%s&_clerk_js_version=%s",
		clerk.ClerkBaseURL, clerk.ClerkAPIVersion, clerk.ClerkJSVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Orchids/0.0.57 Chrome/138.0.7204.251 Electron/37.10.3 Safari/537.36")
	req.Header.Set("Accept-Language", "zh-CN")
	req.Header.Set("Origin", "https://www.orchids.app")
	req.Header.Set("Referer", "https://www.orchids.app/")
	if cookieHeader := orchidsClerkCookieHeader("", c.config.SessionCookie, c.config.ClientUat, c.config.SessionID); cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to bootstrap orchids client cookie: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to bootstrap orchids client cookie: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	updated := c.applyClerkResponseCookies(resp.Cookies())
	_, _ = io.Copy(io.Discard, resp.Body)
	if !updated || strings.TrimSpace(c.config.ClientCookie) == "" {
		return fmt.Errorf("signed out: orchids client bootstrap returned no __client cookie")
	}
	return nil
}

func (c *Client) getChatToken() (string, error) {
	if c == nil || c.config == nil {
		return "", errors.New("missing config")
	}
	if c.config.UpstreamToken != "" {
		return c.config.UpstreamToken, nil
	}

	c.syncConfigFromStoredAccount()

	sessionCookie := strings.TrimSpace(c.config.SessionCookie)
	sessionID := strings.TrimSpace(c.config.SessionID)
	accountToken := ""
	if c.account != nil {
		accountToken = strings.TrimSpace(c.account.Token)
	}
	if accountToken != "" && accountToken != sessionCookie && tokenStillUsable(accountToken) {
		if sessionID != "" {
			setCachedToken(sessionID, accountToken)
		}
		return accountToken, nil
	}
	if cached, ok := getCachedToken(sessionID); ok {
		return cached, nil
	}

	if strings.TrimSpace(c.config.ClientCookie) == "" && sessionCookie != "" {
		if err := c.bootstrapClientCookieFromSession(); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(c.config.ClientCookie) == "" {
		return "", fmt.Errorf("signed out: missing orchids client cookie")
	}

	info, err := orchidsFetchClerkInfoWithSession(c.config.ClientCookie, c.config.SessionCookie, orchidsProxyFunc(c.config))
	if err != nil {
		return "", err
	}
	if info != nil {
		c.applyAccountInfo(info)
		c.persistAccountInfo(info)
		if jwt := strings.TrimSpace(info.JWT); jwt != "" {
			if sid := strings.TrimSpace(info.SessionID); sid != "" {
				setCachedToken(sid, jwt)
			}
			if c.account != nil {
				c.account.Token = jwt
			}
			return jwt, nil
		}
	}

	if strings.TrimSpace(c.config.SessionID) == "" {
		return "", fmt.Errorf("signed out: no active sessions found")
	}

	bearer, err := c.fetchToken()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(bearer) == "" {
		return "", fmt.Errorf("signed out: missing orchids bearer token")
	}
	if c.account != nil {
		c.account.Token = bearer
	}
	return bearer, nil
}
