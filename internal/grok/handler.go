package grok

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"orchids-api/internal/config"
	"orchids-api/internal/handler"
	"orchids-api/internal/loadbalancer"
	"orchids-api/internal/modelpolicy"
	"orchids-api/internal/store"
	"path/filepath"
	"strings"
	"sync"
	"time"

	apperrors "orchids-api/internal/errors"
)

const maxEditImageBytes = 50 * 1024 * 1024

var cacheBaseDir = filepath.Join("data", "tmp")

const grokModelValidationCacheTTL = 3 * time.Second

type Handler struct {
	base         *handler.BaseHandler
	cfg          *config.Config
	lb           *loadbalancer.LoadBalancer
	client       *Client
	cliClient    *CLIClient
	connTracker  loadbalancer.ConnTracker
	modelCacheMu sync.RWMutex
	modelCache   map[string]time.Time
	sessionMu    sync.Mutex
	affinity     map[string]sessionAffinityEntry
	replay       map[string]reasoningReplayEntry
	instanceID   string
}

type chatAccountSession struct {
	acc            *store.Account
	token          string
	poolCandidates []string
	release        func()
}

type imageEditUploadInput struct {
	mime string
	data []byte
}

type imageEditReference struct {
	fileID     string
	contentURL string
}

func NewHandler(cfg *config.Config, lb *loadbalancer.LoadBalancer) *Handler {
	cliClient := NewCLIClient(cfg)
	if lb != nil {
		cliClient.SetAccountStore(lb.Store)
	}
	instanceID := "grok-" + randomHex(16)
	if cfg != nil && strings.TrimSpace(cfg.DeploymentInstance) != "" {
		instanceID = strings.TrimSpace(cfg.DeploymentInstance)
	}
	h := &Handler{
		base:        handler.NewBaseHandler(lb),
		cfg:         cfg,
		lb:          lb,
		client:      New(cfg),
		cliClient:   cliClient,
		connTracker: loadbalancer.NewMemoryConnTracker(),
		modelCache:  make(map[string]time.Time),
		affinity:    make(map[string]sessionAffinityEntry),
		replay:      make(map[string]reasoningReplayEntry),
		instanceID:  instanceID,
	}
	h.recoverStoredConsoleVideoJobs(context.Background())
	return h
}

func (h *Handler) currentClient() *Client {
	if h == nil {
		return nil
	}
	return h.client
}

func (h *Handler) isModelValidationCached(modelID string) bool {
	if h == nil || strings.TrimSpace(modelID) == "" {
		return false
	}
	h.modelCacheMu.RLock()
	expiresAt, ok := h.modelCache[modelID]
	h.modelCacheMu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().Before(expiresAt) {
		return true
	}
	h.modelCacheMu.Lock()
	if staleAt, ok := h.modelCache[modelID]; ok && !time.Now().Before(staleAt) {
		delete(h.modelCache, modelID)
	}
	h.modelCacheMu.Unlock()
	return false
}

func (h *Handler) cacheValidatedModel(modelID string) {
	if h == nil || strings.TrimSpace(modelID) == "" {
		return
	}
	h.modelCacheMu.Lock()
	h.modelCache[modelID] = time.Now().Add(grokModelValidationCacheTTL)
	h.modelCacheMu.Unlock()
}

// isGrokWebAccount selects only Grok Web SSO sessions. Build OAuth and
// Console SSO sessions are deliberately separate products and must never be
// sent to the legacy web transport.
func isGrokWebAccount(acc *store.Account) bool {
	return acc != nil && ProviderForAccount(acc) == ProviderWeb &&
		!strings.EqualFold(strings.TrimSpace(acc.CredentialType), "oauth")
}

// isGrokConsoleAccount selects only Console SSO sessions for console.x.ai.
func isGrokConsoleAccount(acc *store.Account) bool {
	return acc != nil && ProviderForAccount(acc) == ProviderConsole &&
		!strings.EqualFold(strings.TrimSpace(acc.CredentialType), "oauth")
}

// grokSSOTokenRaw returns the raw SSO credential for an account, preferring the
// client cookie and falling back to the refresh token (no normalization).
func grokSSOTokenRaw(acc *store.Account) string {
	if acc == nil {
		return ""
	}
	raw := strings.TrimSpace(acc.ClientCookie)
	if raw == "" {
		raw = strings.TrimSpace(acc.RefreshToken)
	}
	return raw
}

