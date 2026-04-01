package clerk

import (
	"encoding/base64"
	"fmt"
	"github.com/goccy/go-json"
	"io"
	"net/http"
	"net/url"
	"orchids-api/internal/util"
	"strings"
	"time"
)

const (
	// Clerk API 版本参数，升级时统一修改
	ClerkAPIVersion = "2025-11-10"
	ClerkJSVersion  = "5.117.0"

	// 默认项目 ID
	DefaultProjectID = "280b7bae-cd29-41e4-a0a6-7f603c43b607"

	// Clerk 基础 URL
	ClerkBaseURL = "https://clerk.orchids.app"

	// 请求 User-Agent
	clerkUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Orchids/0.0.57 Chrome/138.0.7204.251 Electron/37.10.3 Safari/537.36"
)

type ClientResponse struct {
	Response struct {
		ID                  string `json:"id"`
		LastActiveSessionID string `json:"last_active_session_id"`
		Sessions            []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			User   struct {
				ID             string `json:"id"`
				EmailAddresses []struct {
					EmailAddress string `json:"email_address"`
				} `json:"email_addresses"`
			} `json:"user"`
			LastActiveToken struct {
				JWT string `json:"jwt"`
			} `json:"last_active_token"`
		} `json:"sessions"`
	} `json:"response"`
}

type AccountInfo struct {
	SessionID    string
	ClientCookie string
	ClientUat    string
	ProjectID    string
	UserID       string
	Email        string
	JWT          string
}

type ParsedOrchidsCookies struct {
	ClientCookie  string
	SessionCookie string
	ClientUat     string
}

func FetchAccountInfoWithSessionProxy(clientCookie string, sessionCookie string, proxyFunc func(*http.Request) (*url.URL, error)) (*AccountInfo, error) {
	return FetchAccountInfoWithProjectAndSessionContextProxy(clientCookie, sessionCookie, "", "", "", proxyFunc)
}

func FetchAccountInfoWithProjectAndSessionProxy(clientCookie string, sessionCookie string, customProjectID string, proxyFunc func(*http.Request) (*url.URL, error)) (*AccountInfo, error) {
	return FetchAccountInfoWithProjectAndSessionContextProxy(clientCookie, sessionCookie, "", "", customProjectID, proxyFunc)
}

func FetchAccountInfoWithSessionContextProxy(clientCookie string, sessionCookie string, clientUat string, sessionID string, proxyFunc func(*http.Request) (*url.URL, error)) (*AccountInfo, error) {
	return FetchAccountInfoWithProjectAndSessionContextProxy(clientCookie, sessionCookie, clientUat, sessionID, "", proxyFunc)
}

func FetchAccountInfoWithProjectAndSessionContextProxy(clientCookie string, sessionCookie string, clientUat string, sessionID string, customProjectID string, proxyFunc func(*http.Request) (*url.URL, error)) (*AccountInfo, error) {
	url := fmt.Sprintf("%s/v1/client?__clerk_api_version=%s&_clerk_js_version=%s", ClerkBaseURL, ClerkAPIVersion, ClerkJSVersion)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", clerkUserAgent)
	req.Header.Set("Accept-Language", "zh-CN")
	req.Header.Set("Origin", "https://www.orchids.app")
	req.Header.Set("Referer", "https://www.orchids.app/")
	if sid, _ := ParseSessionInfoFromJWT(strings.TrimSpace(sessionCookie)); strings.TrimSpace(sessionID) == "" && sid != "" {
		sessionID = sid
	}
	if cookieHeader := buildClerkCookieHeader(clientCookie, sessionCookie, clientUat, sessionID); cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}

	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: util.NewBrowserLikeTransport(proxyFunc),
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch client info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Clerk 可能通过 Set-Cookie 轮转 __client cookie（rotating_token），
	// 必须捕获新值，否则旧 token 被消费后后续请求全部失效。
	effectiveCookie := clientCookie
	for _, c := range resp.Cookies() {
		if c.Name == "__client" && strings.TrimSpace(c.Value) != "" {
			effectiveCookie = c.Value
			break
		}
	}

	var clientResp ClientResponse
	if err := json.NewDecoder(resp.Body).Decode(&clientResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(clientResp.Response.Sessions) == 0 {
		return nil, fmt.Errorf("no active sessions found")
	}

	session := clientResp.Response.Sessions[0]
	if len(session.User.EmailAddresses) == 0 {
		return nil, fmt.Errorf("no email address found")
	}

	projectID := DefaultProjectID
	if customProjectID != "" {
		projectID = customProjectID
	}

	return &AccountInfo{
		SessionID:    clientResp.Response.LastActiveSessionID,
		ClientCookie: effectiveCookie,
		ClientUat:    firstNonEmptyClientUat(resp.Cookies(), clientUat),
		ProjectID:    projectID,
		UserID:       session.User.ID,
		Email:        session.User.EmailAddresses[0].EmailAddress,
		JWT:          session.LastActiveToken.JWT,
	}, nil
}

func buildClerkCookieHeader(clientCookie string, sessionCookie string, clientUat string, sessionID string) string {
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
	return strings.Join(parts, "; ")
}

func firstNonEmptyClientUat(cookies []*http.Cookie, fallback string) string {
	for _, c := range cookies {
		if c == nil || c.Name != "__client_uat" {
			continue
		}
		if value := strings.TrimSpace(c.Value); value != "" {
			return value
		}
	}
	fallback = strings.TrimSpace(fallback)
	if fallback != "" {
		return fallback
	}
	return fmt.Sprintf("%d", time.Now().Unix())
}

