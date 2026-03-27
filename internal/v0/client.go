package v0

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/prompt"
	"orchids-api/internal/store"
	"orchids-api/internal/upstream"
	"orchids-api/internal/util"
)

const (
	defaultScopedUserURL = "https://v0.app/api/chat/scoped/user"
	defaultPlanInfoURL   = "https://v0.app/chat/api/plan-info"
	defaultScopesURL     = "https://v0.app/chat/api/scopes"
	defaultRateLimitURL  = "https://v0.app/chat/api/rate-limit"
	defaultModelID       = "v0-max"
	defaultBridgeScript  = "scripts/v0_web_bridge.cjs"
	defaultModelScript   = "scripts/v0_model_bridge.cjs"
)

var (
	ScopedUserURLForTest = defaultScopedUserURL
	PlanInfoURLForTest   = defaultPlanInfoURL
	ScopesURLForTest     = defaultScopesURL
	RateLimitURLForTest  = defaultRateLimitURL
)

type Client struct {
	config           *config.Config
	account          *store.Account
	httpClient       *http.Client
	userSession      string
	sharedHTTPClient bool
}

type scopedUserResponse struct {
	OK    bool       `json:"ok"`
	Value ScopedUser `json:"value"`
}

type ScopedUser struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Username      string   `json:"username"`
	Email         string   `json:"email"`
	Plan          string   `json:"plan"`
	V0Plan        string   `json:"v0plan"`
	RealV0Plan    string   `json:"realv0Plan"`
	Teams         []string `json:"teams"`
	TeamID        string   `json:"teamId"`
	TeamName      string   `json:"teamName"`
	Scope         string   `json:"scope"`
	DefaultTeamID string   `json:"defaultTeamId"`
	BillingStart  int64    `json:"billingCycleStart"`
	BillingEnd    int64    `json:"billingCycleEnd"`
}

type PlanInfo struct {
	Plan         string      `json:"plan"`
	RealPlan     string      `json:"realPlan"`
	Role         string      `json:"role"`
	BillingCycle BillingSpan `json:"billingCycle"`
	Balance      V0Balance   `json:"balance"`
	OnDemand     V0Balance   `json:"onDemand"`
}

type BillingSpan struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type V0Balance struct {
	Remaining float64 `json:"remaining"`
	Total     float64 `json:"total"`
}

type ScopeInfo struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type RateLimitInfo struct {
	Remaining float64 `json:"remaining"`
	Reset     int64   `json:"reset"`
	Limit     float64 `json:"limit"`
}

type bridgeRequest struct {
	UserSession string `json:"userSession"`
	Prompt      string `json:"prompt"`
	Model       string `json:"model"`
	ChatID      string `json:"chatId,omitempty"`
	TimeoutMs   int    `json:"timeoutMs"`
}

type bridgeResponse struct {
	OK       bool   `json:"ok"`
	ChatID   string `json:"chatId"`
	Model    string `json:"model"`
	Response string `json:"response"`
	Error    string `json:"error"`
}

type ModelChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type modelBridgeRequest struct {
	UserSession string `json:"userSession"`
	TimeoutMs   int    `json:"timeoutMs"`
}

type modelBridgeResponse struct {
	OK     bool          `json:"ok"`
	Models []ModelChoice `json:"models"`
	Error  string        `json:"error"`
}

func NewFromAccount(acc *store.Account, cfg *config.Config) *Client {
	timeout := 5 * time.Minute
	if cfg != nil && cfg.RequestTimeout > 0 {
		timeout = time.Duration(cfg.RequestTimeout) * time.Second
		if timeout < 30*time.Second {
			timeout = 30 * time.Second
		}
	}

	proxyFunc := http.ProxyFromEnvironment
	proxyKey := "direct"
	if cfg != nil {
		proxyFunc = util.ProxyFuncFromConfig(cfg)
		proxyKey = util.GenerateProxyKeyFromConfig(cfg)
	}

	return &Client{
		config:           cfg,
		account:          acc,
		httpClient:       util.GetSharedHTTPClient(proxyKey, timeout, proxyFunc),
		userSession:      resolveUserSession(acc),
		sharedHTTPClient: true,
	}
}

func resolveUserSession(acc *store.Account) string {
	if acc == nil {
		return ""
	}
	for _, value := range []string{acc.ClientCookie, acc.Token, acc.SessionCookie} {
		if token := extractUserSession(value); token != "" {
			return token
		}
	}
	return ""
}

