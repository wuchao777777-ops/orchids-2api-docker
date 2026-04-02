package bolt

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/prompt"
	"orchids-api/internal/store"
	"orchids-api/internal/tiktoken"
	"orchids-api/internal/upstream"
	"orchids-api/internal/util"
)

const (
	defaultAPIURL           = "https://bolt.new/api/chat/v2"
	defaultRootDataURL      = "https://bolt.new/?_data=root"
	defaultRateLimitsURL    = "https://bolt.new/api/rate-limits/user"
	defaultTeamsRateURL     = "https://bolt.new/api/rate-limits/teams"
	defaultProjectsURL      = "https://stackblitz.com/api/projects/sb1/fork"
	maxBoltFocusedFileCount = 2
	maxBoltReadResultRunes  = 480
	maxBoltFocusedReadRunes = 2200
	maxBoltShellResultRunes = 480
)

var boltAPIURL = defaultAPIURL
var boltRootDataURL = defaultRootDataURL
var boltRateLimitsURL = defaultRateLimitsURL
var boltTeamsRateLimitsURL = defaultTeamsRateURL
var boltProjectsCreateURL = defaultProjectsURL
var boltStripTaggedNames = []string{
	"local-command-caveat",
	"command-name",
	"command-message",
	"command-args",
	"local-command-stdout",
	"local-command-stderr",
	"local-command-exit-code",
	"ide_opened_file",
	"ide_selection",
}
var boltEmptyParts = make([]Part, 0)
var boltEmptyInterfaces = make([]interface{}, 0)

type InputTokenEstimate struct {
	BasePromptTokens    int
	SystemContextTokens int
	HistoryTokens       int
	ToolsTokens         int
	Total               int
}

type builtMessages struct {
	Items         []Message
	HistoryTokens int
}

type systemPromptParts struct {
	BasePrompt   string
	ToolPrompt   string
	SystemPrompt string
	FullPrompt   string
}

type Client struct {
	httpClient       *http.Client
	sessionToken     string
	projectID        string
	authToken        string
	sharedHTTPClient bool
}

func NewFromAccount(acc *store.Account, cfg *config.Config) *Client {
	timeout := 30 * time.Second
	if cfg != nil && cfg.RequestTimeout > 0 {
		timeout = time.Duration(cfg.RequestTimeout) * time.Second
	}

	proxyFunc := http.ProxyFromEnvironment
	proxyKey := "direct"
	if cfg != nil {
		proxyFunc = util.ProxyFuncFromConfig(cfg)
		proxyKey = util.GenerateProxyKeyFromConfig(cfg)
	}

	sessionToken := ""
	projectID := ""
	authToken := ""
	if acc != nil {
		sessionToken = strings.TrimSpace(acc.SessionCookie)
		if sessionToken == "" {
			sessionToken = strings.TrimSpace(acc.ClientCookie)
		}
		projectID = strings.TrimSpace(acc.ProjectID)
		authToken = strings.TrimSpace(acc.Token)
	}

	return &Client{
		httpClient:       util.GetSharedHTTPClient(proxyKey, timeout, proxyFunc),
		sessionToken:     sessionToken,
		projectID:        projectID,
		authToken:        authToken,
		sharedHTTPClient: true,
	}
}

