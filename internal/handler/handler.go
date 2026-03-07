package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	rtdebug "runtime/debug"
	"strings"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/adapter"
	"orchids-api/internal/audit"
	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	apperrors "orchids-api/internal/errors"
	"orchids-api/internal/loadbalancer"
	"orchids-api/internal/orchids"
	"orchids-api/internal/prompt"
	"orchids-api/internal/store"
	"orchids-api/internal/tokencache"
	"orchids-api/internal/upstream"
	"orchids-api/internal/util"
	"orchids-api/internal/warp"
)

// ClientFactory creates an UpstreamClient for a given account.
// Used to decouple provider-specific client construction from the handler.
type ClientFactory func(acc *store.Account, cfg *config.Config) UpstreamClient

type Handler struct {
	config        *config.Config
	client        UpstreamClient
	clientFactory ClientFactory
	clientCache   *accountClientCache
	loadBalancer  *loadbalancer.LoadBalancer
	tokenCache    tokencache.Cache
	auditLogger   audit.Logger

	sessionStore SessionStore
	dedupStore   DedupStore
}

type UpstreamClient interface {
	SendRequest(ctx context.Context, prompt string, chatHistory []interface{}, model string, onMessage func(upstream.SSEMessage), logger *debug.Logger) error
}

type UpstreamPayloadClient interface {
	SendRequestWithPayload(ctx context.Context, req upstream.UpstreamRequest, onMessage func(upstream.SSEMessage), logger *debug.Logger) error
}

type ClaudeRequest struct {
	Model          string                 `json:"model"`
	Messages       []prompt.Message       `json:"messages"`
	System         SystemItems            `json:"system"`
	Tools          []interface{}          `json:"tools"`
	Stream         bool                   `json:"stream"`
	ConversationID string                 `json:"conversation_id"`
	Metadata       map[string]interface{} `json:"metadata"`
}

type toolCall struct {
	id    string
	name  string
	input string
}

const keepAliveInterval = 15 * time.Second
const maxRequestBytes = 50 * 1024 * 1024 // 50MB
const duplicateWindow = 2 * time.Second
const duplicateCleanupWindow = 10 * time.Second

type recentRequest struct {
	last     time.Time
	inFlight int
}

func NewWithLoadBalancer(cfg *config.Config, lb *loadbalancer.LoadBalancer) *Handler {
	h := &Handler{
		config:       cfg,
		loadBalancer: lb,
		clientCache:  newAccountClientCache(),
		sessionStore: NewMemorySessionStore(30*time.Minute, 1024),
		dedupStore:   NewMemoryDedupStore(duplicateWindow, duplicateCleanupWindow),
		auditLogger:  audit.NewNopLogger(),
	}
	if cfg != nil {
		h.client = orchids.New(cfg)
	}

	return h
}

func (h *Handler) SetTokenCache(cache tokencache.Cache) {
	h.tokenCache = cache
}

// SetSessionStore replaces the default in-memory session store.
func (h *Handler) SetSessionStore(ss SessionStore) {
	h.sessionStore = ss
}

// SetDedupStore replaces the default in-memory dedup store.
func (h *Handler) SetDedupStore(ds DedupStore) {
	h.dedupStore = ds
}

// SetAuditLogger replaces the default nop audit logger.
func (h *Handler) SetAuditLogger(al audit.Logger) {
	h.auditLogger = al
}

// SetClientFactory sets the factory used by selectAccount to create provider-specific clients.
func (h *Handler) SetClientFactory(f ClientFactory) {
	h.clientFactory = f
}

func (h *Handler) computeRequestHash(r *http.Request, body []byte) string {
	hasher := sha256.New()
	hasher.Write([]byte(r.URL.Path))
	hasher.Write([]byte{0})
	if auth := r.Header.Get("Authorization"); auth != "" {
		hasher.Write([]byte(auth))
	}
	hasher.Write([]byte{0})
	hasher.Write(body)
	return hex.EncodeToString(hasher.Sum(nil))
}

