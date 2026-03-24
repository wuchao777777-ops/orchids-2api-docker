package bolt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	defaultAdapterPrompt    = "你正在通过 Orchids 的 Bolt 适配层处理一个代码代理对话。能直接回答时直接回答；当用户要求实际执行代码、修改文件、运行命令、提交或推送 git 时，应优先直接调用工具完成，不要只给步骤说明。需要查看代码、目录或执行命令时，不要先解释计划，不要说你接下来要去看文件，而是立刻返回严格 JSON 的工具调用。"
	maxBoltFocusedFileCount = 2
	maxBoltReadResultRunes  = 900
	maxBoltFocusedReadRunes = 2400
	maxBoltShellResultRunes = 1100
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
var boltInvalidPathMarkers = []string{"/tmp/cc-agent/", "/mnt/", "d:\\", "c:\\", "~/"}
var boltAssistantCompletionMarkers = []string{
	"已创建",
	"创建完成",
	"已经创建",
	"文件已成功创建",
	"文件已经存在",
	"项目中已经有一个",
	"已在项目目录",
	"已更新",
	"已经被更新",
	"运行方式",
	"可以直接运行",
	"当前文件状态是好的",
	"无需进一步操作",
	"created successfully",
	"has been created",
	"already exists",
	"already have",
	"has been updated",
	"updated successfully",
	"run it with",
	"can run",
	"no further action",
	"current file state is good",
}
var boltEnvironmentBlockMarkers = []string{
	"# environment",
	"primary working directory:",
	"# auto memory",
	"gitstatus:",
	"you have been invoked in the following environment",
}
var boltStructuredNestedKeys = []string{"parts", "content", "delta", "data", "messages"}
var boltStructuredStringKeys = []string{"text", "message", "content", "delta", "output", "response"}
var boltToolCallCollectionKeys = []string{"tool_calls", "toolCalls", "calls", "tool_call", "toolCall"}
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
		proxyFunc = util.ProxyFunc(cfg.ProxyHTTP, cfg.ProxyHTTPS, cfg.ProxyUser, cfg.ProxyPass, cfg.ProxyBypass)
		proxyKey = util.GenerateProxyKey(cfg.ProxyHTTP, cfg.ProxyHTTPS, cfg.ProxyUser)
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
	req.Messages = trimBoltMessagesAfterSupersededEmptyProjectClarification(req.Messages)
	req.Messages = trimBoltMessagesAfterSupersededAssistantCompletion(req.Messages)
	req.Messages = trimBoltMessagesAfterSupersededReadFollowup(req.Messages)
	promptParts := buildSystemPromptParts(req.System, req.Workdir, req.Tools, req.NoTools, req.Messages)
	messages := buildBoltMessages(req.Messages)

	boltReq := &Request{
		ID:                   generateRandomID(16),
		SelectedModel:        strings.TrimSpace(req.Model),
		IsFirstPrompt:        false,
		PromptMode:           "build",
		EffortLevel:          "high",
		ProjectID:            projectID,
		GlobalSystemPrompt:   promptParts.FullPrompt,
		ProjectPrompt:        promptParts.FullPrompt,
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

func shouldSkipBoltMessage(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), "tool")
}

func buildSystemPromptParts(system []prompt.SystemItem, workdir string, tools []interface{}, noTools bool, messages []prompt.Message) systemPromptParts {
	parts := systemPromptParts{
		BasePrompt: defaultAdapterPrompt,
		ToolPrompt: buildBoltToolPrompt(workdir, tools, noTools, messages),
	}

	custom := make([]string, 0, len(system))
	for _, item := range system {
		text := sanitizeBoltSystemText(item.Text)
		if strings.TrimSpace(text) != "" {
			custom = append(custom, text)
		}
	}
	parts.SystemPrompt = strings.Join(custom, "\n\n")

	combined := make([]string, 0, 3)
	for _, part := range []string{parts.BasePrompt, parts.ToolPrompt, parts.SystemPrompt} {
		if strings.TrimSpace(part) != "" {
			combined = append(combined, part)
		}
	}
	parts.FullPrompt = strings.Join(combined, "\n\n")
	return parts
}