func (h *Handler) selectAccount(ctx context.Context) (*store.Account, string, error) {
	if h.lb == nil {
		return nil, "", fmt.Errorf("load balancer not configured")
	}
	acc, err := h.lb.GetNextAccountExcludingByChannelWithTrackerFilter(ctx, nil, "grok", h.connTracker, isGrokWebAccount)
	if err != nil {
		return nil, "", err
	}
	raw := grokSSOTokenRaw(acc)
	if NormalizeSSOToken(raw) == "" {
		return nil, "", fmt.Errorf("grok account token is empty")
	}
	return acc, raw, nil
}

func (h *Handler) ensureModelEnabled(ctx context.Context, modelID string) error {
	id := normalizeModelID(modelID)
	if IsDeprecatedModelID(id) {
		return fmt.Errorf("model not found")
	}
	if h.isModelValidationCached(id) {
		return nil
	}
	if h == nil || h.lb == nil || h.lb.Store == nil {
		if !modelpolicy.IsPublicGrokModelID(id) {
			return fmt.Errorf("model not found")
		}
		h.cacheValidatedModel(id)
		return nil
	}

	m, err := h.lb.Store.GetModelByChannelAndModelID(ctx, "grok", id)
	if err != nil || m == nil {
		rawID := strings.ToLower(strings.TrimSpace(modelID))
		if rawID != "" && rawID != id {
			m, err = h.lb.Store.GetModelByChannelAndModelID(ctx, "grok", rawID)
		}
	}
	if err != nil || m == nil {
		return fmt.Errorf("model not found")
	}
	if !modelpolicy.IsVisibleGrokModel(id, m.Verified) {
		return fmt.Errorf("model not found")
	}
	if !m.Status.Enabled() {
		return fmt.Errorf("model not available")
	}
	channel := strings.TrimSpace(m.Channel)
	if channel == "" {
		channel = "grok"
	}
	if !strings.EqualFold(channel, "grok") {
		return fmt.Errorf("model not found")
	}
	h.cacheValidatedModel(id)
	return nil
}

// resolveConversationModel resolves the built-in Web/Console/Build catalog and
// account-discovered Build models. A discovered model is routable only when at
// least one enabled Build account advertises it, so arbitrary client strings
// can never turn into upstream model probes.
func (h *Handler) resolveConversationModel(ctx context.Context, modelID string) (ModelSpec, bool) {
	if spec, ok := ResolveModel(modelID); ok {
		return spec, true
	}
	id := normalizeModelID(modelID)
	if id == "" || IsDeprecatedModelID(id) || h == nil || h.lb == nil || h.lb.Store == nil {
		return ModelSpec{}, false
	}
	accounts, err := h.lb.Store.GetEnabledAccounts(ctx)
	if err != nil {
		return ModelSpec{}, false
	}
	for _, acc := range accounts {
		if ProviderForAccount(acc) != ProviderBuild || len(acc.GrokModels) == 0 {
			continue
		}
		for _, candidate := range acc.GrokModels {
			if strings.EqualFold(strings.TrimSpace(candidate), id) {
				return ModelSpec{ID: id, Name: id, UpstreamModel: strings.TrimSpace(candidate), Tier: grokTierSuper, Upstream: UpstreamCLI}, true
			}
		}
	}
	return ModelSpec{}, false
}

func (h *Handler) ensureResolvedModelEnabled(ctx context.Context, modelID string, spec ModelSpec) error {
	err := h.ensureModelEnabled(ctx, modelID)
	if err == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(err.Error()), "model not found") {
		return err
	}
	// Dynamic Build capabilities are authoritative even before the admin model
	// table has been reconciled by the next catalog refresh.
	if spec.Upstream == UpstreamCLI {
		if dynamic, ok := h.resolveConversationModel(ctx, modelID); ok && strings.EqualFold(dynamic.UpstreamModel, spec.UpstreamModel) {
			return nil
		}
	}
	return err
}

func modelNotFoundMessage(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return "The model does not exist or you do not have access to it."
	}
	return fmt.Sprintf("The model `%s` does not exist or you do not have access to it.", modelID)
}

func modelValidationMessage(modelID string, err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if strings.EqualFold(msg, "model not found") {
		return modelNotFoundMessage(modelID)
	}
	return msg
}