func (c *Client) Close() {
	if c == nil || c.sharedHTTPClient || c.httpClient == nil || c.httpClient.Transport == nil {
		return
	}
	if closer, ok := c.httpClient.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (c *Client) FetchRootData(ctx context.Context) (*RootData, error) {
	if c == nil {
		return nil, fmt.Errorf("bolt client is nil")
	}
	if strings.TrimSpace(c.sessionToken) == "" {
		return nil, fmt.Errorf("missing bolt session token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, boltRootDataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create bolt root request: %w", err)
	}
	c.applyCommonHeaders(req)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch bolt root data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("bolt root data error: status=%d, body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var data RootData
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode bolt root data: %w", err)
	}
	return &data, nil
}

func (c *Client) FetchRateLimits(ctx context.Context, organizationID int64) (*RateLimits, error) {
	if c == nil {
		return nil, fmt.Errorf("bolt client is nil")
	}
	if strings.TrimSpace(c.sessionToken) == "" {
		return nil, fmt.Errorf("missing bolt session token")
	}

	targetURL := boltRateLimitsURL
	if organizationID > 0 {
		targetURL = strings.TrimRight(boltTeamsRateLimitsURL, "/") + "/" + strconv.FormatInt(organizationID, 10)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create bolt rate-limit request: %w", err)
	}
	c.applyCommonHeaders(req)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch bolt rate limits: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("bolt rate-limit error: status=%d, body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var data RateLimits
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode bolt rate limits: %w", err)
	}
	return &data, nil
}

func (c *Client) CreateEmptyProject(ctx context.Context) (string, error) {
	if c == nil {
		return "", fmt.Errorf("bolt client is nil")
	}

	token, err := c.projectAuthToken(ctx, false)
	if err != nil {
		return "", err
	}

	projectID, statusCode, err := c.createEmptyProjectWithToken(ctx, token)
	if err == nil {
		return projectID, nil
	}
	if statusCode != http.StatusUnauthorized {
		return "", err
	}

	token, err = c.projectAuthToken(ctx, true)
	if err != nil {
		return "", err
	}
	projectID, _, err = c.createEmptyProjectWithToken(ctx, token)
	return projectID, err
}

func (c *Client) createEmptyProjectWithToken(ctx context.Context, token string) (string, int, error) {
	if c == nil {
		return "", 0, fmt.Errorf("bolt client is nil")
	}

	payload := map[string]any{
		"project": map[string]any{
			"appFiles": map[string]any{},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", 0, fmt.Errorf("failed to marshal bolt project create payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, boltProjectsCreateURL, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create bolt project request: %w", err)
	}
	c.applyCommonHeaders(req)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create bolt project: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return "", resp.StatusCode, fmt.Errorf("bolt project create error: status=%d, body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var created struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return "", resp.StatusCode, fmt.Errorf("failed to decode bolt project create response: %w", err)
	}
	if strings.TrimSpace(created.Slug) == "" {
		return "", resp.StatusCode, fmt.Errorf("bolt project create response missing slug")
	}
	return strings.TrimSpace(created.Slug), resp.StatusCode, nil
}

func (c *Client) projectAuthToken(ctx context.Context, forceRefresh bool) (string, error) {
	if c == nil {
		return "", fmt.Errorf("bolt client is nil")
	}
	if !forceRefresh {
		if token := strings.TrimSpace(c.authToken); token != "" {
			return token, nil
		}
	}

	rootData, err := c.FetchRootData(ctx)
	if err != nil {
		if forceRefresh {
			return "", fmt.Errorf("failed to refresh bolt root data for project creation: %w", err)
		}
		return "", fmt.Errorf("failed to fetch bolt root data for project creation: %w", err)
	}
	if rootData == nil {
		return "", fmt.Errorf("bolt root data missing project creation token")
	}
	token := strings.TrimSpace(rootData.Token)
	if token == "" {
		return "", fmt.Errorf("bolt root data missing project creation token")
	}
	c.authToken = token
	return token, nil
}

func (c *Client) SendRequest(ctx context.Context, _ string, _ []interface{}, model string, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	req := upstream.UpstreamRequest{Model: model}
	return c.SendRequestWithPayload(ctx, req, onMessage, logger)
}

func (c *Client) SendRequestWithPayload(ctx context.Context, req upstream.UpstreamRequest, onMessage func(upstream.SSEMessage), logger *debug.Logger) error {
	if c == nil {
		return fmt.Errorf("bolt client is nil")
	}
	if strings.TrimSpace(c.sessionToken) == "" {
		return fmt.Errorf("missing bolt session token")
	}
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID == "" {
		projectID = strings.TrimSpace(c.projectID)
	}
	if projectID == "" {
		return fmt.Errorf("missing bolt project id")
	}
	boltReq, inputEstimate := prepareRequest(req, projectID)
	body, err := json.Marshal(boltReq)
	if err != nil {
		return fmt.Errorf("failed to marshal bolt request: %w", err)
	}
	if logger != nil {
		logger.LogUpstreamRequest(boltAPIURL, map[string]string{"provider": "bolt"}, body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, boltAPIURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create bolt request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Referer", "https://bolt.new/~/sb1-"+projectID)
	c.applyCommonHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send bolt request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("bolt API error: status=%d, body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	converter := newOutboundConverter(req.Model, inputEstimate.Total)
	return converter.ProcessStream(resp.Body, logger, func(msg upstream.SSEMessage) error {
		if onMessage != nil {
			onMessage(msg)
		}
		return nil
	})
}

func (c *Client) applyCommonHeaders(req *http.Request) {
	if req == nil {
		return
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Origin", "https://bolt.new")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Cookie", "__session="+c.sessionToken)
}

func EstimateInputTokens(req upstream.UpstreamRequest) InputTokenEstimate {
	_, estimate := prepareRequest(req, strings.TrimSpace(req.ProjectID))
	return estimate
}

func prepareRequest(req upstream.UpstreamRequest, projectID string) (*Request, InputTokenEstimate) {
	req.Messages = compactMessages(req.Messages, req.Workdir)
	promptParts := buildSystemPromptParts(req.System, req.Workdir, req.Tools, req.NoTools, req.Messages)
	messages := buildBoltMessages(req.Messages, req.Workdir)

	boltReq := &Request{
		ID:                   generateRandomID(16),
		SelectedModel:        strings.TrimSpace(req.Model),
		IsFirstPrompt:        req.IsFirstPrompt,
		PromptMode:           "build",
		EffortLevel:          "low",
		ProjectID:            projectID,
		GlobalSystemPrompt:   promptParts.FullPrompt,
		ProjectPrompt:        "",
		StripeStatus:         "not-configured",
		HostingProvider:      "bolt",
		SupportIntegrations:  true,
		UsesInspectedElement: false,
		ErrorReasoning:       nil,
		FeaturePreviews: FeaturePreviews{
			Reasoning: false,
			Diffs:     false,
		},
		ProjectFiles: ProjectFiles{
			Visible: boltEmptyInterfaces,
			Hidden:  boltEmptyInterfaces,
		},
		RunningCommands: boltEmptyInterfaces,
		Dependencies:    boltEmptyInterfaces,
		Problems:        "",
		Messages:        messages.Items,
	}
	return boltReq, estimatePreparedRequestInput(promptParts, messages.HistoryTokens)
}

func buildBoltMessages(messages []prompt.Message, workdir string) builtMessages {
	built := builtMessages{
		Items: make([]Message, 0, len(messages)),
	}
	toolUses := collectBoltToolUseMetadata(messages, workdir)
	gitUploadIntent := detectRecentBoltGitUploadIntent(messages)
	focusedFileAliases := collectFocusedBoltFileAliases(messages, workdir)
	var lastUserMsgID string
	var activeStandaloneUserTask string
	for _, msg := range messages {
		blocks := normalizeBlocks(msg)
		if shouldSkipBoltMessage(msg.Role) {
			continue
		}

		boltMsg := Message{
			ID:    generateRandomID(16),
			Role:  msg.Role,
			Cache: hasEphemeralCache(blocks),
			Parts: boltEmptyParts,
		}

		switch msg.Role {
		case "user":
			if standaloneUserText := extractBoltStandaloneUserText(msg); standaloneUserText != "" && !LooksLikeContinuationOnlyText(standaloneUserText) {
				activeStandaloneUserTask = standaloneUserText
			}
			lastUserMsgID = boltMsg.ID
			boltMsg.Content = extractBoltUserContent(blocks, toolUses, gitUploadIntent, focusedFileAliases, workdir, activeStandaloneUserTask)
			boltMsg.RawContent = boltMsg.Content
		case "assistant":
			if lastUserMsgID != "" {
				boltMsg.Annotations = []Annotation{{Type: "metadata", UserMessageID: lastUserMsgID}}
			}
			boltMsg.Content = extractBoltAssistantHistoryContent(blocks)
			if strings.TrimSpace(boltMsg.Content) != "" {
				boltMsg.Parts = []Part{{Type: "text", Text: boltMsg.Content}}
			}
		default:
			boltMsg.Content = extractTextContent(blocks)
			boltMsg.RawContent = boltMsg.Content
		}
		if strings.TrimSpace(boltMsg.Content) == "" && len(boltMsg.Parts) == 0 {
			continue
		}
		built.Items = append(built.Items, boltMsg)
		built.HistoryTokens += estimateBoltMessageTokens(boltMsg)
	}
	return built
}

func collectFocusedBoltFileAliases(messages []prompt.Message, workdir string) map[string]struct{} {
	if len(messages) == 0 {
		return nil
	}

	focused := make(map[string]struct{})
	seenFiles := make(map[string]struct{})
	fileCount := 0

	addAliases := func(path string) {
		aliases := buildBoltPathAliases(path)
		if len(aliases) == 0 {
			return
		}

		canonical := strings.ToLower(strings.ReplaceAll(strings.Trim(strings.TrimSpace(path), "\"'`"), "\\", "/"))
		if canonical == "" {
			return
		}
		if _, ok := seenFiles[canonical]; ok {
			for alias := range aliases {
				focused[alias] = struct{}{}
			}
			return
		}
		if fileCount >= maxBoltFocusedFileCount {
			return
		}
		seenFiles[canonical] = struct{}{}
		fileCount++
		for alias := range aliases {
			focused[alias] = struct{}{}
		}
	}

	for i := len(messages) - 1; i >= 0 && fileCount < maxBoltFocusedFileCount; i-- {
		for _, block := range normalizeBlocks(messages[i]) {
			if block.Type != "tool_use" {
				continue
			}
			switch strings.TrimSpace(block.Name) {
			case "Read", "Write", "Edit":
			default:
				continue
			}
			path, invalid := normalizeBoltToolPath(extractBoltToolPath(block.Input), workdir)
			if path == "" || invalid {
				continue
			}
			addAliases(path)
		}
	}

	if len(focused) == 0 {
		return nil
	}
	return focused
}

func estimatePreparedRequestInput(parts systemPromptParts, historyTokens int) InputTokenEstimate {
	estimate := InputTokenEstimate{
		BasePromptTokens:    estimateBoltTextTokens(parts.BasePrompt),
		SystemContextTokens: estimateBoltTextTokens(parts.SystemPrompt),
		ToolsTokens:         estimateBoltTextTokens(parts.ToolPrompt),
		HistoryTokens:       historyTokens,
	}
	estimate.Total = estimate.BasePromptTokens + estimate.SystemContextTokens + estimate.ToolsTokens + estimate.HistoryTokens
	return estimate
}

func estimateBoltTextTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	return tiktoken.EstimateTextTokens(text)
}

func estimateBoltMessageTokens(msg Message) int {
	text := strings.TrimSpace(msg.Content)
	if text == "" {
		return 0
	}

	overhead := 15
	if len(msg.Annotations) > 0 {
		overhead += 5 * len(msg.Annotations)
	}
	return tiktoken.EstimateTextTokens(text) + overhead
}

func detectRecentBoltGitUploadIntent(messages []prompt.Message) bool {
	const maxScan = 6
	seen := 0
	for i := len(messages) - 1; i >= 0 && seen < maxScan; i-- {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			continue
		}
		seen++
		for _, block := range normalizeBlocks(msg) {
			if block.Type != "text" {
				continue
			}
			if textIndicatesBoltGitUpload(block.Text) {
				return true
			}
		}
	}
	return false
}

func textIndicatesBoltGitUpload(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"上传到 git", "上传到git", "推送到 git", "推送到git",
		"提交并推送", "提交到 git", "提交到git",
		"git push", "commit and push", "push to git", "upload to git", "push code",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func hasEphemeralCache(blocks []prompt.ContentBlock) bool {
	for _, block := range blocks {
		if block.CacheControl != nil {
			return true
		}
	}
	return false
}

func extractTextContent(blocks []prompt.ContentBlock) string {
	var sb strings.Builder
	first := true
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		if text := strings.TrimSpace(block.Text); text != "" {
			if !first {
				sb.WriteByte('\n')
			}
			sb.WriteString(text)
			first = false
		}
	}
	return sb.String()
}

func extractBoltAssistantHistoryContent(blocks []prompt.ContentBlock) string {
	var sb strings.Builder
	first := true
	hasToolUse := false
	for _, block := range blocks {
		switch block.Type {
		case "tool_use":
			hasToolUse = true
		case "text":
			if text := sanitizeBoltAssistantText(block.Text); text != "" {
				if !first {
					sb.WriteByte('\n')
				}
				sb.WriteString(text)
				first = false
			}
		}
	}
	if hasToolUse {
		return ""
	}
	return sb.String()
}

type boltUserContentPart struct {
	Text   string
	Result *boltSerializedToolResult
}

type boltSerializedToolResult struct {
	ToolName    string
	ToolPath    string
	Text        string
	InvalidPath bool
	IsError     bool
	IsSuccess   bool
	IsMutation  bool
	Aliases     map[string]struct{}
	Drop        bool
}

func extractBoltUserContent(blocks []prompt.ContentBlock, toolUses map[string]boltToolUseMetadata, gitUploadIntent bool, focusedFileAliases map[string]struct{}, workdir string, activeStandaloneUserTask string) string {
	parts := make([]boltUserContentPart, 0, len(blocks))
	results := make([]*boltSerializedToolResult, 0, len(blocks))
	hasUserText := false
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if text := sanitizeBoltMessageText(block.Text); text != "" {
				hasUserText = true
				parts = append(parts, boltUserContentPart{Text: text})
			}
		case "tool_result":
			rawText := sanitizeBoltToolResultText(stringifyContent(block.Content))
			if rawText == "" {
				continue
			}
			toolMeta := toolUses[strings.TrimSpace(block.ToolUseID)]
			rawText = relativizeBoltWorkdirPaths(rawText, workdir)
			text := compactBoltToolResultText(toolMeta, rawText, focusedFileAliases)
			result := &boltSerializedToolResult{
				ToolName:    strings.TrimSpace(toolMeta.Name),
				ToolPath:    strings.TrimSpace(toolMeta.Path),
				Text:        text,
				InvalidPath: toolMeta.InvalidPath || looksLikeInvalidBoltPath(rawText),
				IsError:     isBoltToolResultError(block, rawText),
				IsSuccess:   isBoltToolResultSuccess(toolMeta.Name, rawText),
				IsMutation:  isBoltMutationTool(toolMeta.Name),
				Aliases:     toolMeta.Aliases,
			}
			results = append(results, result)
			parts = append(parts, boltUserContentPart{
				Text:   text,
				Result: result,
			})
		}
	}

	for i := 0; i < len(results); i++ {
		current := results[i]
		if !current.IsMutation && !current.IsError && len(current.Aliases) > 0 {
			for j := i + 1; j < len(results); j++ {
				next := results[j]
				if !next.IsSuccess || !next.IsMutation {
					continue
				}
				if sharesBoltPathAlias(current.Aliases, next.Aliases) {
					current.Drop = true
					break
				}
			}
		}
		if current.Drop {
			continue
		}
		if !current.IsError || !current.IsMutation || len(current.Aliases) == 0 {
			continue
		}
		for j := i + 1; j < len(results); j++ {
			next := results[j]
			if !next.IsSuccess || !next.IsMutation {
				continue
			}
			if sharesBoltPathAlias(current.Aliases, next.Aliases) {
				current.Drop = true
				break
			}
		}
	}

	hasVisibleToolResult := false
	var firstVisibleToolResult *boltSerializedToolResult
	for _, part := range parts {
		if part.Result == nil {
			continue
		}
		if part.Result.InvalidPath || part.Result.Drop || strings.TrimSpace(part.Text) == "" {
			continue
		}
		hasVisibleToolResult = true
		if firstVisibleToolResult == nil {
			firstVisibleToolResult = part.Result
		}
		break
	}
	hasFailedMutation := hasVisibleFailedMutationResult(results)

	var sb strings.Builder
	first := true
	if hasVisibleToolResult && !hasUserText {
		sb.WriteString(formatBoltToolResultContinuation(
			gitUploadIntent,
			hasFailedMutation,
			firstVisibleToolResult,
			activeStandaloneUserTask,
		))
		first = false
	}
	for _, part := range parts {
		if part.Result != nil && (part.Result.InvalidPath || part.Result.Drop) {
			continue
		}
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		if !first {
			sb.WriteString("\n\n")
		}
		sb.WriteString(part.Text)
		first = false
	}
	return sb.String()
}

func hasVisibleFailedMutationResult(results []*boltSerializedToolResult) bool {
	for _, result := range results {
		if result == nil || result.Drop || result.InvalidPath {
			continue
		}
		if result.IsMutation && result.IsError {
			return true
		}
	}
	return false
}

func formatBoltToolResultContinuation(_ bool, mutationFailed bool, result *boltSerializedToolResult, activeStandaloneUserTask string) string {
	var lines []string
	if mutationFailed {
		lines = append(lines, "[工具结果 - 上次修改未成功]")
	} else {
		lines = append(lines, "[工具结果]")
	}
	if result != nil {
		toolName := strings.TrimSpace(result.ToolName)
		toolPath := strings.TrimSpace(result.ToolPath)
		if toolName != "" {
			if toolPath != "" {
				lines = append(lines, toolName+"("+toolPath+")")
			} else {
				lines = append(lines, toolName)
			}
		}
	}
	if task := sanitizeBoltMessageText(activeStandaloneUserTask); task != "" && !LooksLikeContinuationOnlyText(task) {
		lines = append(lines, "当前任务: "+task)
	}
	return strings.Join(lines, " ")
}

func extractBoltToolPath(input interface{}) string {
	switch v := input.(type) {
	case map[string]interface{}:
		for _, key := range []string{"file_path", "path", "filePath"} {
			if raw, ok := v[key]; ok {
				if value := strings.TrimSpace(fmt.Sprint(raw)); value != "" {
					return value
				}
			}
		}
	case map[string]string:
		for _, key := range []string{"file_path", "path", "filePath"} {
			if value := strings.TrimSpace(v[key]); value != "" {
				return value
			}
		}
	case json.RawMessage:
		if len(v) == 0 {
			return ""
		}
		var decoded interface{}
		if err := json.Unmarshal(v, &decoded); err == nil {
			return extractBoltToolPath(decoded)
		}
	case []byte:
		if len(v) == 0 {
			return ""
		}
		var decoded interface{}
		if err := json.Unmarshal(v, &decoded); err == nil {
			return extractBoltToolPath(decoded)
		}
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return ""
		}
		if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
			var decoded interface{}
			if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
				return extractBoltToolPath(decoded)
			}
		}
	}
	return ""
}