func buildBoltMessages(messages []prompt.Message) builtMessages {
	built := builtMessages{
		Items: make([]Message, 0, len(messages)),
	}
	toolUses := collectBoltToolUseMetadata(messages)
	gitUploadIntent := detectRecentBoltGitUploadIntent(messages)
	focusedFileAliases := collectFocusedBoltFileAliases(messages)
	var lastUserMsgID string
	var lastSubstantiveUserTask string
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
			if standalone := extractBoltStandaloneUserText(msg); strings.TrimSpace(standalone) != "" && !LooksLikeContinuationOnlyText(standalone) {
				lastSubstantiveUserTask = standalone
			}
			lastUserMsgID = boltMsg.ID
			boltMsg.Content = extractBoltUserContent(blocks, toolUses, gitUploadIntent, focusedFileAliases, lastSubstantiveUserTask)
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

type boltToolUseMetadata struct {
	Name        string
	Path        string
	InvalidPath bool
	Aliases     map[string]struct{}
}

func collectBoltToolUseMetadata(messages []prompt.Message) map[string]boltToolUseMetadata {
	toolUses := make(map[string]boltToolUseMetadata)
	for _, msg := range messages {
		for _, block := range normalizeBlocks(msg) {
			if block.Type != "tool_use" || strings.TrimSpace(block.ID) == "" {
				continue
			}
			path := extractBoltToolPath(block.Input)
			toolUses[strings.TrimSpace(block.ID)] = boltToolUseMetadata{
				Name:        strings.TrimSpace(block.Name),
				Path:        path,
				InvalidPath: looksLikeInvalidBoltPath(path),
				Aliases:     buildBoltPathAliases(path),
			}
		}
	}
	return toolUses
}

func collectFocusedBoltFileAliases(messages []prompt.Message) map[string]struct{} {
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
			path := extractBoltToolPath(block.Input)
			if path == "" || looksLikeInvalidBoltPath(path) {
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

func buildBoltToolPrompt(workdir string, tools []interface{}, noTools bool, messages []prompt.Message) string {
	workdir = strings.TrimSpace(workdir)
	parts := append([]string{}, buildBoltWorkspacePrompt(workdir)...)

	if noTools {
		parts = append(parts, "这次回合不要发起任何工具调用，只基于已有上下文和已返回的工具结果直接回答。")
		return strings.Join(parts, "\n")
	}

	toolNames := supportedBoltToolNames(tools)
	if len(toolNames) == 0 {
		parts = append(parts, "如果上下文已经足够，请直接回答。")
		return strings.Join(parts, "\n")
	}

	parts = append(parts, buildBoltToolUsagePrompt(toolNames)...)
	parts = append(parts, buildBoltHistoryRecoveryPrompt(workdir, messages)...)
	if hasBoltToolName(toolNames, "Bash") {
		parts = append(parts, buildBoltGitExecutionPrompt(messages)...)
	}
	parts = append(parts, "单个工具调用格式: {\"tool\":\"Read\",\"parameters\":{\"file_path\":\"README.md\"}}")
	parts = append(parts, "多个工具调用格式: {\"tool_calls\":[{\"function\":\"Glob\",\"parameters\":{\"path\":\".\",\"pattern\":\"*.go\"}},{\"function\":\"Read\",\"parameters\":{\"file_path\":\"README.md\"}}]}")
	parts = append(parts, "拿到工具结果后，继续基于结果回答或发起下一步工具调用，不要重复已经完成的同一调用。")

	return strings.Join(parts, "\n")
}

func buildBoltWorkspacePrompt(workdir string) []string {
	if strings.TrimSpace(workdir) == "" {
		return nil
	}

	parts := make([]string, 0, 5)
	projectName := filepath.Base(filepath.Clean(workdir))
	if strings.Contains(workdir, "\\") {
		windowsPath := strings.ReplaceAll(workdir, "\\", "/")
		if base := filepath.Base(filepath.Clean(windowsPath)); strings.TrimSpace(base) != "" {
			projectName = base
		}
	}
	if projectName != "" && projectName != "." && projectName != string(filepath.Separator) {
		parts = append(parts, "当前项目目录名: "+projectName)
	}
	parts = append(parts, "当前项目真实工作目录(仅用于回答用户询问“项目目录地址/当前路径/workspace 在哪里”这类问题): `"+workdir+"`")
	parts = append(parts, "如果用户问项目目录地址、当前项目路径或 workspace 路径，直接回答上面的真实工作目录；不要回答 `/tmp/cc-agent/...`、`/mnt/...`、`~/...` 这类沙箱占位路径。")
	parts = append(parts, "把项目根目录视为 `.`。Read/Write/Edit/Glob/Grep 的路径统一优先使用相对路径；如果要看项目根目录，直接用 `.`。")
	parts = append(parts, "Bash 默认就在项目根目录执行，只写项目内的相对路径，不要拼接 `d:\\...`、`C:\\...`、`/mnt/...`、`~/...` 或 `/tmp/cc-agent/...`。")
	if isBoltGitRepository(workdir) {
		parts = append(parts, "当前项目已经是一个 git 仓库；当用户要求上传到 git、提交或推送时，不要误判为没有 `.git` 仓库，应优先直接使用 Bash 执行 git status/add/commit/push。")
	}
	return parts
}

func isBoltGitRepository(workdir string) bool {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(workdir, ".git"))
	return err == nil
}

func buildBoltToolUsagePrompt(toolNames []string) []string {
	toolHints := make([]string, 0, len(toolNames))
	for _, name := range toolNames {
		toolHints = append(toolHints, supportedToolHint(name))
	}

	parts := []string{
		"可用工具: " + strings.Join(toolHints, "; "),
		"需要工具时，输出纯 JSON，不要加解释、不要加前后缀、不要说“让我先看看项目文件”。",
		"如果这回合决定调用工具，不要先输出“我来修改”“我先看一下”“让我读取后再改”之类说明文字；第一个非空输出字符应当直接是 `{`，避免浪费 token。",
		"不要解释当前运行在什么系统或沙箱；如果需要确认目录或文件，直接调用工具。",
		"如果某次工具结果提示路径不存在，不要据此断言项目为空；应优先改用 `.`、README.md、go.mod、package.json 等项目内相对路径继续调用工具。",
		"如果目标文件已经存在，优先使用 Edit 做最小修改；只有在创建新文件，或你明确打算整文件重写且确实需要一次性替换全文时才使用 Write。",
		"如果你刚刚已经 Read 了某个文件，接下来目标又是继续修改它，优先沿用同一路径继续 Edit；不要无故改成整文件 Write。",
		"如果 Write/Edit 的工具结果出现 `Hook PreToolUse` 或 `denied this tool`，不要改写成 `/tmp/cc-agent/...` 之类的沙箱绝对路径重试；应继续坚持项目内相对路径，必要时先用 Read/Glob 确认再对同一路径继续操作。",
		"如果最近一轮 Write/Edit 已经成功返回，不要仅为了确认结果就再次 Read 同一文件，更不要切到 `/tmp/cc-agent/...` 路径验证；优先直接向用户总结已经完成的修改。",
		"如果最近一轮 Write/Edit 明确报错，或工具结果里包含 `<tool_use_error>`、`Error editing file`、`String to replace not found` 等失败信号，说明修改尚未完成；不要沿用更早的成功 Write/Edit 来声称已经更新完成，应继续基于最新错误和当前文件上下文调用工具修复，直到出现新的成功结果。",
		"如果刚通过 Glob/Read/Bash 确认项目根目录为空，而用户目标已经足够具体，不要再次追问“要构建什么”；应直接在 `.` 创建最小可运行实现。",
		"空目录初始化时，优先直接使用 Write 创建首个文件，不要在 `No files found` 之后继续反复 Glob、ls 或再次输出空项目澄清。",
		"如果用户明确要求修改、创建或继续完善代码，而历史里只有 Read/Glob/Grep/Bash 结果、还没有对应的成功 Write/Edit 结果，不要声称“已经完成”“文件已经包含该功能”或“无需进一步操作”；应继续调用工具完成实际修改。",
		"如果你刚刚已经读过相关文件，用户随后又明确说“修改/改进/添加 X”，不要继续追问需求，也不要停在现状总结；直接基于已读内容继续 Edit/Write，只有缺少关键上下文时才补一次 Read。",
		"在连续编程对话里，用户后续补充的技术说明、约束或示例，默认视为对当前实现的追加需求；如果需要落地到代码，继续调用工具修改，而不是只解释这段说明。",
		"例如用户说“帮我用python写一个计算器”，默认直接在项目根目录创建 `calculator.py`。",
	}
	if hasBoltToolName(toolNames, "Task") {
		parts = append(parts,
			"只有在本地 Read/Glob/Grep/Bash 不足以完成广泛代码探索时才使用 `Task`；不要为了确认根目录、判断空项目或查单个文件就先发起子任务。",
			"如果客户端声明的是 `Agent`，上游 Bolt 可能会返回 `Task`；把它视为同一种子代理能力继续执行。",
		)
	}
	return parts
}

func buildBoltGitExecutionPrompt(messages []prompt.Message) []string {
	if !detectRecentBoltGitUploadIntent(messages) {
		return nil
	}
	return []string{
		"用户当前明确要求把当前代码上传到 git / 完成提交推送。这已经构成对本地 git add、git commit、git push 的明确授权。",
		"这是一个基于已有工具结果继续推进的连续 git 流程；不要重新打招呼，不要把它当成新的空白任务重新开始。",
		"只要已经收到 git status / git add / git diff 等工具结果，就说明这些结果来自用户本地真实仓库；不要再根据 `/tmp/cc-agent/...` 之类的沙箱占位路径声称“没有 .git 仓库”。",
		"如果当前项目已经是 git 仓库，第一步优先直接使用 Bash 执行 git status 或 git status --short；不要先用 ls、Read、Glob 之类与 git 无关的检查替代。",
		"这里的“当前代码”默认指当前工作区里的全部改动；除非用户明确说只提交部分文件，否则不要停下来追问“只提交已暂存的文件还是全部改动”。",
		"如果已经确认当前项目是 git 仓库且 Bash 可用，不要只给用户输出命令步骤；应继续直接使用 Bash 执行 git status/add/commit/push，直到真正完成上传，或遇到真实命令错误再说明原因。",
		"如果已经拿到 git status、git diff、git add 等结果，除非用户明确改变目标，否则不要在中间停下来改成文字建议。",
		"如果上一轮某个 git 步骤已经成功返回结果，应直接继续下一步，不要重复已经成功完成的同一步；只有当最近一次工具结果明确报错时，才回退并重试。",
	}
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
		"上传到 git",
		"上传到git",
		"推送到 git",
		"推送到git",
		"提交并推送",
		"提交到 git",
		"提交到git",
		"git push",
		"commit and push",
		"push to git",
		"upload to git",
		"push code",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func hasBoltToolName(toolNames []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, name := range toolNames {
		if strings.EqualFold(strings.TrimSpace(name), want) {
			return true
		}
	}
	return false
}

func buildBoltHistoryRecoveryPrompt(workdir string, messages []prompt.Message) []string {
	invalidPath := detectRecentInvalidBoltHistoryPath(messages)
	if invalidPath == "" {
		return nil
	}

	parts := []string{
		"检测到历史里刚刚有一次无效的外部路径工具调用 `" + invalidPath + "`，它不是当前项目目录；把那次失败视为错误示例，不要复用这个路径，也不要基于它做同路径变体。",
	}
	if strings.TrimSpace(workdir) != "" {
		parts = append(parts, "真实项目目录是 `"+workdir+"`；如果用户追问项目目录地址，直接回答这个真实工作目录。")
	}
	parts = append(parts, "如果需要重新检查项目，下一次必须改用项目根目录 `.` 或 README.md、go.mod、package.json 这类项目内相对路径。")
	parts = append(parts, "在至少成功查看一次 `.`、README.md、go.mod、package.json 等项目内路径之前，不要回答“项目为空”“没有文件”或“目录是空的”。")
	return parts
}

func detectRecentInvalidBoltHistoryPath(messages []prompt.Message) string {
	if len(messages) == 0 {
		return ""
	}

	toolPaths := make(map[string]string)
	for _, msg := range messages {
		for _, block := range normalizeBlocks(msg) {
			if block.Type != "tool_use" || strings.TrimSpace(block.ID) == "" {
				continue
			}
			if path := extractInvalidBoltPathFromValue(block.Input); path != "" {
				toolPaths[block.ID] = path
			}
		}
	}

	for i := len(messages) - 1; i >= 0; i-- {
		for _, block := range normalizeBlocks(messages[i]) {
			if block.Type != "tool_result" || !isBoltMissingPathResult(block.Content) {
				continue
			}
			if path := toolPaths[strings.TrimSpace(block.ToolUseID)]; path != "" {
				return path
			}
			if path := extractInvalidBoltPathFromValue(block.Content); path != "" {
				return path
			}
		}
	}

	return ""
}

func isBoltMissingPathResult(content interface{}) bool {
	lower := strings.ToLower(strings.TrimSpace(stringifyContent(content)))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"no such file or directory",
		"cannot access",
		"does not exist",
		"path does not exist",
		"系统找不到指定的路径",
		"找不到指定的路径",
		"enoent",
		"not found",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func extractInvalidBoltPathFromValue(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return extractInvalidBoltPathFromString(v)
	case map[string]interface{}:
		for _, nested := range v {
			if path := extractInvalidBoltPathFromValue(nested); path != "" {
				return path
			}
		}
	case []interface{}:
		for _, nested := range v {
			if path := extractInvalidBoltPathFromValue(nested); path != "" {
				return path
			}
		}
	case []prompt.ContentBlock:
		for _, nested := range v {
			if path := extractInvalidBoltPathFromValue(nested.Content); path != "" {
				return path
			}
			if path := extractInvalidBoltPathFromString(nested.Text); path != "" {
				return path
			}
		}
	default:
		if data, err := json.Marshal(v); err == nil {
			return extractInvalidBoltPathFromString(string(data))
		}
	}
	return ""
}

func extractInvalidBoltPathFromString(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	for _, field := range strings.Fields(text) {
		candidate := strings.Trim(field, "\"'`,;()[]{}")
		if looksLikeInvalidBoltPath(candidate) {
			return candidate
		}
	}

	lower := strings.ToLower(text)
	for _, marker := range boltInvalidPathMarkers {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		end := len(text)
		for i := idx; i < len(text); i++ {
			switch text[i] {
			case ' ', '\n', '\r', '\t', '"', '\'', '`':
				end = i
				goto found
			}
		}
	found:
		candidate := strings.Trim(text[idx:end], "\"'`,;()[]{}")
		if looksLikeInvalidBoltPath(candidate) {
			return candidate
		}
	}
	return ""
}

func looksLikeInvalidBoltPath(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "/tmp/cc-agent/") ||
		strings.Contains(lower, "/mnt/") ||
		strings.HasPrefix(lower, "~/") ||
		strings.Contains(lower, "d:\\") ||
		strings.Contains(lower, "c:\\")
}

func supportedBoltToolNames(tools []interface{}) []string {
	if len(tools) == 0 {
		return nil
	}

	rawNames := make([]string, 0, len(tools))
	for _, raw := range tools {
		name := strings.TrimSpace(extractBoltToolName(raw))
		if name == "" {
			continue
		}
		rawNames = append(rawNames, name)
	}

	toolNames := FilterSupportedToolNames(rawNames)
	return toolNames
}

func extractBoltToolName(raw interface{}) string {
	rawMap, ok := raw.(map[string]interface{})
	if !ok {
		return ""
	}
	if function, ok := rawMap["function"].(map[string]interface{}); ok {
		if name, ok := function["name"].(string); ok {
			return strings.TrimSpace(name)
		}
	}
	if name, ok := rawMap["name"].(string); ok {
		return strings.TrimSpace(name)
	}
	return ""
}

func sanitizeBoltSystemText(text string) string {
	text = stripTaggedBoltText(text)
	text = stripCCEntrypointLines(text)
	text = stripBoltEnvironmentLines(text)
	text = trimBoltSystemBoilerplate(text)
	return condenseBoltSystemText(text)
}

func trimBoltSystemBoilerplate(text string) string {
	if !looksLikeClaudeCodeBoilerplate(text) {
		return text
	}
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	keepSection := false
	seenHeading := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isBoltMarkdownHeading(trimmed) {
			seenHeading = true
			keepSection = !shouldDropBoltSystemSection(trimmed)
			if keepSection && !shouldSuppressBoltSystemHeading(trimmed) {
				kept = append(kept, trimmed)
			}
			continue
		}
		if !seenHeading {
			if !shouldDropBoltSystemLine(trimmed) {
				kept = append(kept, trimmed)
			}
			continue
		}
		if keepSection {
			kept = append(kept, trimmed)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n")
}

func looksLikeClaudeCodeBoilerplate(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"you are an interactive agent that helps users with software engineering tasks",
		"# doing tasks",
		"# using your tools",
		"# auto memory",
		"# mcp server instructions",
		"the following mcp servers have provided instructions",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isBoltMarkdownHeading(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "#")
}

func shouldDropBoltSystemSection(heading string) bool {
	lower := strings.ToLower(strings.TrimSpace(heading))
	switch lower {
	case "# system":
		return true
	case "# doing tasks":
		return true
	case "# executing actions with care":
		return true
	case "# using your tools":
		return true
	case "# tone and style":
		return true
	case "# output efficiency":
		return true
	case "# auto memory":
		return true
	case "## types of memory":
		return true
	case "## what not to save in memory":
		return true
	case "## how to save memories":
		return true
	case "## when to access memories":
		return true
	case "## before recommending from memory":
		return true
	case "## memory and other forms of persistence":
		return true
	case "# environment":
		return true
	case "# committing changes with git":
		return true
	}
	return false
}

func shouldSuppressBoltSystemHeading(heading string) bool {
	switch strings.ToLower(strings.TrimSpace(heading)) {
	case "# mcp server instructions":
		return true
	case "## context7":
		return true
	case "# vscode extension context":
		return true
	default:
		return false
	}
}

func condenseBoltSystemText(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if shouldDropBoltSystemLine(trimmed) {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "\n")
}

func shouldDropBoltSystemLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	switch {
	case lower == "":
		return true
	case lower == "the following mcp servers have provided instructions for how to use their tools and resources:":
		return true
	case strings.Contains(lower, "use this server to retrieve up-to-date documentation and code examples for any library."):
		return true
	case strings.Contains(lower, "when working with tool results, write down any important information you might need later in your response"):
		return true
	case strings.Contains(lower, "anthropic's official cli for claude"):
		return true
	case strings.Contains(lower, "you are claude code"):
		return true
	case strings.Contains(lower, "you are an interactive cli tool"):
		return true
	case strings.Contains(lower, "you are an interactive agent that helps users with software engineering tasks"):
		return true
	case strings.Contains(lower, "claude agent sdk"):
		return true
	case strings.Contains(lower, "claude code system prompt"):
		return true
	case strings.Contains(lower, "assist with authorized security testing"):
		return true
	case strings.Contains(lower, "you must never generate or guess urls"):
		return true
	case strings.Contains(lower, "the system will automatically compress prior messages"):
		return true
	case strings.Contains(lower, "assistant knowledge cutoff is august 2025"):
		return true
	case strings.Contains(lower, "the most recent claude model family"):
		return true
	case strings.Contains(lower, "fast mode for claude code uses the same claude opus 4.6 model"):
		return true
	}
	return false
}

func sanitizeBoltMessageText(text string) string {
	return strings.TrimSpace(stripTaggedBoltText(text))
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
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return text
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
		blockStart := i + start
		endStart := blockStart + len(startTag)
		end := strings.LastIndex(text[endStart:], endTag)
		if end == -1 {
			sb.WriteString(text[blockStart:])
			break
		}
		i = endStart + end + len(endTag)
	}
	return sb.String()
}

func stripSimpleTaggedBlock(text, tag string) string {
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
		blockStart := i + start
		endStart := blockStart + len(startTag)
		end := strings.Index(text[endStart:], endTag)
		if end == -1 {
			sb.WriteString(text[blockStart:])
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
				trimmed = cleaned
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

func normalizeBlocks(msg prompt.Message) []prompt.ContentBlock {
	if msg.Content.IsString() {
		text := msg.Content.GetText()
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []prompt.ContentBlock{{Type: "text", Text: text}}
	}
	return msg.Content.GetBlocks()
}

func trimBoltMessagesAfterSupersededEmptyProjectClarification(messages []prompt.Message) []prompt.Message {
	if len(messages) < 3 {
		return messages
	}

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			continue
		}
		if !looksLikeBoltEmptyProjectClarification(msg.ExtractText()) {
			continue
		}

		replyIdx := -1
		for j := i + 1; j < len(messages); j++ {
			if text := extractBoltStandaloneUserText(messages[j]); text != "" {
				replyIdx = j
				break
			}
		}
		if replyIdx < 0 {
			return messages
		}

		trimmed := append([]prompt.Message(nil), messages[replyIdx:]...)
		return trimmed
	}

	return messages
}

func trimBoltMessagesAfterSupersededAssistantCompletion(messages []prompt.Message) []prompt.Message {
	if len(messages) < 6 {
		return messages
	}

	drop := make([]bool, len(messages))
	dropped := false

	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			continue
		}

		text := extractBoltAssistantHistoryContent(normalizeBlocks(msg))
		if !looksLikeBoltAssistantCompletionSummary(text) {
			continue
		}

		prevIdx := previousBoltVisibleMessageIndex(messages, i-1)
		if prevIdx < 0 || !isBoltToolResultOnlyUserMessage(messages[prevIdx]) {
			continue
		}

		nextUserIdx := nextBoltStandaloneUserMessageIndex(messages, i+1)
		if nextUserIdx < 0 {
			continue
		}
		if !hasBoltToolResultOnlyUserFollowupBeforeNextStandaloneUser(messages, nextUserIdx+1) {
			continue
		}

		drop[i] = true
		dropped = true
	}

	if !dropped {
		return messages
	}

	trimmed := make([]prompt.Message, 0, len(messages))
	for i, msg := range messages {
		if !drop[i] {
			trimmed = append(trimmed, msg)
		}
	}
	return trimmed
}