func (h *Handler) trackAccount(acc *store.Account) func() {
	if h == nil || h.connTracker == nil || acc == nil || acc.ID == 0 {
		return h.base.TrackAccount(acc)
	}
	h.connTracker.Acquire(acc.ID)
	return func() {
		h.connTracker.Release(acc.ID)
	}
}

func (h *Handler) markAccountStatus(ctx context.Context, acc *store.Account, err error) {
	// Cloudflare / DPoP challenges are egress problems, not account problems.
	// Do not cool or disable the account; the egress layer must re-solve.
	if isEgressChallengeError(err) {
		return
	}
	// Team-level resource-exhausted 429: the rate limit is on the token/session,
	// not the account. Set a cooldown so the RPM window can reset. Without this,
	// unmarked sibling accounts sharing the same token immediately hit the same
	// team limit. Prefer the precise reset parsed from the 429 body (team+model
	// granularity), falling back to a 60s blanket cooldown.
	if err != nil && isResourceExhaustedError(err) && acc != nil {
		cooldown := 60 * time.Second
		if meta := ParseRateLimitMetadata([]byte(err.Error())); meta != nil {
			if remaining := teamCooldown.RetryAfterFor(meta.Scope, meta.TeamID, meta.Model); remaining > 0 {
				cooldown = remaining
			} else if meta.RetryAfter > 0 {
				cooldown = meta.RetryAfter
			}
		}
		acc.QuotaResetAt = time.Now().Add(cooldown)
	}
	h.base.MarkAccountStatus(ctx, acc, err)
}

func isResourceExhaustedError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "resource-exhausted") ||
		strings.Contains(lower, "resource_exhausted") ||
		strings.Contains(lower, "too many requests for team")
}

func (h *Handler) openChatAccountSessionForModel(ctx context.Context, spec ModelSpec) (*chatAccountSession, error) {
	return h.openChatAccountSessionForModelExcluding(ctx, nil, spec)
}

func (h *Handler) openChatAccountSessionForModelExcluding(ctx context.Context, excludeIDs []int64, spec ModelSpec) (*chatAccountSession, error) {
	return h.openChatAccountSessionExcludingWithPools(ctx, excludeIDs, spec.PoolCandidates())
}

func (h *Handler) openChatAccountSessionForImagineLite(ctx context.Context, excludeIDs []int64, spec ModelSpec) (*chatAccountSession, error) {
	spec.Tier = grokTierLite
	return h.openChatAccountSessionExcludingWithPoolsAndFilter(ctx, excludeIDs, spec.PoolCandidates(), func(acc *store.Account) bool {
		return grokAccountPool(acc) != "basic"
	})
}

func (h *Handler) openChatAccountSessionExcludingWithPools(ctx context.Context, excludeIDs []int64, poolCandidates []string) (*chatAccountSession, error) {
	return h.openChatAccountSessionExcludingWithPoolsAndFilter(ctx, excludeIDs, poolCandidates, nil)
}

func (h *Handler) openChatAccountSessionExcludingWithPoolsAndFilter(ctx context.Context, excludeIDs []int64, poolCandidates []string, extraFilter func(*store.Account) bool) (*chatAccountSession, error) {
	if h.lb == nil {
		return nil, fmt.Errorf("load balancer not configured")
	}
	var (
		acc     *store.Account
		err     error
		lastErr error
	)
	// App-chat/media only accepts Web SSO cookie accounts. OAuth Build and
	// Console SSO are selected by their own provider-specific selectors.
	ssoFilter := func(acc *store.Account) bool {
		if !isGrokWebAccount(acc) {
			return false
		}
		return extraFilter == nil || extraFilter(acc)
	}
	if pinnedID := h.affinityAccount(ctx, ProviderWeb); pinnedID != 0 && !containsAccountID(excludeIDs, pinnedID) && h.lb.Store != nil {
		if pinned, getErr := h.lb.Store.GetAccount(ctx, pinnedID); getErr == nil && pinned != nil && accountAffinityUsable(pinned) && ssoFilter(pinned) {
			raw := grokSSOTokenRaw(pinned)
			if NormalizeSSOToken(raw) != "" {
				return &chatAccountSession{acc: pinned, token: raw, poolCandidates: normalizeGrokPoolCandidates(poolCandidates), release: h.trackAccount(pinned)}, nil
			}
		}
	}
	candidates := normalizeGrokPoolCandidates(poolCandidates)
	if len(candidates) == 0 {
		acc, err = h.lb.GetNextAccountExcludingByChannelWithTrackerFilter(ctx, excludeIDs, "grok", h.connTracker, ssoFilter)
		if err != nil {
			return nil, err
		}
	} else {
		for _, pool := range candidates {
			wantPool := pool
			acc, err = h.lb.GetNextAccountExcludingByChannelWithTrackerFilter(ctx, excludeIDs, "grok", h.connTracker, func(acc *store.Account) bool {
				return strings.EqualFold(grokAccountPool(acc), wantPool) && ssoFilter(acc)
			})
			if err == nil && acc != nil {
				break
			}
			if err != nil {
				if lastErr == nil || strings.Contains(err.Error(), "rate-limited or cooling down") {
					lastErr = err
				}
			}
		}
		if acc == nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, fmt.Errorf("no enabled grok accounts available for requested pools: %s", strings.Join(candidates, ","))
		}
	}
	raw := grokSSOTokenRaw(acc)
	if NormalizeSSOToken(raw) == "" {
		return nil, fmt.Errorf("grok account token is empty")
	}
	h.bindAffinity(ctx, ProviderWeb, acc.ID)
	return &chatAccountSession{
		acc:            acc,
		token:          raw,
		poolCandidates: candidates,
		release:        h.trackAccount(acc),
	}, nil
}