func extractUserSession(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(strings.ToLower(trimmed), "user_session=") {
		for _, part := range strings.Split(trimmed, ";") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(strings.ToLower(part), "user_session=") {
				return strings.TrimSpace(strings.TrimPrefix(part, "user_session="))
			}
		}
	}
	return strings.Trim(strings.TrimSpace(trimmed), "\"'")
}

func (c *Client) Close() {
	if c == nil || c.sharedHTTPClient || c.httpClient == nil || c.httpClient.Transport == nil {
		return
	}
	if closer, ok := c.httpClient.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (c *Client) VerifyAuthToken(ctx context.Context) error {
	_, err := c.FetchScopedUser(ctx)
	return err
}

func (c *Client) FetchScopedUser(ctx context.Context) (*ScopedUser, error) {
	if c == nil {
		return nil, fmt.Errorf("v0 client is nil")
	}
	if strings.TrimSpace(c.userSession) == "" {
		return nil, fmt.Errorf("missing v0 user_session")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ScopedUserURLForTest, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build v0 scoped user request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", "user_session="+c.userSession)
	req.Header.Set("User-Agent", "Orchids-2api/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch v0 scoped user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("v0 scoped user error: status=%d, body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed scopedUserResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("failed to decode v0 scoped user response: %w", err)
	}
	if !parsed.OK {
		return nil, fmt.Errorf("v0 scoped user request was not accepted")
	}
	return &parsed.Value, nil
}

func (c *Client) FetchPlanInfo(ctx context.Context) (*PlanInfo, error) {
	if c == nil {
		return nil, fmt.Errorf("v0 client is nil")
	}
	if strings.TrimSpace(c.userSession) == "" {
		return nil, fmt.Errorf("missing v0 user_session")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, PlanInfoURLForTest, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build v0 plan info request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", "user_session="+c.userSession)
	req.Header.Set("User-Agent", "Orchids-2api/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch v0 plan info: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("v0 plan info error: status=%d, body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var info PlanInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to decode v0 plan info response: %w", err)
	}
	return &info, nil
}

func (c *Client) FetchScopes(ctx context.Context) ([]ScopeInfo, error) {
	if c == nil {
		return nil, fmt.Errorf("v0 client is nil")
	}
	if strings.TrimSpace(c.userSession) == "" {
		return nil, fmt.Errorf("missing v0 user_session")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ScopesURLForTest, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build v0 scopes request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", "user_session="+c.userSession)
	req.Header.Set("User-Agent", "Orchids-2api/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch v0 scopes: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("v0 scopes error: status=%d, body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var scopes []ScopeInfo
	if err := json.NewDecoder(resp.Body).Decode(&scopes); err != nil {
		return nil, fmt.Errorf("failed to decode v0 scopes response: %w", err)
	}
	return scopes, nil
}

func (c *Client) FetchRateLimit(ctx context.Context, scopeSlug string) (*RateLimitInfo, error) {
	if c == nil {
		return nil, fmt.Errorf("v0 client is nil")
	}
	if strings.TrimSpace(c.userSession) == "" {
		return nil, fmt.Errorf("missing v0 user_session")
	}
	scopeSlug = strings.TrimSpace(scopeSlug)
	if scopeSlug == "" {
		return nil, fmt.Errorf("missing v0 scope slug")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, RateLimitURLForTest+"?scope="+scopeSlug, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build v0 rate limit request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", "user_session="+c.userSession)
	req.Header.Set("User-Agent", "Orchids-2api/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch v0 rate limit: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("v0 rate limit error: status=%d, body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var info RateLimitInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to decode v0 rate limit response: %w", err)
	}
	return &info, nil
}

func (c *Client) SendRequest(ctx context.Context, _ string, _ []interface{}, model string, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	return c.SendRequestWithPayload(ctx, upstream.UpstreamRequest{Model: model}, onMessage, logger)
}

func (c *Client) SendRequestWithPayload(ctx context.Context, req upstream.UpstreamRequest, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	if c == nil {
		return fmt.Errorf("v0 client is nil")
	}
	if strings.TrimSpace(c.userSession) == "" {
		return fmt.Errorf("missing v0 user_session")
	}

	timeoutMs := 180000
	if c.config != nil && c.config.RequestTimeout > 0 {
		timeoutMs = c.config.RequestTimeout * 1000
		if timeoutMs < 30000 {
			timeoutMs = 30000
		}
	}

	payload := bridgeRequest{
		UserSession: c.userSession,
		Prompt:      c.buildPrompt(req),
		Model:       normalizeWebModel(req.Model),
		ChatID:      normalizeChatID(req.ChatSessionID),
		TimeoutMs:   timeoutMs,
	}
	if payload.Prompt == "" {
		payload.Prompt = "hello"
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal v0 bridge request: %w", err)
	}
	if logger != nil {
		logger.LogUpstreamRequest("node:"+defaultBridgeScript, map[string]string{"provider": "v0"}, body)
	}

	bridgeOut, bridgeErr, err := c.runScript(ctx, defaultBridgeScript, body)
	if err != nil {
		return err
	}

	var resp bridgeResponse
	if err := json.Unmarshal(bridgeOut, &resp); err != nil {
		return fmt.Errorf("failed to decode v0 bridge response: %w; stderr=%s; stdout=%s", err, strings.TrimSpace(string(bridgeErr)), strings.TrimSpace(string(bridgeOut)))
	}
	if !resp.OK {
		if strings.TrimSpace(resp.Error) != "" {
			return fmt.Errorf("v0 bridge error: %s", strings.TrimSpace(resp.Error))
		}
		return fmt.Errorf("v0 bridge request failed")
	}

	if onMessage != nil {
		if chatID := strings.TrimSpace(resp.ChatID); chatID != "" {
			onMessage(upstream.SSEMessage{
				Type:  "model.conversation_id",
				Event: map[string]interface{}{"id": chatID},
			})
		}
		if text := strings.TrimSpace(resp.Response); text != "" {
			onMessage(upstream.SSEMessage{
				Type: "model.text-delta",
				Event: map[string]interface{}{
					"delta": resp.Response,
				},
			})
		}
		onMessage(upstream.SSEMessage{
			Type: "model.finish",
			Event: map[string]interface{}{
				"finishReason": "end_turn",
				"usage": map[string]int{
					"inputTokens":   estimateTokens(payload.Prompt),
					"outputTokens":  estimateTokens(resp.Response),
					"input_tokens":  estimateTokens(payload.Prompt),
					"output_tokens": estimateTokens(resp.Response),
				},
				"model": firstNonEmpty(resp.Model, payload.Model, defaultModelID),
			},
		})
	}
	return nil
}

func (c *Client) FetchDiscoveredModelChoices(ctx context.Context) ([]ModelChoice, string, error) {
	if c == nil {
		return nil, "", fmt.Errorf("v0 client is nil")
	}
	if strings.TrimSpace(c.userSession) == "" {
		return nil, "", fmt.Errorf("missing v0 user_session")
	}

	timeoutMs := 60000
	if c.config != nil && c.config.RequestTimeout > 0 {
		timeoutMs = c.config.RequestTimeout * 1000
		if timeoutMs < 30000 {
			timeoutMs = 30000
		}
	}

	body, err := json.Marshal(modelBridgeRequest{
		UserSession: c.userSession,
		TimeoutMs:   timeoutMs,
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal v0 model bridge request: %w", err)
	}

	stdout, stderr, err := c.runScript(ctx, defaultModelScript, body)
	if err == nil {
		var resp modelBridgeResponse
		if decodeErr := json.Unmarshal(stdout, &resp); decodeErr != nil {
			err = fmt.Errorf("failed to decode v0 model bridge response: %w; stderr=%s; stdout=%s", decodeErr, strings.TrimSpace(string(stderr)), strings.TrimSpace(string(stdout)))
		} else if !resp.OK {
			err = fmt.Errorf("v0 model bridge error: %s", strings.TrimSpace(resp.Error))
		} else if normalized := normalizeDiscoveredModelChoices(resp.Models); len(normalized) > 0 {
			return normalized, "v0_web_model_picker", nil
		}
	}

	if _, scopedErr := c.FetchScopedUser(ctx); scopedErr != nil {
		if err != nil {
			return nil, "", err
		}
		return nil, "", scopedErr
	}

	return buildSeedModelChoices(), "v0_seed_fallback", nil
}

func normalizeDiscoveredModelChoices(items []ModelChoice) []ModelChoice {
	seen := make(map[string]struct{}, len(items))
	out := make([]ModelChoice, 0, len(items))
	for _, item := range items {
		id := normalizeWebModel(item.ID)
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = prettifyV0ModelName(id)
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, ModelChoice{ID: id, Name: name})
	}
	return out
}

func prettifyV0ModelName(modelID string) string {
	switch strings.ToLower(strings.TrimSpace(modelID)) {
	case "v0-auto":
		return "v0 Auto"
	case "v0-max":
		return "v0 Max"
	case "v0-mini":
		return "v0 Mini"
	case "v0-pro":
		return "v0 Pro"
	case "v0-max-fast":
		return "v0 Max Fast"
	default:
		return strings.TrimSpace(modelID)
	}
}

func buildSeedModelChoices() []ModelChoice {
	items := store.BuildV0SeedModels()
	out := make([]ModelChoice, 0, len(items))
	for _, item := range items {
		id := normalizeWebModel(item.ModelID)
		if id == "" {
			continue
		}
		out = append(out, ModelChoice{
			ID:   id,
			Name: firstNonEmpty(item.Name, prettifyV0ModelName(id), id),
		})
	}
	return out
}

func (c *Client) runScript(ctx context.Context, script string, body []byte) ([]byte, []byte, error) {
	scriptPath, err := filepath.Abs(script)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve v0 bridge path: %w", err)
	}

	nodeCmd := "node"
	if runtime.GOOS == "windows" {
		nodeCmd = "node.exe"
	}

	cmd := exec.CommandContext(ctx, nodeCmd, scriptPath)
	cmd.Stdin = bytes.NewReader(body)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("v0 bridge execution failed: %w; stderr=%s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

func (c *Client) buildPrompt(req upstream.UpstreamRequest) string {
	if normalizeChatID(req.ChatSessionID) != "" {
		if latest := strings.TrimSpace(extractLatestUserMessageText(req.Messages)); latest != "" {
			return latest
		}
	}
	return flattenConversation(req.System, req.Messages)
}

func flattenConversation(system []prompt.SystemItem, messages []prompt.Message) string {
	parts := make([]string, 0, len(system)+len(messages))
	if sys := buildSystemText(system); sys != "" {
		parts = append(parts, "[system]\n"+sys)
	}
	for _, msg := range messages {
		if rendered := renderMessageForBridge(msg); rendered != "" {
			role := strings.TrimSpace(msg.Role)
			if role == "" {
				role = "user"
			}
			parts = append(parts, "["+role+"]\n"+rendered)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func buildSystemText(system []prompt.SystemItem) string {
	parts := make([]string, 0, len(system))
	for _, item := range system {
		if text := strings.TrimSpace(item.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func renderMessageForBridge(msg prompt.Message) string {
	if msg.Content.IsString() {
		return strings.TrimSpace(msg.Content.GetText())
	}

	blocks := msg.Content.GetBlocks()
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				parts = append(parts, text)
			}
		case "tool_use":
			raw, _ := json.Marshal(block.Input)
			if len(raw) == 0 || string(raw) == "null" {
				raw = []byte("{}")
			}
			parts = append(parts, fmt.Sprintf("[tool_use:%s] %s", firstNonEmpty(block.Name, block.ID, "tool"), string(raw)))
		case "tool_result":
			parts = append(parts, fmt.Sprintf("[tool_result:%s] %s", firstNonEmpty(block.ToolUseID, block.Name, "tool"), stringifyToolResult(block.Content)))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func stringifyToolResult(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case []prompt.ContentBlock:
		out := make([]string, 0, len(x))
		for _, block := range x {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				out = append(out, strings.TrimSpace(block.Text))
			}
		}
		return strings.Join(out, "\n")
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			return ""
		}
		return string(raw)
	}
}

func extractLatestUserMessageText(messages []prompt.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			continue
		}
		if rendered := strings.TrimSpace(renderMessageForBridge(msg)); rendered != "" {
			return rendered
		}
	}
	return ""
}

func normalizeWebModel(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "", "v0", "v0-max", "v0 max", "v0-1.5-md", "v0-1.5-lg", "v0-1.0-md":
		return defaultModelID
	case "v0-auto", "v0 auto":
		return "v0-auto"
	case "v0-mini", "v0 mini":
		return "v0-mini"
	case "v0-pro", "v0 pro":
		return "v0-pro"
	case "v0-max-fast", "v0 max fast":
		return "v0-max-fast"
	default:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "v0-") {
			return strings.ToLower(strings.TrimSpace(model))
		}
		return defaultModelID
	}
}

func normalizeChatID(chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" || strings.HasPrefix(chatID, "chat_") {
		return ""
	}
	return chatID
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return len(text) / 4
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