func compactBoltToolResultText(toolMeta boltToolUseMetadata, text string, focusedFileAliases map[string]struct{}) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	switch strings.TrimSpace(toolMeta.Name) {
	case "Read":
		text = stripBoltReadLineNumbers(text)
		if sharesBoltPathAlias(toolMeta.Aliases, focusedFileAliases) {
			return truncateBoltToolResultRunes(text, maxBoltFocusedReadRunes, "\n...[truncated]")
		}
		return truncateBoltToolResultRunes(text, maxBoltReadResultRunes, "\n...[truncated]")
	case "Bash", "Grep":
		return truncateBoltToolResultRunes(text, maxBoltShellResultRunes, "\n...[truncated]")
	default:
		return text
	}
}

func relativizeBoltWorkdirPaths(text, workdir string) string {
	text = strings.TrimSpace(text)
	workdir = strings.Trim(strings.TrimSpace(workdir), "\"'`")
	if text == "" || workdir == "" {
		return text
	}

	candidates := []string{
		workdir,
		strings.ReplaceAll(workdir, "\\", "/"),
		strings.ReplaceAll(workdir, "/", "\\"),
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.Trim(strings.TrimSpace(candidate), "\"'`")
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		text = strings.ReplaceAll(text, candidate+"\\", "")
		text = strings.ReplaceAll(text, candidate+"/", "")
		text = strings.ReplaceAll(text, candidate, ".")
	}
	return strings.TrimSpace(text)
}