func looksLikeBoltEmptyProjectClarification(text string) bool {
	text = sanitizeBoltAssistantText(text)
	if strings.TrimSpace(text) == "" {
		return false
	}

	lower := strings.ToLower(text)
	for _, marker := range []string{
		"这是一个空项目",
		"项目目录为空",
		"请告诉我你想要构建什么",
		"project directory is empty",
		"what would you like to build",
		"please describe your application",
	} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func looksLikeBoltAssistantCompletionSummary(text string) bool {
	text = sanitizeBoltAssistantText(text)
	if strings.TrimSpace(text) == "" {
		return false
	}
	if len([]rune(text)) > 900 {
		return false
	}

	lower := strings.ToLower(text)
	for _, marker := range boltAssistantCompletionMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func trimBoltMessagesAfterSupersededReadFollowup(messages []prompt.Message) []prompt.Message {
	if len(messages) < 5 {
		return messages
	}

	toolUses := collectBoltToolUseMetadata(messages)
	drop := make([]bool, len(messages))
	dropped := false

	for i := 0; i < len(messages); i++ {
		current := summarizeBoltToolResultOnlyMessage(messages[i], toolUses)
		if !current.IsToolResultOnly || len(current.ReadAliases) == 0 {
			continue
		}

		for j := i + 1; j < len(messages); j++ {
			if extractBoltStandaloneUserText(messages[j]) != "" {
				break
			}
			next := summarizeBoltToolResultOnlyMessage(messages[j], toolUses)
			if !next.IsToolResultOnly || len(next.SuccessfulMutationAliases) == 0 {
				continue
			}
			if sharesBoltPathAlias(current.ReadAliases, next.SuccessfulMutationAliases) {
				drop[i] = true
				dropped = true
				break
			}
		}
	}

	if !dropped {
		return messages
	}

	trimmed := make([]prompt.Message, 0, len(messages))
	for i, msg := range messages {
		if !drop[i] {
			trimmed = append(trimmed, msg)
		}
	}
	return trimmed
}

type boltToolResultOnlySummary struct {
	IsToolResultOnly          bool
	ReadAliases               map[string]struct{}
	SuccessfulMutationAliases map[string]struct{}
}

func summarizeBoltToolResultOnlyMessage(msg prompt.Message, toolUses map[string]boltToolUseMetadata) boltToolResultOnlySummary {
	summary := boltToolResultOnlySummary{}
	if !isBoltToolResultOnlyUserMessage(msg) {
		return summary
	}
	summary.IsToolResultOnly = true

	for _, block := range normalizeBlocks(msg) {
		if !strings.EqualFold(strings.TrimSpace(block.Type), "tool_result") {
			continue
		}
		rawText := sanitizeBoltToolResultText(stringifyContent(block.Content))
		if rawText == "" {
			continue
		}
		toolMeta := toolUses[strings.TrimSpace(block.ToolUseID)]
		if toolMeta.InvalidPath || len(toolMeta.Aliases) == 0 {
			continue
		}
		switch strings.TrimSpace(toolMeta.Name) {
		case "Read":
			if !isBoltToolResultError(block, rawText) {
				if summary.ReadAliases == nil {
					summary.ReadAliases = make(map[string]struct{})
				}
				for alias := range toolMeta.Aliases {
					summary.ReadAliases[alias] = struct{}{}
				}
			}
		case "Write", "Edit":
			if isBoltToolResultSuccess(toolMeta.Name, rawText) {
				if summary.SuccessfulMutationAliases == nil {
					summary.SuccessfulMutationAliases = make(map[string]struct{})
				}
				for alias := range toolMeta.Aliases {
					summary.SuccessfulMutationAliases[alias] = struct{}{}
				}
			}
		}
	}

	return summary
}

func extractBoltStandaloneUserText(msg prompt.Message) string {
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
		return ""
	}

	if msg.Content.IsString() {
		return sanitizeBoltMessageText(msg.Content.GetText())
	}

	blocks := msg.Content.GetBlocks()
	if len(blocks) == 0 {
		return ""
	}

	var sb strings.Builder
	first := true
	hasToolResult := false
	for _, block := range blocks {
		switch strings.ToLower(strings.TrimSpace(block.Type)) {
		case "tool_result":
			hasToolResult = true
		case "text":
			if text := sanitizeBoltMessageText(block.Text); text != "" {
				if !first {
					sb.WriteByte('\n')
				}
				sb.WriteString(text)
				first = false
			}
		}
	}
	if hasToolResult {
		return ""
	}
	return sb.String()
}

func isBoltToolResultOnlyUserMessage(msg prompt.Message) bool {
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
		return false
	}

	blocks := normalizeBlocks(msg)
	if len(blocks) == 0 {
		return false
	}

	hasToolResult := false
	for _, block := range blocks {
		switch strings.ToLower(strings.TrimSpace(block.Type)) {
		case "tool_result":
			hasToolResult = true
		case "text":
			if sanitizeBoltMessageText(block.Text) != "" {
				return false
			}
		default:
			return false
		}
	}
	return hasToolResult
}