func (s *chatAccountSession) Close() {
	if s == nil || s.release == nil {
		return
	}
	s.release()
	s.release = nil
}

// doSingleAccountRequest calls the Grok API once on a single account without retry switching.
func (h *Handler) doSingleAccountRequest(
	ctx context.Context,
	sess *chatAccountSession,
	payload map[string]interface{},
	shouldMarkStatus grokAccountStatusPolicy,
	callAPI func(*Client, context.Context, string, map[string]interface{}) (*http.Response, error),
) (*http.Response, error) {
	if sess == nil || strings.TrimSpace(sess.token) == "" {
		return nil, fmt.Errorf("empty chat session")
	}
	client := h.currentClient()
	if client == nil {
		return nil, fmt.Errorf("grok client not configured")
	}
	resp, err := callAPI(client, ctx, sess.token, payload)
	if err != nil {
		if shouldMarkStatus == nil || shouldMarkStatus(err) {
			h.markAccountStatus(ctx, sess.acc, err)
		}
		return nil, err
	}
	return resp, nil
}

type grokAccountStatusPolicy func(error) bool

func markAllGrokAccountStatuses(err error) bool {
	if err == nil {
		return false
	}
	// Egress challenges and shared team rate limits are not account failures.
	if isEgressChallengeError(err) {
		return false
	}
	if isSharedGrokRateLimitError(err) {
		return false
	}
	// A generic 403 must not mark the account; only explicit account blocks do.
	if ClassifyUpstreamError(err) == UpstreamErrorGenericForbidden {
		return false
	}
	return true
}

func skipExternalAttachmentFetchGrokAccountStatus(err error) bool {
	if err == nil {
		return false
	}
	return !strings.Contains(strings.ToLower(err.Error()), "fetch url status=")
}

// doChatWithAutoSwitchRebuild calls doChat with automatic account switching and payload rebuild on failure.
func (h *Handler) doChatWithAutoSwitchRebuild(
	ctx context.Context,
	sess *chatAccountSession,
	payload *map[string]interface{},
	rebuild func(token string) (map[string]interface{}, error),
) (*http.Response, error) {
	return h.doAutoSwitchRequest(ctx, sess, payload, rebuild, (*Client).doChat)
}

// doAutoSwitchRequest calls the Grok API with automatic account switching on
// failure, rebuilding the request payload for the new account when rebuild is set.
func (h *Handler) doAutoSwitchRequest(
	ctx context.Context,
	sess *chatAccountSession,
	payload *map[string]interface{},
	rebuild func(token string) (map[string]interface{}, error),
	callAPI func(*Client, context.Context, string, map[string]interface{}) (*http.Response, error),
) (*http.Response, error) {
	if sess == nil || strings.TrimSpace(sess.token) == "" {
		return nil, fmt.Errorf("empty chat session")
	}
	if payload == nil {
		return nil, fmt.Errorf("empty payload")
	}
	client := h.currentClient()
	if client == nil {
		return nil, fmt.Errorf("grok client not configured")
	}
	return h.retryWithAccountSwitch(ctx, sess, 100*time.Millisecond,
		func() (*http.Response, error) { return callAPI(client, ctx, sess.token, *payload) },
		func(used []int64) (*chatAccountSession, error) {
			return h.openChatAccountSessionExcludingWithPools(ctx, used, sess.poolCandidates)
		},
		func() error {
			if rebuild == nil {
				return nil
			}
			newPayload, rbErr := rebuild(sess.token)
			if rbErr != nil {
				return rbErr
			}
			*payload = newPayload
			return nil
		})
}