func truncateBoltToolResultRunes(text string, limit int, suffix string) string {
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return string(runes[:limit])
	}
	suffixRunes := []rune("\n" + suffix)
	if len(suffixRunes) >= limit {
		return string(runes[:limit])
	}
	headLimit := limit - len(suffixRunes)
	if headLimit < 1 {
		headLimit = limit
		suffixRunes = nil
	}
	out := string(runes[:headLimit])
	if len(suffixRunes) > 0 {
		out += string(suffixRunes)
	}
	return out
}

func sanitizeBoltMessageText(text string) string {
	text = stripTaggedBoltText(text)
	text = stripBoltContinuationPrefix(text)
	return strings.TrimSpace(text)
}

var boltContinuationPrefixes = []string{
	"下面是上一轮工具结果。基于这些结果继续回答",
	"下面是上一轮工具结果，来自用户本地真实仓库",
	"下面是上一轮失败的工具结果。最近一次 Write/Edit 还没成功",
	"下面是上一轮工具结果。上一条用户请求仍然有效",
}

func stripBoltContinuationPrefix(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return text
	}
	for _, prefix := range boltContinuationPrefixes {
		if idx := strings.Index(trimmed, prefix); idx >= 0 {
			endIdx := strings.Index(trimmed[idx:], "\n\n")
			if endIdx >= 0 {
				trimmed = strings.TrimSpace(trimmed[:idx]) + "\n" + strings.TrimSpace(trimmed[idx+endIdx+2:])
			} else {
				trimmed = strings.TrimSpace(trimmed[:idx])
			}
			break
		}
	}
	return strings.TrimSpace(trimmed)
}