func ParseClientCookies(input string) (clientJWT string, sessionJWT string, err error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", "", nil
	}

	if strings.Contains(trimmed, "__client=") {
		client, err := extractCookieValue(trimmed, "__client")
		if err != nil {
			return "", "", err
		}
		client = normalizeCookieTokenValue(client)
		if client == "" {
			return "", "", fmt.Errorf("missing __client value")
		}
		session, _ := extractCookieValue(trimmed, "__session")
		session = normalizeCookieTokenValue(session)
		return client, session, nil
	}

	if strings.Contains(trimmed, "|") {
		parts := strings.Split(trimmed, "|")
		jwt := strings.TrimSpace(parts[0])
		if isLikelyJWT(jwt) {
			return jwt, "", nil
		}
		return "", "", fmt.Errorf("invalid JWT before '|'")
	}

	if isLikelyJWT(trimmed) {
		return trimmed, "", nil
	}

	if token := normalizeCookieTokenValue(trimmed); isLikelyOpaqueClientToken(token) {
		return token, "", nil
	}

	return "", "", fmt.Errorf("unsupported client cookie format")
}

func ParseOrchidsCookies(input string) (ParsedOrchidsCookies, bool, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ParsedOrchidsCookies{}, false, nil
	}

	if strings.HasPrefix(trimmed, "[") {
		var cookies []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal([]byte(trimmed), &cookies); err != nil {
			return ParsedOrchidsCookies{}, false, err
		}
		parsed := collectOrchidsCookies(func(yield func(string, string)) {
			for _, cookie := range cookies {
				yield(cookie.Name, cookie.Value)
			}
		})
		return parsed, parsed.hasAny(), nil
	}

	if strings.Contains(trimmed, "=") {
		parts := strings.FieldsFunc(trimmed, func(r rune) bool {
			return r == ';' || r == '\n'
		})
		parsed := collectOrchidsCookies(func(yield func(string, string)) {
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				kv := strings.SplitN(part, "=", 2)
				if len(kv) != 2 {
					continue
				}
				yield(strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1]))
			}
		})
		return parsed, parsed.hasAny(), nil
	}

	return ParsedOrchidsCookies{}, false, nil
}

func (p ParsedOrchidsCookies) hasAny() bool {
	return strings.TrimSpace(p.ClientCookie) != "" || strings.TrimSpace(p.SessionCookie) != "" || strings.TrimSpace(p.ClientUat) != ""
}

func collectOrchidsCookies(iter func(func(string, string))) ParsedOrchidsCookies {
	var parsed ParsedOrchidsCookies
	var exactClient bool
	var exactSession bool
	var exactClientUat bool

	iter(func(name, value string) {
		name = strings.TrimSpace(name)
		value = normalizeCookieTokenValue(value)
		if name == "" || value == "" {
			return
		}

		switch {
		case name == "__client":
			parsed.ClientCookie = value
			exactClient = true
		case isNamespacedClientCookie(name) && !exactClient && parsed.ClientCookie == "":
			parsed.ClientCookie = value
		case name == "__session":
			parsed.SessionCookie = value
			exactSession = true
		case isNamespacedSessionCookie(name) && !exactSession && parsed.SessionCookie == "":
			parsed.SessionCookie = value
		case name == "__client_uat":
			parsed.ClientUat = value
			exactClientUat = true
		case isNamespacedClientUatCookie(name) && !exactClientUat && parsed.ClientUat == "":
			parsed.ClientUat = value
		}
	})

	return parsed
}

func isNamespacedClientCookie(name string) bool {
	return name != "__client_uat" && strings.HasPrefix(name, "__client_") && !isNamespacedClientUatCookie(name)
}

func isNamespacedSessionCookie(name string) bool {
	return strings.HasPrefix(name, "__session_")
}

func isNamespacedClientUatCookie(name string) bool {
	return strings.HasPrefix(name, "__client_uat_")
}

func extractCookieValue(input string, name string) (string, error) {
	parts := strings.Split(input, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(kv[0]), name) {
			return strings.TrimSpace(kv[1]), nil
		}
	}
	return "", nil
}

func normalizeCookieTokenValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'")
	if value == "" {
		return ""
	}
	if decoded, err := url.QueryUnescape(value); err == nil && strings.TrimSpace(decoded) != "" {
		value = strings.TrimSpace(decoded)
		value = strings.Trim(value, "\"'")
	}
	return strings.TrimSpace(value)
}

func isLikelyOpaqueClientToken(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	if len(value) < 16 {
		return false
	}
	if strings.ContainsAny(value, " ;\t\r\n") {
		return false
	}
	return true
}

func ParseSessionInfoFromJWT(sessionJWT string) (sessionID string, userID string) {
	parts := strings.Split(sessionJWT, ".")
	if len(parts) != 3 {
		return "", ""
	}
	payload := parts[1]
	payload = strings.ReplaceAll(payload, "-", "+")
	payload = strings.ReplaceAll(payload, "_", "/")
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", ""
	}
	var data struct {
		SID string `json:"sid"`
		SUB string `json:"sub"`
		ID  string `json:"id"`
	}
	if err := json.Unmarshal(decoded, &data); err != nil {
		return "", ""
	}
	sid := data.SID
	if sid == "" {
		sid = data.ID
	}
	return sid, data.SUB
}

func isLikelyJWT(value string) bool {
	if value == "" {
		return false
	}
	if strings.Count(value, ".") != 2 {
		return false
	}
	parts := strings.Split(value, ".")
	return len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != ""
}