// isEgressChallengeError reports whether an error is a Cloudflare interstitial
// or DPoP proof challenge. These are egress/clearance problems, not account
// problems: switching accounts (or cooling the account) is wrong.
func isEgressChallengeError(err error) bool {
	if err == nil {
		return false
	}
	kind := ClassifyUpstreamError(err)
	return kind == UpstreamErrorCloudflareChallenge || kind == UpstreamErrorDPoPChallenge
}

func shouldSwitchGrokAccount(err error) bool {
	if err == nil {
		return false
	}
	// Cloudflare / DPoP challenges should not drain the account pool: switching
	// to another account hits the same wall. Leave to the egress layer instead.
	if isEgressChallengeError(err) {
		return false
	}
	// Response-aware classification: only an explicit account block switches
	// accounts. A generic 403 (feature/plan/permission) is not an account
	// failure.
	switch ClassifyUpstreamError(err) {
	case UpstreamErrorAccountBlock:
		return true
	case UpstreamErrorGenericForbidden:
		return false
	}
	status := apperrors.ClassifyAccountStatus(err.Error())
	if status == "429" {
		return !isSharedGrokRateLimitError(err)
	}
	if isSharedGrokRateLimitError(err) {
		return false
	}
	if upstreamStatus := parseUpstreamStatus(err); upstreamStatus == http.StatusBadGateway ||
		upstreamStatus == http.StatusServiceUnavailable ||
		upstreamStatus == http.StatusGatewayTimeout ||
		upstreamStatus == http.StatusInternalServerError {
		return true
	}

	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "timeout"),
		strings.Contains(lower, "deadline exceeded"),
		strings.Contains(lower, "connection reset"),
		strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "broken pipe"),
		strings.HasSuffix(lower, ": eof"),
		lower == "eof":
		return true
	default:
		return false
	}
}

func isSharedGrokRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	if parseUpstreamStatus(err) != http.StatusTooManyRequests && !strings.Contains(lower, "too many requests") {
		return false
	}
	return strings.Contains(lower, "too_many_requests") ||
		strings.Contains(lower, "too many requests for team") ||
		strings.Contains(lower, "resource-exhausted") ||
		strings.Contains(lower, "resource_exhausted") ||
		strings.Contains(lower, "please try again in a bit") ||
		strings.Contains(lower, "body=too many requests")
}

func upstreamHTTPResponseStatus(err error) int {
	switch parseUpstreamStatus(err) {
	case http.StatusTooManyRequests:
		return http.StatusTooManyRequests
	case http.StatusForbidden:
		return http.StatusForbidden
	case http.StatusUnauthorized:
		return http.StatusUnauthorized
	case http.StatusServiceUnavailable:
		return http.StatusServiceUnavailable
	case http.StatusGatewayTimeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}

func (h *Handler) syncGrokQuota(acc *store.Account, headers http.Header) {
	if acc == nil || h.lb == nil || h.lb.Store == nil {
		return
	}
	accCopy := *acc
	headers = headers.Clone()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.lb.Store.IncrementAccountStats(ctx, accCopy.ID, 0, 1); err != nil {
			slog.Warn("grok usage touch failed", "account_id", accCopy.ID, "error", err)
		}

		info := parseRateLimitInfo(headers)
		latest, err := h.lb.Store.GetAccount(ctx, accCopy.ID)
		if err != nil || latest == nil {
			slog.Warn("grok quota account reload failed", "account_id", accCopy.ID, "error", err)
			return
		}
		NormalizeProvider(latest)
		if ProviderForAccount(latest) == ProviderBuild {
			if !ApplyBuildRateLimits(latest, headers) {
				return
			}
		} else if info == nil || !ApplyQuotaInfo(latest, info) {
			return
		}
		if err := h.lb.Store.UpdateAccount(ctx, latest); err != nil {
			slog.Warn("grok quota update failed", "account_id", latest.ID, "error", err)
		}
	}()
}