func sanitizeBoltAssistantText(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	text = stripNestedTaggedBlock(text, "thinking")
	text = stripTaggedBoltText(text)
	text = stripCCEntrypointLines(text)
	text = stripBoltEnvironmentLines(text)
	text = stripBoltToolTranscript(text)
	text = collapseBoltBlankLines(text)
	if isBoltNoReplySentinel(text) {
		return ""
	}
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}

func isBoltNoReplySentinel(text string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(text))
	return normalized == "NO_REPLY" || normalized == "NOREPLY"
}

func sanitizeBoltToolResultText(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	text = stripTaggedBoltText(text)
	text = stripCCEntrypointLines(text)
	text = stripBoltEnvironmentLines(text)
	text = collapseBoltBlankLines(text)
	if isBoltIgnorableToolResult(text) {
		return ""
	}
	return strings.TrimSpace(text)
}

func isBoltIgnorableToolResult(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return true
	}
	switch lower {
	case "unsupported_sim_command", "unsupported_sim_command:":
		return true
	default:
		return false
	}
}

func stripTaggedBoltText(text string) string {
	text = stripNestedTaggedBlock(text, "system-reminder")
	for _, tag := range boltStripTaggedNames {
		text = stripSimpleTaggedBlock(text, tag)
	}
	return text
}