func previousBoltVisibleMessageIndex(messages []prompt.Message, start int) int {
	for i := start; i >= 0; i-- {
		if shouldSkipBoltMessage(messages[i].Role) {
			continue
		}
		return i
	}
	return -1
}

func nextBoltStandaloneUserMessageIndex(messages []prompt.Message, start int) int {
	for i := start; i < len(messages); i++ {
		if extractBoltStandaloneUserText(messages[i]) != "" {
			return i
		}
	}
	return -1
}

func hasBoltToolResultOnlyUserFollowupBeforeNextStandaloneUser(messages []prompt.Message, start int) bool {
	for i := start; i < len(messages); i++ {
		if isBoltToolResultOnlyUserMessage(messages[i]) {
			return true
		}
		if extractBoltStandaloneUserText(messages[i]) != "" {
			return false
		}
	}
	return false
}

func hasEphemeralCache(blocks []prompt.ContentBlock) bool {
	for _, block := range blocks {
		if block.CacheControl != nil && block.CacheControl.Type == "ephemeral" {
			return true
		}
	}
	return false
}

func extractTextContent(blocks []prompt.ContentBlock) string {
	var sb strings.Builder
	first := true
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			if text := sanitizeBoltMessageText(block.Text); text != "" {
				if !first {
					sb.WriteByte('\n')
				}
				sb.WriteString(text)
				first = false
			}
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
	Text        string
	InvalidPath bool
	IsError     bool
	IsSuccess   bool
	IsMutation  bool
	Aliases     map[string]struct{}
	Drop        bool
}