func (h *Handler) computeSemanticRequestHash(r *http.Request, req ClaudeRequest) string {
	userText := normalizeTopicText(extractUserText(req.Messages))
	if userText == "" {
		return ""
	}
	if len(userText) > 4096 {
		userText = userText[:4096]
	}

	mode := "chat"
	if isTopicClassifierRequest(req) {
		mode = "topic_classifier"
	} else if ok, _ := isCommandPrefixRequest(req); ok {
		mode = "command_prefix"
	}

	hasher := sha256.New()
	hasher.Write([]byte(r.URL.Path))
	hasher.Write([]byte{0})
	if auth := r.Header.Get("Authorization"); auth != "" {
		hasher.Write([]byte(auth))
	}
	hasher.Write([]byte{0})
	hasher.Write([]byte(strings.ToLower(strings.TrimSpace(req.Model))))
	hasher.Write([]byte{0})
	hasher.Write([]byte(strings.ToLower(strings.TrimSpace(conversationKeyForRequest(r, req)))))
	hasher.Write([]byte{0})
	hasher.Write([]byte(mode))
	hasher.Write([]byte{0})
	if req.Stream {
		hasher.Write([]byte{1})
	} else {
		hasher.Write([]byte{0})
	}
	hasher.Write([]byte{0})
	hasher.Write([]byte(userText))
	return hex.EncodeToString(hasher.Sum(nil))
}

func (h *Handler) registerRequest(hash string) (bool, bool) {
	return h.dedupStore.Register(context.Background(), hash)
}

func (h *Handler) finishRequest(hash string) {
	h.dedupStore.Finish(context.Background(), hash)
}

func (h *Handler) writeDuplicateResponse(w http.ResponseWriter, req ClaudeRequest) {
	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		msgStart, _ := marshalSSEMessageStartNoUsageBytes("dup", req.Model)
		_ = writeSSEFrameBytes(w, "message_start", msgStart)
		_ = writeSSEFrameBytes(w, "message_stop", sseMessageStopBytes)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(struct {
		Type     string `json:"type"`
		Deduped  bool   `json:"deduped"`
		Message  string `json:"message"`
		Model    string `json:"model"`
		Streamed bool   `json:"streamed"`
	}{
		Type:     "duplicate_request",
		Deduped:  true,
		Message:  "duplicate request suppressed",
		Model:    req.Model,
		Streamed: false,
	}); err != nil {
		slog.Error("Failed to write duplicate response", "error", err)
	}
}