func stripNestedTaggedBlock(text, tag string) string {
	return stripTaggedBlock(text, tag, true)
}

func stripSimpleTaggedBlock(text, tag string) string {
	return stripTaggedBlock(text, tag, false)
}

func stripTaggedBlock(text, tag string, greedy bool) string {
	startTag := "<" + tag + ">"
	endTag := "</" + tag + ">"
	if !strings.Contains(text, startTag) {
		return text
	}
	var sb strings.Builder
	sb.Grow(len(text))
	i := 0
	for i < len(text) {
		start := strings.Index(text[i:], startTag)
		if start == -1 {
			sb.WriteString(text[i:])
			break
		}
		sb.WriteString(text[i : i+start])
		endStart := i + start + len(startTag)
		var end int
		if greedy {
			end = strings.LastIndex(text[endStart:], endTag)
		} else {
			end = strings.Index(text[endStart:], endTag)
		}
		if end == -1 {
			sb.WriteString(text[i+start:])
			break
		}
		i = endStart + end + len(endTag)
	}
	return sb.String()
}

func stripCCEntrypointLines(text string) string {
	if !strings.Contains(strings.ToLower(text), "cc_entrypoint=") {
		return text
	}
	lines := strings.Split(text, "\n")
	filtered := lines[:0]
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), "cc_entrypoint=") {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.Join(filtered, "\n")
}