func extractBoltUserContent(blocks []prompt.ContentBlock, toolUses map[string]boltToolUseMetadata, gitUploadIntent bool, focusedFileAliases map[string]struct{}, continuationTask string) string {
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
			text := compactBoltToolResultText(toolMeta, rawText, focusedFileAliases)
			result := &boltSerializedToolResult{
				Text:        text,
				InvalidPath: toolMeta.InvalidPath || looksLikeInvalidBoltPath(rawText),
				IsError:     isBoltToolResultError(block, rawText),
				IsSuccess:   isBoltToolResultSuccess(toolMeta.Name, rawText),
				IsMutation:  isBoltMutationTool(toolMeta.Name),
				Aliases:     toolMeta.Aliases,
			}
			results = append(results, result)
			parts = append(parts, boltUserContentPart{
				Text:   "Tool result:\n" + text,
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
	for _, part := range parts {
		if part.Result == nil {
			continue
		}
		if part.Result.InvalidPath || part.Result.Drop || strings.TrimSpace(part.Text) == "" {
			continue
		}
		hasVisibleToolResult = true
		break
	}

	var sb strings.Builder
	first := true
	if hasVisibleToolResult && !hasUserText {
		sb.WriteString(formatBoltToolResultContinuation(
			gitUploadIntent,
			continuationTask,
			hasVisibleSuccessfulMutationResult(results),
			hasVisibleFailedMutationResult(results),
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

func hasVisibleSuccessfulMutationResult(results []*boltSerializedToolResult) bool {
	for _, result := range results {
		if result == nil || result.Drop || result.InvalidPath {
			continue
		}
		if result.IsMutation && result.IsSuccess {
			return true
		}
	}
	return false
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

func formatBoltToolResultContinuation(gitUploadIntent bool, continuationTask string, mutationSucceeded bool, mutationFailed bool) string {
	if gitUploadIntent {
		return "继续完成当前的 git 提交与推送任务。以下内容是上一轮工具调用刚返回的结果，这些结果来自用户本地真实仓库，不是 `/tmp/cc-agent/...` 沙箱。请直接基于这些结果继续，不要重新打招呼，也不要把它当成新的空白任务。"
	}

	task := truncateBoltContinuationTask(strings.TrimSpace(continuationTask), 120)
	if mutationFailed {
		if task != "" {
			return "继续完成用户刚才明确提出的任务：" + task + "。以下内容是上一轮工具调用刚返回的失败结果，说明最近一次 Write/Edit 尚未完成请求的修改。不要声称已经完成，不要把更早的成功 Write/Edit 当成这次修改已经成功；应直接基于这些失败结果和已有上下文继续调用 Read/Edit/Write/Bash 修复，直到出现新的成功工具结果。如果目标文件已经存在或你刚刚读过它，优先继续调用 Edit 做最小修改；不要先输出说明文字，一旦决定调用工具，本回合第一个非空输出字符应当直接是 `{`。"
		}
		return "以下内容是上一轮工具调用刚返回的失败结果，说明最近一次 Write/Edit 尚未完成请求的修改。不要声称已经完成，不要把更早的成功 Write/Edit 当成这次修改已经成功；应直接基于这些失败结果和已有上下文继续调用 Read/Edit/Write/Bash 修复，直到出现新的成功工具结果。如果目标文件已经存在或你刚刚读过它，优先继续调用 Edit 做最小修改；不要先输出说明文字，一旦决定调用工具，本回合第一个非空输出字符应当直接是 `{`。"
	}
	if mutationSucceeded {
		if task != "" {
			return "继续完成用户刚才明确提出的任务：" + task + "。以下内容是上一轮工具调用刚返回的成功结果。若 Write/Edit 已经成功，就优先直接向用户总结已完成的修改，不要仅为了确认结果而再次 Read 同一文件，更不要切到 `/tmp/cc-agent/...` 路径验证；只有结果本身明确报错或不完整时才继续调用工具。"
		}
		return "以下内容是上一轮工具调用刚返回的成功结果。若 Write/Edit 已经成功，就优先直接向用户总结已完成的修改，不要仅为了确认结果而再次 Read 同一文件，更不要切到 `/tmp/cc-agent/...` 路径验证；只有结果本身明确报错或不完整时才继续调用工具。"
	}
	if task != "" {
		return "继续完成用户刚才明确提出的任务：" + task + "。以下内容是上一轮工具调用刚返回的结果。请直接基于这些结果继续执行；如果这个任务需要修改或创建代码，就继续调用 Edit/Write/Bash 完成，不要只停在现状总结，不要重新打招呼，也不要把它当成新的空白任务。如果目标文件已经存在或你刚刚读过它，优先继续调用 Edit 做最小修改；只有在创建新文件，或你明确要整文件重写时才使用 Write。不要先输出说明文字，一旦决定调用工具，本回合第一个非空输出字符应当直接是 `{`。"
	}

	return "以下内容是上一轮工具调用刚返回的结果。请直接基于这些结果继续当前任务；如果这个任务需要修改或创建代码，就继续调用 Edit/Write/Bash 完成，不要只停在现状总结，不要重新打招呼，也不要把它当成新的空白任务。如果目标文件已经存在或你刚刚读过它，优先继续调用 Edit 做最小修改；只有在创建新文件，或你明确要整文件重写时才使用 Write。不要先输出说明文字，一旦决定调用工具，本回合第一个非空输出字符应当直接是 `{`。"
}

func truncateBoltContinuationTask(text string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" || limit <= 0 {
		return text
	}

	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
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
	}
	return ""
}

func buildBoltPathAliases(path string) map[string]struct{} {
	aliases := make(map[string]struct{})
	trimmed := strings.Trim(strings.TrimSpace(path), "\"'`")
	if trimmed == "" {
		return aliases
	}
	canonical := strings.ToLower(strings.ReplaceAll(trimmed, "\\", "/"))
	aliases[canonical] = struct{}{}
	base := strings.TrimSpace(filepath.Base(filepath.Clean(trimmed)))
	if base != "" && base != "." && base != string(filepath.Separator) {
		aliases[strings.ToLower(strings.ReplaceAll(base, "\\", "/"))] = struct{}{}
	}
	return aliases
}

func sharesBoltPathAlias(left, right map[string]struct{}) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for alias := range left {
		if _, ok := right[alias]; ok {
			return true
		}
	}
	return false
}

func isBoltMutationTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "Write", "Edit":
		return true
	default:
		return false
	}
}

func isBoltToolResultError(block prompt.ContentBlock, text string) bool {
	if block.IsError {
		return true
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"<tool_use_error>",
		"error editing file",
		"string to replace not found",
		"failed to edit",
		"edit failed",
		"hook pretooluse",
		"denied this tool",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isBoltToolResultSuccess(toolName, text string) bool {
	if !isBoltMutationTool(toolName) {
		return false
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"updated successfully",
		"created successfully",
		"written successfully",
		"wrote ",
		"applied successfully",
		"has been updated",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func compactBoltToolResultText(toolMeta boltToolUseMetadata, text string, focusedFileAliases map[string]struct{}) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	switch strings.TrimSpace(toolMeta.Name) {
	case "Read":
		if sharesBoltPathAlias(toolMeta.Aliases, focusedFileAliases) {
			return truncateBoltToolResultRunes(text, maxBoltFocusedReadRunes, "\n...[truncated read output for active file; call Read again if more context is needed]")
		}
		return truncateBoltToolResultRunes(text, maxBoltReadResultRunes, "\n...[truncated read output; call Read again if more context is needed]")
	case "Bash", "Grep":
		return truncateBoltToolResultRunes(text, maxBoltShellResultRunes, "\n...[truncated command output; rerun the tool if more context is needed]")
	default:
		return text
	}
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

type outboundConverter struct {
	model                   string
	inCodeBlock             bool
	codeBuffer              strings.Builder
	codeFenceHeader         strings.Builder
	awaitingCodeFenceHeader bool
	inputTokens             int
	outputTokens            int
	emittedToolUse          bool
	seenToolCalls           map[string]struct{}
	suppressText            bool
}

func newOutboundConverter(model string, inputTokens int) *outboundConverter {
	return &outboundConverter{
		model:         model,
		inputTokens:   inputTokens,
		seenToolCalls: make(map[string]struct{}),
	}
}

func (c *outboundConverter) ProcessStream(reader io.Reader, logger *debug.Logger, writer func(upstream.SSEMessage) error) error {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var textBuffer strings.Builder
	var pendingEndEvent *EndEvent
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 {
			continue
		}
		eventType := line[:colonIdx]
		eventData := line[colonIdx+1:]
		if logger != nil {
			logger.LogUpstreamSSE(eventType, eventData)
		}

		switch eventType {
		case "0":
			if err := c.processChunkData(eventData, &textBuffer, writer, true); err != nil {
				return err
			}
			if c.emittedToolUse {
				return c.finishImmediatelyAfterToolUse(writer)
			}
		case "8":
			continue
		case "9", "a":
			if err := c.processChunkData(eventData, &textBuffer, writer, false); err != nil {
				return err
			}
			if c.emittedToolUse {
				return c.finishImmediatelyAfterToolUse(writer)
			}
		case "e":
			endEvent, err := c.parseEndEvent(eventData)
			if err != nil {
				continue
			}
			pendingEndEvent = endEvent
			continue
		case "d":
			endEvent, err := c.parseEndEvent(eventData)
			if err != nil {
				endEvent = pendingEndEvent
			}
			if endEvent == nil {
				endEvent = pendingEndEvent
			}
			if endEvent == nil {
				continue
			}
			return c.flushAndFinish(endEvent, &textBuffer, writer)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	if pendingEndEvent != nil {
		return c.flushAndFinish(pendingEndEvent, &textBuffer, writer)
	}
	return nil
}

func (c *outboundConverter) parseEndEvent(data string) (*EndEvent, error) {
	var event EndEvent
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil, err
	}
	return &event, nil
}

func (c *outboundConverter) processChunkData(data string, textBuffer *strings.Builder, writer func(upstream.SSEMessage) error, allowRawFallback bool) error {
	switch firstNonSpaceByte(data) {
	case '"':
		var text string
		if err := json.Unmarshal([]byte(data), &text); err != nil {
			if allowRawFallback {
				return c.processTextContent(strings.TrimSpace(data), textBuffer, writer)
			}
			return nil
		}
		text = strings.ReplaceAll(text, "626f6c742d63632d6167656e74", "")
		return c.processTextContent(text, textBuffer, writer)
	case '{', '[':
		var value interface{}
		if err := json.Unmarshal([]byte(data), &value); err != nil {
			if allowRawFallback {
				return c.processTextContent(strings.TrimSpace(data), textBuffer, writer)
			}
			return nil
		}
		return c.processStructuredValue(value, textBuffer, writer, allowRawFallback)
	default:
		if allowRawFallback {
			return c.processTextContent(strings.TrimSpace(data), textBuffer, writer)
		}
		return nil
	}
}

func (c *outboundConverter) processStructuredValue(value interface{}, textBuffer *strings.Builder, writer func(upstream.SSEMessage) error, allowRawFallback bool) error {
	if toolCalls := extractToolCallsFromValue(value); len(toolCalls) > 0 {
		return c.flushTextAndSendToolCalls(toolCalls, textBuffer, writer)
	}

	switch v := value.(type) {
	case string:
		return c.processTextContent(v, textBuffer, writer)
	case []interface{}:
		for _, item := range v {
			if err := c.processStructuredValue(item, textBuffer, writer, allowRawFallback); err != nil {
				return err
			}
		}
		return nil
	case map[string]interface{}:
		for _, key := range boltStructuredNestedKeys {
			if nested, ok := v[key]; ok {
				switch nested.(type) {
				case []interface{}, map[string]interface{}:
					if err := c.processStructuredValue(nested, textBuffer, writer, false); err != nil {
						return err
					}
				}
			}
		}
		for _, key := range boltStructuredStringKeys {
			if raw, ok := v[key].(string); ok && strings.TrimSpace(raw) != "" {
				return c.processTextContent(raw, textBuffer, writer)
			}
		}
	}

	if !allowRawFallback {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return c.processTextContent(string(raw), textBuffer, writer)
}

func (c *outboundConverter) processTextContent(text string, textBuffer *strings.Builder, writer func(upstream.SSEMessage) error) error {
	if c.suppressText {
		return nil
	}
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		if toolCalls := extractToolCallsFromJSON([]byte(trimmed)); len(toolCalls) > 0 {
			return c.flushTextAndSendToolCalls(toolCalls, textBuffer, writer)
		}
	}

	if !c.inCodeBlock {
		idx := strings.Index(text, "```")
		if idx >= 0 {
			beforeBlock := text[:idx]
			afterMarker := text[idx+3:]
			textBuffer.WriteString(beforeBlock)
			if textBuffer.Len() > 0 {
				if err := c.sendTextDelta(textBuffer.String(), writer); err != nil {
					return err
				}
				textBuffer.Reset()
			}
			c.inCodeBlock = true
			c.awaitingCodeFenceHeader = true
			c.codeFenceHeader.Reset()
			c.codeBuffer.Reset()
			text = afterMarker
		}
	}

	if c.inCodeBlock {
		if idx := strings.Index(text, "```"); idx >= 0 {
			beforeEnd := text[:idx]
			afterEnd := text[idx+3:]
			c.appendCodeBlockContent(beforeEnd)
			codeContent := strings.Trim(c.codeBuffer.String(), "\r\n")
			fenceHeader := strings.TrimSpace(c.codeFenceHeader.String())
			c.codeBuffer.Reset()
			c.codeFenceHeader.Reset()
			c.inCodeBlock = false
			c.awaitingCodeFenceHeader = false
			if err := c.processCodeBlock(fenceHeader, codeContent, textBuffer, writer); err != nil {
				return err
			}
			if afterEnd != "" {
				textBuffer.WriteString(afterEnd)
			}
		} else {
			c.appendCodeBlockContent(text)
		}
		return nil
	}

	textBuffer.WriteString(text)
	return nil
}

func (c *outboundConverter) appendCodeBlockContent(text string) {
	if !c.awaitingCodeFenceHeader {
		c.codeBuffer.WriteString(text)
		return
	}
	if text == "" {
		return
	}

	if idx := strings.IndexAny(text, "\r\n"); idx >= 0 {
		c.codeFenceHeader.WriteString(text[:idx])
		c.awaitingCodeFenceHeader = false
		next := idx + 1
		if text[idx] == '\r' && next < len(text) && text[next] == '\n' {
			next++
		}
		if next < len(text) {
			c.codeBuffer.WriteString(text[next:])
		}
		return
	}

	c.codeFenceHeader.WriteString(text)
}

func (c *outboundConverter) processCodeBlock(fenceHeader string, content string, textBuffer *strings.Builder, writer func(upstream.SSEMessage) error) error {
	if toolCalls := extractToolCallsFromJSON([]byte(content)); len(toolCalls) > 0 {
		return c.flushTextAndSendToolCalls(toolCalls, textBuffer, writer)
	}

	textBuffer.WriteString("```")
	if fenceHeader != "" {
		textBuffer.WriteString(fenceHeader)
	}
	textBuffer.WriteByte('\n')
	textBuffer.WriteString(content)
	textBuffer.WriteString("\n```")
	return nil
}

func (c *outboundConverter) sendTextDelta(text string, writer func(upstream.SSEMessage) error) error {
	text = sanitizeBoltAssistantText(text)
	if text == "" {
		return nil
	}
	return writeBoltStreamMessage(writer, "model.text-delta", map[string]interface{}{
		"delta": text,
	})
}

func (c *outboundConverter) sendToolUse(toolCall *ToolCall, writer func(upstream.SSEMessage) error) error {
	if toolCall == nil || strings.TrimSpace(toolCall.Function) == "" {
		return nil
	}
	params := normalizeToolCallParameters(toolCall.Parameters)
	key := toolCall.Function + "\x00" + string(params)
	if _, exists := c.seenToolCalls[key]; exists {
		return nil
	}
	c.seenToolCalls[key] = struct{}{}
	c.emittedToolUse = true
	c.suppressText = true
	return writeBoltStreamMessage(writer, "model.tool-call", map[string]interface{}{
		"toolCallId": "toolu_" + generateRandomID(20),
		"toolName":   strings.TrimSpace(toolCall.Function),
		"input":      string(params),
	})
}

func (c *outboundConverter) flushTextAndSendToolCalls(toolCalls []ToolCall, textBuffer *strings.Builder, writer func(upstream.SSEMessage) error) error {
	if textBuffer != nil {
		// Prefer a pure tool-use turn when Bolt mixes narration with tool calls.
		textBuffer.Reset()
	}
	for i := range toolCalls {
		if err := c.sendToolUse(&toolCalls[i], writer); err != nil {
			return err
		}
	}
	return nil
}

func (c *outboundConverter) flushTextBuffer(textBuffer *strings.Builder, writer func(upstream.SSEMessage) error) error {
	if textBuffer == nil || textBuffer.Len() == 0 {
		return nil
	}
	if err := c.sendTextDelta(textBuffer.String(), writer); err != nil {
		return err
	}
	textBuffer.Reset()
	return nil
}

func extractToolCallsFromJSON(data []byte) []ToolCall {
	var value interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		return nil
	}
	return extractToolCallsFromValue(value)
}

func extractToolCallsFromValue(value interface{}) []ToolCall {
	var calls []ToolCall
	appendToolCallsFromValue(&calls, value)
	return calls
}

func appendToolCallsFromValue(calls *[]ToolCall, value interface{}) {
	switch v := value.(type) {
	case []interface{}:
		for _, item := range v {
			appendToolCallsFromValue(calls, item)
		}
	case map[string]interface{}:
		for _, key := range boltToolCallCollectionKeys {
			if nested, ok := v[key]; ok {
				appendToolCallsFromValue(calls, nested)
				if len(*calls) > 0 {
					return
				}
			}
		}
		if call, ok := parseToolCallValue(v); ok {
			*calls = append(*calls, call)
		}
	}
}

func parseToolCallValue(v map[string]interface{}) (ToolCall, bool) {
	typeHint := strings.ToLower(strings.TrimSpace(stringValue(v["type"])))

	if toolName := strings.TrimSpace(stringValue(v["tool"])); toolName != "" {
		return ToolCall{Function: toolName, Parameters: normalizeToolCallParameters(firstNonNil(v["parameters"], v["params"], v["arguments"], v["args"], v["input"]))}, true
	}

	if functionValue, ok := v["function"].(map[string]interface{}); ok {
		if toolName := strings.TrimSpace(stringValue(functionValue["name"])); toolName != "" {
			args := firstNonNil(functionValue["parameters"], functionValue["arguments"], v["parameters"], v["arguments"], v["args"], v["input"])
			return ToolCall{Function: toolName, Parameters: normalizeToolCallParameters(args)}, true
		}
	}

	name := strings.TrimSpace(stringValue(v["name"]))
	if name == "" {
		name = strings.TrimSpace(stringValue(v["function"]))
	}
	if name == "" {
		name = strings.TrimSpace(stringValue(v["toolName"]))
	}
	if name == "" {
		return ToolCall{}, false
	}

	args := firstNonNil(v["parameters"], v["params"], v["arguments"], v["args"], v["input"])
	if args == nil && typeHint != "tool_use" && typeHint != "tool_call" && typeHint != "function" {
		return ToolCall{}, false
	}

	return ToolCall{Function: name, Parameters: normalizeToolCallParameters(args)}, true
}

func firstNonNil(values ...interface{}) interface{} {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func stringValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return ""
	}
}

func normalizeToolCallParameters(value interface{}) json.RawMessage {
	switch v := value.(type) {
	case nil:
		return json.RawMessage("{}")
	case json.RawMessage:
		if len(v) == 0 {
			return json.RawMessage("{}")
		}
		return v
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return json.RawMessage("{}")
		}
		if json.Valid([]byte(trimmed)) {
			return json.RawMessage(trimmed)
		}
	}

	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || string(data) == "null" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(data)
}

func (c *outboundConverter) sendEndEvents(endEvent *EndEvent, writer func(upstream.SSEMessage) error) error {
	c.outputTokens = endEvent.Usage.CompletionTokens
	finishReason := "end_turn"
	if c.emittedToolUse || strings.EqualFold(strings.TrimSpace(endEvent.FinishReason), "tool_use") {
		finishReason = "tool_use"
	}

	return writeBoltStreamMessage(writer, "model.finish", map[string]interface{}{
		"finishReason": finishReason,
		"usage": map[string]interface{}{
			"inputTokens":   c.inputTokens,
			"outputTokens":  c.outputTokens,
			"input_tokens":  c.inputTokens,
			"output_tokens": c.outputTokens,
		},
	})
}

func (c *outboundConverter) finishImmediatelyAfterToolUse(writer func(upstream.SSEMessage) error) error {
	return writeBoltStreamMessage(writer, "model.finish", map[string]interface{}{
		"finishReason": "tool_use",
		"usage": map[string]interface{}{
			"inputTokens":   c.inputTokens,
			"outputTokens":  c.outputTokens,
			"input_tokens":  c.inputTokens,
			"output_tokens": c.outputTokens,
		},
	})
}

func (c *outboundConverter) flushAndFinish(endEvent *EndEvent, textBuffer *strings.Builder, writer func(upstream.SSEMessage) error) error {
	if c.inCodeBlock && c.codeBuffer.Len() > 0 {
		codeContent := strings.Trim(c.codeBuffer.String(), "\r\n")
		fenceHeader := strings.TrimSpace(c.codeFenceHeader.String())
		c.codeBuffer.Reset()
		c.codeFenceHeader.Reset()
		c.inCodeBlock = false
		c.awaitingCodeFenceHeader = false
		if err := c.processCodeBlock(fenceHeader, codeContent, textBuffer, writer); err != nil {
			return err
		}
	}
	if err := c.flushTextBuffer(textBuffer, writer); err != nil {
		return err
	}
	return c.sendEndEvents(endEvent, writer)
}

func writeBoltStreamMessage(writer func(upstream.SSEMessage) error, eventType string, eventMap map[string]interface{}) error {
	if writer == nil {
		return nil
	}
	raw, err := json.Marshal(eventMap)
	if err != nil {
		return err
	}
	return writer(upstream.SSEMessage{
		Type:    eventType,
		Event:   eventMap,
		RawJSON: raw,
	})
}

func firstNonSpaceByte(text string) byte {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return text[i]
		}
	}
	return 0
}