func (h *Handler) HandleMessages(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	streamingStarted := false

	defer func() {
		if err := recover(); err != nil {
			stack := string(rtdebug.Stack())
			slog.Error("Panic in HandleMessages", "error", err, "stack", stack)
			if streamingStarted {
				// Headers already sent — write an SSE error event instead of HTTP error
				// Pre-compiled zero-allocation string
				fmt.Fprintf(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"message\":\"Internal Server Error\"}}\n\n")
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			} else {
				apperrors.New("server_error", "Internal Server Error", http.StatusInternalServerError).WriteResponse(w)
			}
		}
	}()

	if r.Method != http.MethodPost {
		apperrors.New("invalid_request_error", "Method not allowed", http.StatusMethodNotAllowed).WriteResponse(w)
		return
	}

	var req ClaudeRequest
	if maxRequestBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		if maxRequestBytes > 0 {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				apperrors.New("invalid_request_error", "Request body too large", http.StatusRequestEntityTooLarge).WriteResponse(w)
				return
			}
		}
		apperrors.New("invalid_request_error", "Invalid request body", http.StatusBadRequest).WriteResponse(w)
		return
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		apperrors.New("invalid_request_error", "Invalid request body", http.StatusBadRequest).WriteResponse(w)
		return
	}

	// 初始化调试日志
	logger := debug.New(h.config.DebugEnabled, h.config.DebugLogSSE)
	defer logger.Close()

	// 1. 记录进入的 Claude 请求
	logger.LogIncomingRequest(req)

	reqHash := h.computeRequestHash(r, bodyBytes)
	semanticHash := h.computeSemanticRequestHash(r, req)
	slog.Debug("Request fingerprint", "hash", reqHash, "semantic_hash", semanticHash, "path", r.URL.Path, "content_length", len(bodyBytes), "retry", r.Header.Get("X-Stainless-Retry-Count"))

	exactKey := "exact:" + reqHash
	registeredKeys := []string{}
	if dup, inFlight := h.registerRequest(exactKey); dup {
		slog.Warn("Duplicate request suppressed", "hash", reqHash, "in_flight", inFlight, "path", r.URL.Path, "user_agent", r.UserAgent())
		logger.LogEarlyExit("duplicate_request", map[string]interface{}{
			"hash":      exactKey,
			"in_flight": inFlight,
			"path":      r.URL.Path,
			"kind":      "exact",
		})
		h.writeDuplicateResponse(w, req)
		return
	}
	registeredKeys = append(registeredKeys, exactKey)

	if semanticHash != "" {
		semanticKey := "semantic:" + semanticHash
		if dup, inFlight := h.registerRequest(semanticKey); dup {
			for i := len(registeredKeys) - 1; i >= 0; i-- {
				h.finishRequest(registeredKeys[i])
			}
			slog.Warn("Semantic duplicate request suppressed", "hash", semanticHash, "in_flight", inFlight, "path", r.URL.Path, "user_agent", r.UserAgent())
			logger.LogEarlyExit("duplicate_request", map[string]interface{}{
				"hash":      semanticKey,
				"in_flight": inFlight,
				"path":      r.URL.Path,
				"kind":      "semantic",
			})
			h.writeDuplicateResponse(w, req)
			return
		}
		registeredKeys = append(registeredKeys, semanticKey)
	}
	defer func() {
		for i := len(registeredKeys) - 1; i >= 0; i-- {
			h.finishRequest(registeredKeys[i])
		}
	}()

	// ...
	if ok, command := isCommandPrefixRequest(req); ok {
		slog.Debug("Handling command prefix request", "command", command)
		prefix := detectCommandPrefix(command)
		logger.LogEarlyExit("command_prefix", map[string]interface{}{
			"command": command,
			"prefix":  prefix,
		})
		writeCommandPrefixResponse(w, req, prefix, startTime, logger)
		return
	}

	if isTopicClassifierRequest(req) {
		slog.Debug("Handling topic classifier request locally")
		logger.LogEarlyExit("topic_classifier", map[string]interface{}{
			"mode": "local",
		})
		writeTopicClassifierResponse(w, req, startTime, logger)
		return
	}

	cacheStrategy := h.config.CacheStrategy
	if cacheStrategy != "" && cacheStrategy != "none" {
		applyCacheStrategy(&req, cacheStrategy)
	}

	// Debug: log all headers
	for k, v := range r.Header {
		slog.Debug("Incoming header V2 CHECK", "key", k, "value", v)
	}

	// Context and Conversation Key
	conversationKey := conversationKeyForRequest(r, req)

	forcedChannel := channelFromPath(r.URL.Path)
	validatedModel, err := h.validateModelAvailability(r.Context(), req.Model, forcedChannel)
	if err != nil {
		apperrors.New("invalid_request_error", err.Error(), http.StatusBadRequest).WriteResponse(w)
		return
	}
	targetChannel := strings.TrimSpace(forcedChannel)
	if targetChannel == "" && validatedModel != nil {
		targetChannel = strings.TrimSpace(validatedModel.Channel)
		if targetChannel == "" {
			targetChannel = "orchids"
		}
	}
	effectiveWorkdir, prevWorkdir, workdirChanged := h.resolveWorkdir(r, req, conversationKey)
	if workdirChanged {
		slog.Warn("检测到工作目录变化，已清空历史", "prev", prevWorkdir, "next", effectiveWorkdir, "session", conversationKey)
		req.Messages = resetMessagesForNewWorkdir(req.Messages)
		// 工作目录变化时清除上游会话ID，强制开启新对话
		if conversationKey != "" {
			h.sessionStore.DeleteSession(r.Context(), conversationKey)
		}
	}

	// 选择账号 (Initial Selection)
	failedAccountIDs := []int64{}
	failedAccountSet := make(map[int64]struct{})

	apiClient, currentAccount, err := h.selectAccount(r.Context(), targetChannel, forcedChannel != "", failedAccountIDs)
	if err != nil {
		slog.Error("selectAccount failed", "error", err, "channel", targetChannel)
		logger.LogEarlyExit("select_account_failed", map[string]interface{}{
			"error":   err.Error(),
			"model":   req.Model,
			"channel": targetChannel,
		})
		apperrors.New("overloaded_error", err.Error(), http.StatusServiceUnavailable).WriteResponse(w)
		return
	}
	slog.Debug("Checkpoint: selectAccount success")

	// 捕获账号快照，用于请求结束后检测 forceRefreshToken 是否更新了账号信息
	var accountSnapshot *store.Account
	if currentAccount != nil {
		snap := *currentAccount
		accountSnapshot = &snap
	}

	isWarpRequest := strings.EqualFold(forcedChannel, "warp")
	if currentAccount != nil && strings.EqualFold(currentAccount.AccountType, "warp") {
		isWarpRequest = true
	}
	if isWarpRequest {
		// Warp passthrough mode: do not trim history/tool results.
		slog.Debug("Checkpoint: warp passthrough, skip trim/sanitize")
	} else {
		// Orchids: do not trim message/tool_result content to preserve full context.
		slog.Debug("Checkpoint: orchids passthrough, skip context trimming")
		if sanitized, changed := sanitizeSystemItems(req.System, false, h.config); changed {
			req.System = sanitized
			slog.Info("系统提示已移除 cc_entrypoint", "mode", h.config.OrchidsCCEntrypointMode, "warp", false)
		}
	}
	slog.Debug("Checkpoint: message processing done")

	// 手动管理连接计数，账号切换时需要释放旧账号、获取新账号
	trackedAccountID := int64(0)
	if currentAccount != nil && h.loadBalancer != nil {
		h.loadBalancer.AcquireConnection(currentAccount.ID)
		trackedAccountID = currentAccount.ID
	}
	defer func() {
		if trackedAccountID != 0 && h.loadBalancer != nil {
			h.loadBalancer.ReleaseConnection(trackedAccountID)
		}
	}()

	suggestionMode := isSuggestionMode(req.Messages)
	noThinking := suggestionMode || h.config.SuppressThinking
	gateNoTools := false
	suppressThinking := noThinking
	if suggestionMode {
		gateNoTools = true
	}
	if lastUserIsToolResultOnly(req.Messages) {
		gateNoTools = true
		if h.config.DebugEnabled {
			slog.Debug("tool_gate: disabled tools for tool_result-only follow-up")
		}
	}
	effectiveTools := req.Tools
	if h.config.WarpDisableTools != nil && *h.config.WarpDisableTools {
		effectiveTools = nil
	}
	if gateNoTools {
		effectiveTools = nil
		slog.Debug("tool_gate: disabled tools for short non-code request")
	}

	// 构建 prompt（V2 Markdown 格式）
	startBuild := time.Now()
	slog.Debug("Starting prompt build...", "conversation_id", conversationKey)
	// Orchids: always use AIClient mode (other implementations are deprecated/removed).
	isOrchidsAIClient := false
	if _, ok := apiClient.(*orchids.Client); ok {
		isOrchidsAIClient = true
	}

	// 映射模型（用于上游请求与提示一致）
	mappedModel := mapModel(req.Model)
	if currentAccount != nil && strings.EqualFold(currentAccount.AccountType, "warp") {
		mappedModel = req.Model
	}

	var aiClientHistory []map[string]string
	var builtPrompt string
	var promptMeta orchids.AIClientPromptMeta
	builtPrompt, aiClientHistory, promptMeta = orchids.BuildAIClientPromptAndHistoryWithMeta(req.Messages, req.System, mappedModel, noThinking, effectiveWorkdir, h.config.ContextMaxTokens)
	buildDuration := time.Since(startBuild)
	slog.Debug("Prompt build completed", "duration", buildDuration)
	if h.config.DebugEnabled {
		buildLabel := "BuildAIClientPromptAndHistory"
		slog.Info("[Performance] "+buildLabel, "duration", buildDuration)
		// Project context injection is deprecated (non-AIClient path removed).
	}

	slog.Debug("Model mapping", "original", req.Model, "mapped", mappedModel)

	isStream := req.Stream

	if isStream {
		// 设置 SSE 响应头
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		if _, ok := w.(http.Flusher); !ok {
			apperrors.New("api_error", "Streaming not supported by underlying connection", http.StatusInternalServerError).WriteResponse(w)
			return
		}
		streamingStarted = true
	} else {
		w.Header().Set("Content-Type", "application/json")
	}

	// 状态管理
	// msgID is now managed by streamHandler

	var chatHistory []interface{}
	upstreamMessages := append([]prompt.Message(nil), req.Messages...)

	// Pre-allocate chatHistory
	if isOrchidsAIClient {
		chatHistory = make([]interface{}, len(aiClientHistory))
		for i := range aiClientHistory {
			chatHistory[i] = aiClientHistory[i]
		}
	} else {
		chatHistory = make([]interface{}, 0, 10)
	}

	if gateNoTools {
		builtPrompt = injectToolGate(builtPrompt, "This is a short, non-code request. Do NOT call tools or perform any file operations. Answer directly.")
	}

	// 2. 记录转换后的 prompt
	slog.Debug("Checkpoint: LogConvertedPrompt")
	logger.LogConvertedPrompt(builtPrompt)

	breakdown := estimateInputTokenBreakdown(builtPrompt, aiClientHistory, effectiveTools)
	slog.Debug(
		"Input token breakdown (estimated)",
		"prompt_profile", promptMeta.Profile,
		"base_prompt_tokens", breakdown.BasePromptTokens,
		"system_context_tokens", breakdown.SystemContextTokens,
		"history_tokens", breakdown.HistoryTokens,
		"tools_tokens", breakdown.ToolsTokens,
		"estimated_total_input_tokens", breakdown.Total,
	)

	// Token 计数（用于前置 usage 展示）
	inputTokens := breakdown.Total
	if inputTokens <= 0 {
		inputTokens = h.estimateInputTokens(r.Context(), req.Model, builtPrompt)
	}

	// Detect Response Format (Anthropic vs OpenAI)
	responseFormat := adapter.DetectResponseFormat(r.URL.Path)

	sh := newStreamHandler(
		h.config, w, logger, suppressThinking, isStream, responseFormat, effectiveWorkdir,
	)
	sh.seedSideEffectDedupFromMessages(upstreamMessages)
	sh.setUsageTokens(inputTokens, -1) // Correctly initialize input tokens
	// 捕获上游返回的 conversationID，持久化到 session 以便后续请求复用
	sh.onConversationID = func(id string) {
		if conversationKey == "" {
			return
		}
		h.sessionStore.SetConvID(r.Context(), conversationKey, id)
		h.sessionStore.Touch(r.Context(), conversationKey)
		slog.Debug("Warp conversationID captured", "key", conversationKey, "id", id)
	}
	defer sh.release()

	// 发送 message_start
	sh.writeSSEMessageStart(req.Model, inputTokens, 0)

	slog.Debug("New request received")

	// KeepAlive
	var keepAliveStop chan struct{}
	if isStream {
		keepAliveStop = make(chan struct{})
		defer close(keepAliveStop)
		ticker := time.NewTicker(keepAliveInterval)
		go func() {
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					sh.mu.Lock()
					done := sh.hasReturn
					sh.mu.Unlock()
					if done {
						return
					}
					sh.writeKeepAlive()
				case <-keepAliveStop:
					return
				case <-r.Context().Done():
					return
				}
			}
		}()
	}

	// Main execution
	run := func() {
		// 复用上游返回的 conversationID，保持会话连续性
		chatSessionID := ""
		if conversationKey != "" {
			chatSessionID, _ = h.sessionStore.GetConvID(r.Context(), conversationKey)
			h.sessionStore.Touch(r.Context(), conversationKey)
		}
		if chatSessionID == "" {
			chatSessionID = "chat_" + randomSessionID()
		}
		maxRetries := h.config.MaxRetries
		if maxRetries < 0 {
			maxRetries = 0
		}
		retryDelay := time.Duration(h.config.RetryDelay) * time.Millisecond
		retriesRemaining := maxRetries

		payloadMessages := upstreamMessages
		payloadSystem := req.System

		upstreamReq := upstream.UpstreamRequest{
			Prompt:        builtPrompt,
			ChatHistory:   chatHistory,
			Workdir:       effectiveWorkdir,
			Model:         mappedModel,
			Messages:      payloadMessages,
			System:        payloadSystem,
			Tools:         effectiveTools,
			NoTools:       gateNoTools,
			NoThinking:    noThinking,
			ChatSessionID: chatSessionID,
		}
		var attempt int
		for {
			if attempt > 0 {
				// 非首次尝试：向客户端发送重试提示，避免前一次不完整内容造成混淆
				sh.emitTextBlock("\n\n[Retrying request...]\n\n")
			}
			sh.resetRoundState()
			var err error
			slog.Debug("Calling Upstream Client...", "attempt", attempt+1)

			if h.config != nil && h.config.DebugEnabled {
				slog.Debug("Interface check", "type", fmt.Sprintf("%T", apiClient))
			}
			if sender, ok := apiClient.(UpstreamPayloadClient); ok {
				slog.Debug("Using SendRequestWithPayload")
				warpBatches := [][]prompt.Message{upstreamMessages}
				if isWarpRequest {
					// Enforce hard token budget for Warp requests to avoid runaway context cost.
					if _, isWarp := apiClient.(*warp.Client); isWarp {
						budget := h.config.ContextMaxTokens
						if budget <= 0 || budget > 12000 {
							budget = 12000
						}
						trimmed, before, after, compressed, summarized, dropped := enforceWarpBudget(builtPrompt, upstreamMessages, budget)
						if before.Total != after.Total || compressed > 0 || summarized > 0 || dropped > 0 {
							slog.Debug(
								"Warp budget applied",
								"budget", budget,
								"tokens_before", before.Total,
								"tokens_after", after.Total,
								"prompt_tokens", after.PromptTokens,
								"messages_tokens", after.MessagesTokens,
								"tool_tokens", after.ToolTokens,
								"compressed_blocks", compressed,
								"summarized_messages", summarized,
								"dropped_messages", dropped,
							)
						}
						upstreamMessages = trimmed
					}

					if h.config.WarpSplitToolResults {
						if _, isWarp := apiClient.(*warp.Client); isWarp {
							batches, total := splitWarpToolResults(upstreamMessages, 1)
							if len(batches) > 1 {
								slog.Debug("Warp tool results split", "total_tool_results", total, "batches", len(batches))
							}
							warpBatches = batches
						}
					}
				}
				noopHandler := func(msg upstream.SSEMessage) {
					if msg.Type == "error" {
						slog.Warn("Warp intermediate batch error", "event", msg.Event)
					}
				}
				for i, batch := range warpBatches {
					batchReq := upstreamReq
					batchReq.Messages = batch
					isLast := i == len(warpBatches)-1
					if isLast {
						err = sender.SendRequestWithPayload(r.Context(), batchReq, sh.handleMessage, logger)
					} else {
						err = sender.SendRequestWithPayload(r.Context(), batchReq, noopHandler, nil)
					}
					if err != nil {
						break
					}
				}
			} else {
				slog.Warn("Falling back to legacy SendRequest (Workdir lost!)", "type", fmt.Sprintf("%T", apiClient))
				err = apiClient.SendRequest(r.Context(), builtPrompt, chatHistory, mappedModel, sh.handleMessage, logger)
			}
			slog.Debug("Upstream Client Returned", "error", err)

			if err == nil {
				sh.forceFinishIfMissing()
				break
			}
			if sh.hasAnyOutput() {
				slog.Warn("Upstream failed after partial output, skip retry to avoid duplicated token billing", "error", err)
				sh.finishResponse("end_turn")
				return
			}

			// Check for non-retriable errors
			errStr := err.Error()
			errClass := classifyUpstreamError(errStr)
			slog.Error("Request error", "error", err, "category", errClass.Category, "retryable", errClass.Retryable)
			// 标记账号状态（auth 类错误始终标记，无论是否可重试）
			if currentAccount != nil && h.loadBalancer != nil && h.loadBalancer.Store != nil {
				if status := classifyAccountStatus(errStr); status != "" {
					// Mark status if it's auth-related OR if it's a 429 (rate limit)
					// We want to rotate accounts on 429 even if we retry the request on a new account
					if !errClass.Retryable || errClass.Category == "auth" || status == "429" {
						slog.Info("标记账号状态", "account_id", currentAccount.ID, "status", status, "category", errClass.Category)
						markAccountStatus(r.Context(), h.loadBalancer.Store, currentAccount, status)
					}
				}
			}

			if !errClass.Retryable {
				slog.Error("Aborting retries for non-retriable error", "error", err, "category", errClass.Category)
				if errClass.Category == "auth_blocked" || errClass.Category == "auth" {
					sh.InjectAuthError(errClass.Category, errStr)
				}
				sh.finishResponse("end_turn")
				return
			}

			if r.Context().Err() != nil {
				sh.finishResponse("end_turn")
				return
			}
			if retriesRemaining <= 0 {
				if currentAccount != nil && h.loadBalancer != nil {
					slog.Error("Account request failed, max retries reached", "account", currentAccount.Name)
				}
				if errClass.Category == "auth" || errClass.Category == "auth_blocked" {
					sh.InjectAuthError(errClass.Category, errStr)
				} else {
					sh.InjectRetryExhaustedError(errStr)
				}
				sh.finishResponse("end_turn")
				return
			}
			retriesRemaining--
			if errClass.SwitchAccount && currentAccount != nil && h.loadBalancer != nil {
				if _, ok := failedAccountSet[currentAccount.ID]; !ok {
					failedAccountSet[currentAccount.ID] = struct{}{}
					failedAccountIDs = append(failedAccountIDs, currentAccount.ID)
				}
				slog.Warn("Account request failed, switching account", "account", currentAccount.Name, "unsuccessful_attempts", len(failedAccountIDs))

				// 释放旧账号的连接计数
				if trackedAccountID != 0 {
					h.loadBalancer.ReleaseConnection(trackedAccountID)
					trackedAccountID = 0
				}

				var retryErr error
				apiClient, currentAccount, retryErr = h.selectAccount(r.Context(), targetChannel, forcedChannel != "", failedAccountIDs)
				if retryErr == nil {
					if currentAccount != nil {
						h.loadBalancer.AcquireConnection(currentAccount.ID)
						trackedAccountID = currentAccount.ID
						slog.Debug("Switched to account", "account", currentAccount.Name)
					} else {
						slog.Debug("Switched to default upstream config")
					}
				} else {
					slog.Error("No more accounts available", "error", retryErr)
					sh.InjectNoAvailableAccountError(errStr, retryErr)
					sh.finishResponse("end_turn")
					return
				}
			}
			if retryDelay > 0 {
				delay := computeRetryDelay(retryDelay, attempt+1, errClass.Category)
				if delay > 0 && !util.SleepWithContext(r.Context(), delay) {
					sh.finishResponse("end_turn")
					return
				}
			}
			attempt++
		}
	}

	run()

	// 确保有最终响应
	if !sh.hasReturn {
		sh.finishResponse("end_turn")
	}

	if !isStream {
		stopReason := sh.finalStopReason
		if stopReason == "" {
			stopReason = "end_turn"
		}

		for i := range sh.contentBlocks {
			blockType, _ := sh.contentBlocks[i]["type"].(string)
			switch blockType {
			case "text":
				if builder, ok := sh.textBlockBuilders[i]; ok {
					sh.contentBlocks[i]["text"] = builder.String()
				} else if _, ok := sh.contentBlocks[i]["text"]; !ok {
					sh.contentBlocks[i]["text"] = ""
				}
			case "thinking":
				if builder, ok := sh.thinkingBlockBuilders[i]; ok {
					sh.contentBlocks[i]["thinking"] = builder.String()
				} else if _, ok := sh.contentBlocks[i]["thinking"]; !ok {
					sh.contentBlocks[i]["thinking"] = ""
				}
			}
		}

		if len(sh.contentBlocks) == 0 && sh.responseText.Len() > 0 {
			sh.contentBlocks = append(sh.contentBlocks, map[string]interface{}{
				"type": "text",
				"text": sh.responseText.String(),
			})
		}

		response := map[string]interface{}{
			"id":            sh.msgID,
			"type":          "message",
			"role":          "assistant",
			"content":       sh.contentBlocks,
			"model":         req.Model,
			"stop_reason":   stopReason,
			"stop_sequence": nil,
			"usage": map[string]int{
				"input_tokens":  sh.inputTokens,
				"output_tokens": sh.outputTokens,
			},
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			slog.Error("Failed to write JSON response", "error", err)
		}

	}

	// Sync state and update stats using helpers
	h.syncWarpState(currentAccount, apiClient, accountSnapshot)
	h.updateAccountStats(currentAccount, sh.inputTokens, sh.outputTokens)

	// Audit log
	if h.auditLogger != nil {
		accountID := int64(0)
		channel := forcedChannel
		if currentAccount != nil {
			accountID = currentAccount.ID
			if channel == "" {
				channel = currentAccount.AccountType
			}
		}
		status := "success"
		if sh.finalStopReason == "" && !sh.hasReturn {
			status = "error"
		}
		h.auditLogger.Log(r.Context(), audit.Event{
			Action:    "chat_request",
			AccountID: accountID,
			Model:     req.Model,
			Channel:   channel,
			ClientIP:  r.RemoteAddr,
			UserAgent: r.UserAgent(),
			Duration:  time.Since(startTime).Milliseconds(),
			Status:    status,
			Metadata: map[string]interface{}{
				"input_tokens":  sh.inputTokens,
				"output_tokens": sh.outputTokens,
				"stream":        isStream,
			},
		})
	}
}

func randomSessionID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based if crypto/rand fails (unlikely)
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