func stripBoltEnvironmentLines(text string) string {
	if !looksLikeBoltEnvironmentBlock(text) {
		return text
	}
	lines := strings.Split(text, "\n")
	filtered := lines[:0]
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		switch {
		case lower == "# environment":
			continue
		case lower == "# auto memory":
			continue
		case strings.HasPrefix(lower, "- primary working directory:"):
			continue
		case strings.HasPrefix(lower, "primary working directory:"):
			continue
		case strings.HasPrefix(lower, "working directory:"):
			continue
		case strings.HasPrefix(lower, "cwd:"):
			continue
		case strings.HasPrefix(lower, "you have been invoked in the following environment"):
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.Join(filtered, "\n")
}

func looksLikeBoltEnvironmentBlock(text string) bool {
	lower := strings.ToLower(text)
	markers := 0
	for _, marker := range boltEnvironmentBlockMarkers {
		if strings.Contains(lower, marker) {
			markers++
		}
	}
	return markers >= 2
}

func stripBoltToolTranscript(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	filtered := make([]string, 0, len(lines))
	inTranscript := false
	justExitedTranscript := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.ReplaceAll(line, "\u00a0", " "))
		if isBoltToolTranscriptHeader(trimmed) {
			inTranscript = true
			justExitedTranscript = false
			continue
		}
		if inTranscript {
			if trimmed == "" || isBoltIndentedTranscriptLine(line) || strings.HasPrefix(trimmed, "⎿") || strings.HasPrefix(trimmed, "... +") || strings.HasPrefix(trimmed, "… +") {
				continue
			}
			inTranscript = false
			justExitedTranscript = true
		}

		if justExitedTranscript {
			if cleaned, ok := stripBoltTranscriptBullet(trimmed); ok {
				line = cleaned
			}
			justExitedTranscript = false
		}

		filtered = append(filtered, strings.TrimRight(line, " \t"))
	}

	return strings.Join(filtered, "\n")
}

