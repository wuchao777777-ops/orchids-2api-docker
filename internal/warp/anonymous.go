package warp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-json"
)

const (
	anonGraphQLURL  = "https://app.warp.dev/graphql/v2?op=CreateAnonymousUser"
	identityBaseURL = "https://identitytoolkit.googleapis.com/v1/accounts:signInWithCustomToken"
	googleAPIKey    = "AIzaSyBdy3O3S9hrdayLJxJ7mriBR4qgUaUygAs"
)

var anonHTTPClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: newUTLSTransport(http.ProxyFromEnvironment),
}

type anonToken struct {
	mu           sync.Mutex
	accessToken  string
	refreshToken string
	expiresAt    time.Time
	acquiring    bool
	acquireDone  chan struct{}
}

var globalAnonToken anonToken

func (a *anonToken) valid() bool {
	return a.accessToken != "" && time.Now().Add(5*time.Minute).Before(a.expiresAt)
}

// AcquireAnonymousJWT returns a valid anonymous JWT, creating or refreshing
// the anonymous user as needed. Safe for concurrent use.
func AcquireAnonymousJWT(ctx context.Context) (string, error) {
	globalAnonToken.mu.Lock()
	if globalAnonToken.valid() {
		jwt := globalAnonToken.accessToken
		globalAnonToken.mu.Unlock()
		return jwt, nil
	}
	if globalAnonToken.acquiring {
		ch := globalAnonToken.acquireDone
		globalAnonToken.mu.Unlock()
		if ch != nil {
			select {
			case <-ch:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		globalAnonToken.mu.Lock()
		if globalAnonToken.valid() {
			jwt := globalAnonToken.accessToken
			globalAnonToken.mu.Unlock()
			return jwt, nil
		}
		globalAnonToken.mu.Unlock()
		return "", fmt.Errorf("anonymous token acquisition failed")
	}

	globalAnonToken.acquiring = true
	globalAnonToken.acquireDone = make(chan struct{})
	hasRefresh := globalAnonToken.refreshToken != ""
	refresh := globalAnonToken.refreshToken
	globalAnonToken.mu.Unlock()

	var jwt string
	var newRefresh string
	var expiresIn int64
	var err error

	if hasRefresh {
		jwt, newRefresh, expiresIn, err = refreshAnonToken(ctx, refresh)
		if err != nil {
			slog.Info("Anonymous refresh failed, creating new user", "error", err)
			jwt, newRefresh, expiresIn, err = createAndExchangeAnonymous(ctx)
		}
	} else {
		jwt, newRefresh, expiresIn, err = createAndExchangeAnonymous(ctx)
	}

	globalAnonToken.mu.Lock()
	globalAnonToken.acquiring = false
	close(globalAnonToken.acquireDone)
	globalAnonToken.acquireDone = nil
	if err == nil {
		globalAnonToken.accessToken = jwt
		if newRefresh != "" {
			globalAnonToken.refreshToken = newRefresh
		}
		if expiresIn > 0 {
			globalAnonToken.expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
		} else {
			globalAnonToken.expiresAt = time.Now().Add(50 * time.Minute)
		}
	}
	globalAnonToken.mu.Unlock()

	return jwt, err
}

func createAndExchangeAnonymous(ctx context.Context) (accessToken, refreshToken string, expiresIn int64, err error) {
	slog.Info("Creating anonymous Warp user")
	idToken, err := createAnonymousUser(ctx)
	if err != nil {
		return "", "", 0, fmt.Errorf("create anonymous user: %w", err)
	}

	refreshToken, err = exchangeIDTokenForRefresh(ctx, idToken)
	if err != nil {
		return "", "", 0, fmt.Errorf("exchange idToken: %w", err)
	}

	accessToken, expiresIn, err = refreshWarpToken(ctx, refreshToken)
	if err != nil {
		return "", "", 0, fmt.Errorf("refresh warp token: %w", err)
	}

	slog.Info("Anonymous Warp user created successfully")
	return accessToken, refreshToken, expiresIn, nil
}

func createAnonymousUser(ctx context.Context) (string, error) {
	query := `mutation CreateAnonymousUser($input: CreateAnonymousUserInput!, $requestContext: RequestContext!) {
  createAnonymousUser(input: $input, requestContext: $requestContext) {
    __typename
    ... on CreateAnonymousUserOutput {
      expiresAt
      anonymousUserType
      firebaseUid
      idToken
      isInviteValid
      responseContext { serverVersion }
    }
    ... on UserFacingError {
      error { __typename message }
    }
  }
}`
	variables := map[string]interface{}{
		"input": map[string]interface{}{
			"anonymousUserType": "NATIVE_CLIENT_ANONYMOUS_USER_FEATURE_GATED",
			"expirationType":    "NO_EXPIRATION",
			"referralCode":      nil,
		},
		"requestContext": defaultRequestContext(),
	}
	body := map[string]interface{}{
		"query":         query,
		"variables":     variables,
		"operationName": "CreateAnonymousUser",
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anonGraphQLURL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept-encoding", "gzip, br")
	req.Header.Set("x-warp-client-id", clientID)
	req.Header.Set("x-warp-client-version", clientVersion)
	req.Header.Set("x-warp-os-category", osCategory)
	req.Header.Set("x-warp-os-name", osName)
	req.Header.Set("x-warp-os-version", osVersion)
	req.Header.Set("user-agent", "")

	// #region agent log
	func() { f, e := os.OpenFile("debug-a666ec.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); if e != nil { return }; defer f.Close(); fmt.Fprintf(f, "{\"sessionId\":\"a666ec\",\"hypothesisId\":\"H14,H16\",\"location\":\"anonymous.go:createAnonymousUser\",\"message\":\"sending CreateAnonymousUser via UTLS\",\"data\":{\"url\":\"%s\",\"has_client_id\":true},\"timestamp\":%d}\n", anonGraphQLURL, time.Now().UnixMilli()) }()
	// #endregion

	resp, err := anonHTTPClient.Do(req)
	if err != nil {
		// #region agent log
		func() { f, e := os.OpenFile("debug-a666ec.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); if e != nil { return }; defer f.Close(); fmt.Fprintf(f, "{\"sessionId\":\"a666ec\",\"hypothesisId\":\"H14\",\"location\":\"anonymous.go:createAnonymousUser-err\",\"message\":\"UTLS request failed\",\"data\":{\"error\":\"%s\"},\"timestamp\":%d}\n", strings.ReplaceAll(err.Error(), "\"", "'"), time.Now().UnixMilli()) }()
		// #endregion
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	// #region agent log
	func() { f, e := os.OpenFile("debug-a666ec.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); if e != nil { return }; defer f.Close(); bs := strings.ReplaceAll(string(respBody), "\"", "'"); if len(bs) > 300 { bs = bs[:300] }; fmt.Fprintf(f, "{\"sessionId\":\"a666ec\",\"hypothesisId\":\"H14,H16\",\"location\":\"anonymous.go:createAnonymousUser-resp\",\"message\":\"CreateAnonymousUser response\",\"data\":{\"status\":%d,\"body\":\"%s\"},\"timestamp\":%d}\n", resp.StatusCode, bs, time.Now().UnixMilli()) }()
	// #endregion

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("CreateAnonymousUser HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	dataObj, _ := result["data"].(map[string]interface{})
	if dataObj == nil {
		return "", fmt.Errorf("missing data in response")
	}
	createAnon, _ := dataObj["createAnonymousUser"].(map[string]interface{})
	if createAnon == nil {
		return "", fmt.Errorf("missing createAnonymousUser in response")
	}

	if typeName, _ := createAnon["__typename"].(string); typeName == "UserFacingError" {
		errObj, _ := createAnon["error"].(map[string]interface{})
		msg := "unknown"
		if errObj != nil {
			if m, ok := errObj["message"].(string); ok {
				msg = m
			}
		}
		return "", fmt.Errorf("UserFacingError: %s", msg)
	}

	idToken, _ := createAnon["idToken"].(string)
	if idToken == "" {
		return "", fmt.Errorf("no idToken in response")
	}
	return idToken, nil
}

func exchangeIDTokenForRefresh(ctx context.Context, idToken string) (string, error) {
	url := identityBaseURL + "?key=" + googleAPIKey
	form := "returnSecureToken=true&token=" + idToken

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(form))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("x-warp-client-version", clientVersion)
	req.Header.Set("x-warp-os-category", osCategory)
	req.Header.Set("x-warp-os-name", osName)
	req.Header.Set("x-warp-os-version", osVersion)

	resp, err := anonHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// #region agent log
	func() { f, e := os.OpenFile("debug-a666ec.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); if e != nil { return }; defer f.Close(); fmt.Fprintf(f, "{\"sessionId\":\"a666ec\",\"hypothesisId\":\"H14\",\"location\":\"anonymous.go:exchangeIDToken-resp\",\"message\":\"exchangeIDToken response\",\"data\":{\"status\":%d},\"timestamp\":%d}\n", resp.StatusCode, time.Now().UnixMilli()) }()
	// #endregion

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("signInWithCustomToken HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	refreshToken, _ := result["refreshToken"].(string)
	if refreshToken == "" {
		return "", fmt.Errorf("no refreshToken in response")
	}
	return refreshToken, nil
}

func refreshWarpToken(ctx context.Context, refreshToken string) (string, int64, error) {
	form := "grant_type=refresh_token&refresh_token=" + refreshToken

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL, strings.NewReader(form))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("x-warp-client-version", clientVersion)
	req.Header.Set("x-warp-os-category", osCategory)
	req.Header.Set("x-warp-os-name", osName)
	req.Header.Set("x-warp-os-version", osVersion)
	req.Header.Set("accept", "*/*")

	resp, err := anonHTTPClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// #region agent log
	func() { f, e := os.OpenFile("debug-a666ec.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); if e != nil { return }; defer f.Close(); fmt.Fprintf(f, "{\"sessionId\":\"a666ec\",\"hypothesisId\":\"H14\",\"location\":\"anonymous.go:refreshWarpToken-resp\",\"message\":\"refreshWarpToken response\",\"data\":{\"status\":%d},\"timestamp\":%d}\n", resp.StatusCode, time.Now().UnixMilli()) }()
	// #endregion

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("refresh token HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed refreshResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", 0, err
	}

	token := parsed.AccessToken
	if token == "" {
		token = parsed.IDToken
	}
	if token == "" {
		return "", 0, fmt.Errorf("no access_token in response")
	}

	var expiresIn int64
	if v, err := parsed.ExpiresIn.Int64(); err == nil && v > 0 {
		expiresIn = v
	}
	if expiresIn <= 0 {
		if v, err := parsed.ExpiresInAlt.Int64(); err == nil && v > 0 {
			expiresIn = v
		}
	}
	if expiresIn <= 0 {
		expiresIn = 3600
	}

	return token, expiresIn, nil
}

func refreshAnonToken(ctx context.Context, refreshToken string) (string, string, int64, error) {
	token, expiresIn, err := refreshWarpToken(ctx, refreshToken)
	if err != nil {
		return "", "", 0, err
	}
	return token, refreshToken, expiresIn, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// InvalidateAnonymousToken clears the cached anonymous token so the next
// request creates a fresh anonymous user.
func InvalidateAnonymousToken() {
	globalAnonToken.mu.Lock()
	oldRefresh := globalAnonToken.refreshToken
	globalAnonToken.accessToken = ""
	globalAnonToken.refreshToken = ""
	globalAnonToken.expiresAt = time.Time{}
	globalAnonToken.mu.Unlock()

	if oldRefresh != "" {
		sessionCache.Delete(sessionKey(-1, oldRefresh))
	}
	sessionCache.Delete("warp:anon")
	slog.Info("Anonymous token invalidated, next request will create new anonymous user")
}

// AcquireAnonymousRefreshToken creates an anonymous Warp user and returns
// a refresh token that can be used with a standard warp session. The httpClient
// parameter is accepted for interface compatibility but not used (anonymous
// user creation uses the default HTTP client).
func AcquireAnonymousRefreshToken(ctx context.Context, _ *http.Client) (string, error) {
	globalAnonToken.mu.Lock()
	if globalAnonToken.refreshToken != "" {
		rt := globalAnonToken.refreshToken
		globalAnonToken.mu.Unlock()
		return rt, nil
	}
	globalAnonToken.mu.Unlock()

	slog.Info("Creating anonymous Warp user for refresh token")
	idToken, err := createAnonymousUser(ctx)
	if err != nil {
		return "", fmt.Errorf("create anonymous user: %w", err)
	}

	refreshToken, err := exchangeIDTokenForRefresh(ctx, idToken)
	if err != nil {
		return "", fmt.Errorf("exchange idToken: %w", err)
	}

	globalAnonToken.mu.Lock()
	globalAnonToken.refreshToken = refreshToken
	globalAnonToken.mu.Unlock()

	slog.Info("Anonymous Warp refresh token acquired")
	return refreshToken, nil
}

// IsBlockedError checks if an error message indicates the account is blocked
// from using AI features.
func IsBlockedError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "blocked from using AI features") ||
		strings.Contains(s, "please upgrade to a paid plan")
}