func isBoltToolTranscriptHeader(line string) bool {
	if line == "" {
		return false
	}
	lower := strings.ToLower(line)
	if strings.Contains(lower, "ctrl+o to expand") {
		return true
	}
	if !strings.HasPrefix(line, "● ") {
		return false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, "● "))
	for _, name := range boltSupportedToolOrder {
		if strings.HasPrefix(rest, name+"(") {
			return true
		}
	}
	return false
}

func isBoltIndentedTranscriptLine(line string) bool {
	if line == "" {
		return false
	}
	switch line[0] {
	case ' ', '\t':
		return true
	default:
		return false
	}
}

func stripBoltTranscriptBullet(line string) (string, bool) {
	if !strings.HasPrefix(line, "● ") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "● ")), true
}

func collapseBoltBlankLines(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	prevBlank := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
			out = append(out, "")
			continue
		}
		prevBlank = false
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func stringifyContent(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []prompt.ContentBlock:
		var sb strings.Builder
		first := true
		for _, block := range x {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				if !first {
					sb.WriteByte('\n')
				}
				sb.WriteString(block.Text)
				first = false
			}
		}
		if !first {
			return sb.String()
		}
	case []interface{}:
		var sb strings.Builder
		first := true
		for _, item := range x {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				if !first {
					sb.WriteByte('\n')
				}
				sb.WriteString(text)
				first = false
			}
		}
		if !first {
			return sb.String()
		}
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

func generateRandomID(length int) string {
	bytes := make([]byte, length/2+1)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)[:length]
}
