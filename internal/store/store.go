package store

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"orchids-api/internal/modelpolicy"
)

var ErrNoRows = fmt.Errorf("no rows in result set")

type Account struct {
	ID                   int64     `json:"id"`
	Name                 string    `json:"name"`
	AccountType          string    `json:"account_type"`
	NSFWEnabled          bool      `json:"nsfw_enabled"`
	SessionID            string    `json:"session_id"`
	ClientCookie         string    `json:"client_cookie"`
	RefreshToken         string    `json:"refresh_token,omitempty"`
	DeviceID             string    `json:"device_id,omitempty"`
	RequestID            string    `json:"request_id,omitempty"`
	SessionCookie        string    `json:"session_cookie"`
	ClientUat            string    `json:"client_uat"`
	ProjectID            string    `json:"project_id"`
	UserID               string    `json:"user_id"`
	AgentMode            string    `json:"agent_mode"`
	Email                string    `json:"email"`
	Weight               int       `json:"weight"`
	Enabled              bool      `json:"enabled"`
	Token                string    `json:"token"`        // Runtime/display token for non-Warp channels
	Subscription         string    `json:"subscription"` // "free", "pro", etc.
	UsageCurrent         float64   `json:"usage_current"`
	UsageTotal           float64   `json:"usage_total"` // Used as lifetime usage
	UsageLimit           float64   `json:"usage_limit"` // Daily limit
	WarpMonthlyLimit     float64   `json:"warp_monthly_limit,omitempty"`
	WarpMonthlyRemaining float64   `json:"warp_monthly_remaining,omitempty"`
	WarpBonusRemaining   float64   `json:"warp_bonus_remaining,omitempty"`
	StatusCode           string    `json:"status_code"`
	LastAttempt          time.Time `json:"last_attempt"`
	QuotaResetAt         time.Time `json:"quota_reset_at"`
	RequestCount         int64     `json:"request_count"`
	LastUsedAt           time.Time `json:"last_used_at"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`

	// CredentialType marks the Grok account credential mode. Empty or "sso"
	// keeps the legacy SSO-cookie behavior; "oauth" selects the Build CLI OAuth
	// flow (cli-chat-proxy.grok.com + Bearer). Zero value must preserve legacy
	// SSO semantics for old Redis records.
	CredentialType    string    `json:"credential_type,omitempty"`
	OAuthAccessToken  string    `json:"oauth_access_token,omitempty"`
	OAuthRefreshToken string    `json:"oauth_refresh_token,omitempty"`
	OAuthExpiresAt    time.Time `json:"oauth_expires_at,omitempty"`
	TeamID            string    `json:"team_id,omitempty"`
	// UpstreamMode overrides the per-account upstream selection. Empty lets the
	// ModelSpec decide; otherwise one of "app_chat", "console", "cli".
	UpstreamMode string `json:"upstream_mode,omitempty"`
	// GrokProvider is the explicit xAI product boundary. Build OAuth, Grok Web
	// SSO and Console SSO have different credentials, model catalogs, quotas
	// and failure semantics; they must not be treated as interchangeable.
	// Legacy accounts are normalized on read/write from CredentialType.
	GrokProvider string `json:"grok_provider,omitempty"`
	// GrokModels is the last successful account-specific upstream /v1/models
	// capability snapshot. An empty snapshot means not synced yet, not that the
	// account supports every model.
	GrokModels         []string  `json:"grok_models,omitempty"`
	GrokModelsSyncedAt time.Time `json:"grok_models_synced_at,omitempty"`
	// GrokBilling contains only official xAI Build billing information. It is
	// deliberately separate from GrokRateLimits, whose request/token headers
	// are short-lived throttling windows rather than subscription allowance.
	GrokBilling    GrokBillingSnapshot   `json:"grok_billing,omitempty"`
	GrokRateLimits GrokRateLimitSnapshot `json:"grok_rate_limits,omitempty"`
	// GrokWebQuota stores the Web SSO quota windows returned by the upstream
	// auto/fast modes. It is intentionally separate from Build billing and
	// passive request/token rate-limit headers.
	GrokWebQuota GrokWebQuotaSnapshot `json:"grok_web_quota,omitempty"`
}

// GrokQuotaWindow is one explicit upstream usage or throttling dimension.
// Values are meaningful only when their Has* marker is true; zero is valid.
type GrokQuotaWindow struct {
	Limit        float64   `json:"limit,omitempty"`
	Remaining    float64   `json:"remaining,omitempty"`
	UsagePercent float64   `json:"usage_percent,omitempty"`
	HasLimit     bool      `json:"has_limit,omitempty"`
	HasRemaining bool      `json:"has_remaining,omitempty"`
	HasUsage     bool      `json:"has_usage,omitempty"`
	ResetAt      time.Time `json:"reset_at,omitempty"`
}

// GrokBillingSnapshot stores official Build weekly/monthly windows only.
type GrokBillingSnapshot struct {
	Weekly   GrokQuotaWindow `json:"weekly,omitempty"`
	Monthly  GrokQuotaWindow `json:"monthly,omitempty"`
	SyncedAt time.Time       `json:"synced_at,omitempty"`
	Source   string          `json:"source,omitempty"`
}

// GrokRateLimitSnapshot stores passive response headers separately from
// billing. They can be useful for cooldown and diagnostics but must never be
// rendered as a paid-plan balance.
type GrokRateLimitSnapshot struct {
	Requests   GrokQuotaWindow `json:"requests,omitempty"`
	Tokens     GrokQuotaWindow `json:"tokens,omitempty"`
	Model      string          `json:"model,omitempty"`
	ObservedAt time.Time       `json:"observed_at,omitempty"`
}

// GrokWebQuotaSnapshot is the authoritative Web SSO quota snapshot. Either
// mode may be unavailable for a given account, so each window carries its own
// presence markers and the snapshot can represent a partial response.
type GrokWebQuotaSnapshot struct {
	Auto     GrokQuotaWindow `json:"auto,omitempty"`
	Fast     GrokQuotaWindow `json:"fast,omitempty"`
	SyncedAt time.Time       `json:"synced_at,omitempty"`
	Source   string          `json:"source,omitempty"`
}

// AccountStatusWarpQuotaExhausted records a Warp credit exhaustion separately
// from a transient HTTP 429. The account remains usable for Warp's free-only
// capabilities while model/capability filters keep paid requests away from it.
const AccountStatusWarpQuotaExhausted = "warp_quota_exhausted"

type Settings struct {
	ID    int64  `json:"id"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ApiKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	KeyHash    string     `json:"-"`
	KeyFull    string     `json:"-"`
	KeyPrefix  string     `json:"key_prefix"`
	KeySuffix  string     `json:"key_suffix"`
	Enabled    bool       `json:"enabled"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

type Store struct {
	accounts accountStore
	settings settingsStore
	apiKeys  apiKeyStore
	models   modelStore
}

type Options struct {
	StoreMode     string
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	RedisPrefix   string
}

type accountStore interface {
	CreateAccount(ctx context.Context, acc *Account) error
	UpdateAccount(ctx context.Context, acc *Account) error
	DeleteAccount(ctx context.Context, id int64) error
	GetAccount(ctx context.Context, id int64) (*Account, error)
	ListAccounts(ctx context.Context) ([]*Account, error)
	GetEnabledAccounts(ctx context.Context) ([]*Account, error)
	IncrementRequestCount(ctx context.Context, id int64) error
	IncrementAccountStats(ctx context.Context, id int64, usage float64, count int64) error
}

type settingsStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

type apiKeyStore interface {
	CreateApiKey(ctx context.Context, key *ApiKey) error
	ListApiKeys(ctx context.Context) ([]*ApiKey, error)
	UpdateApiKeyEnabled(ctx context.Context, id int64, enabled bool) error
	DeleteApiKey(ctx context.Context, id int64) error
	GetApiKeyByID(ctx context.Context, id int64) (*ApiKey, error)
}

type modelStore interface {
	CreateModel(ctx context.Context, m *Model) error
	UpdateModel(ctx context.Context, m *Model) error
	DeleteModel(ctx context.Context, id string) error
	GetModel(ctx context.Context, id string) (*Model, error)
	ListModels(ctx context.Context) ([]*Model, error)
	GetModelByModelID(ctx context.Context, modelID string) (*Model, error)
	GetModelByChannelAndModelID(ctx context.Context, channel, modelID string) (*Model, error)
}

func New(opts Options) (*Store, error) {
	store := &Store{}
	redisStore, err := newRedisStore(opts.RedisAddr, opts.RedisPassword, opts.RedisDB, opts.RedisPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to init redis store: %w", err)
	}
	store.accounts = redisStore
	store.settings = redisStore
	store.apiKeys = redisStore
	store.models = redisStore
	if err := store.seedModels(); err != nil {
		slog.Warn("failed to seed models in redis", "error", err)
	}
	return store, nil
}

func (s *Store) seedModels() error {
	ctx := context.Background()
	s.cleanupDeprecatedModelIDs(ctx)
	s.reconcileLatestPuterModels(ctx)
	existing, err := s.ListModels(ctx)
	if err == nil && len(existing) > 0 {
		s.ensureRequiredGrokChatModels(ctx)
		slog.Debug("Model seed skipped; existing model records preserved", "count", len(existing))
		return nil
	}
	if err != nil {
		slog.Warn("failed to inspect existing models before seed", "error", err)
	}

	models := BuildWarpSeedModels()
	models = append(models, buildGrokSeedModels()...)
	models = append(models, buildPuterSeedModels()...)

	for _, m := range models {
		if _, err := s.GetModelByChannelAndModelID(ctx, m.Channel, m.ModelID); err == nil {
			continue
		}
		if err := s.CreateModel(ctx, &m); err != nil {
			slog.Warn("Failed to seed model", "model_id", m.ModelID, "error", err)
		} else {
			slog.Debug("Seeded model", "model_id", m.ModelID)
		}
	}

	s.cleanupDeprecatedModelIDs(ctx)
	s.reconcileLatestPuterModels(ctx)

	return nil
}

func (s *Store) reconcileLatestPuterModels(ctx context.Context) {
	models, err := s.ListModels(ctx)
	if err != nil {
		slog.Warn("Failed to inspect Puter models for reconciliation", "error", err)
		return
	}
	for _, model := range models {
		if model == nil || !strings.EqualFold(strings.TrimSpace(model.Channel), "puter") || modelpolicy.IsLatestPuterModelID(model.ModelID) {
			continue
		}
		if err := s.DeleteModel(ctx, model.ID); err != nil {
			slog.Warn("Failed to remove old Puter model", "model_id", model.ModelID, "error", err)
		}
	}
}

func (s *Store) cleanupDeprecatedModelIDs(ctx context.Context) {
	deprecatedModelIDs := []string{
		"grok-4.20-0309-non-reasoning",
		"grok-4.20-0309",
		"grok-4.20-0309-reasoning",
		"grok-4.20-0309-non-reasoning-super",
		"grok-4.20-0309-super",
		"grok-4.20-0309-reasoning-super",
		"grok-4.20-0309-non-reasoning-heavy",
		"grok-4.20-0309-heavy",
		"grok-4.20-0309-reasoning-heavy",
		"grok-4.20-multi-agent-0309",
		"grok-4.20-fast",
		"grok-4.20-auto",
		"grok-4.20-expert",
		"grok-4.20-heavy",
		"grok-4.3-beta",
		"grok-imagine-image-pro",
		"grok-3",
		"grok-3-thinking",
		"grok-3-fast",
		"grok-4",
		"grok-4-mini",
		"grok-4-fast",
		"grok-4-heavy",
		"grok-4.1-mini",
		"grok-4.1-fast",
		"grok-4.1-thinking",
		"grok-4.1",
		"grok-4-1-thinking-1129",
		"grok-4.2",
		"grok-4.20-beta",
		"grok-4.20-reasoning",
		"grok-4.20-non-reasoning",
		"grok-4.20-multi-agent",
		"grok-420",
		"grok-4.3",
		"grok-build-0.1",
		"grok-code-fast",
		"grok-code-fast-1",
		"grok-imagine-1.0",
		"grok-imagine-1.0-fast",
		"grok-imagine-1.0-edit",
		"grok-imagine-1.0-video",
		"grok-2",
		"grok-2.1",
		"grok-3.1",
		"grok-4.21",
	}
	for _, modelID := range deprecatedModelIDs {
		m, err := s.GetModelByModelID(ctx, modelID)
		if err != nil || m == nil {
			continue
		}
		if err := s.DeleteModel(ctx, m.ID); err != nil {
			slog.Warn("Failed to remove deprecated model", "model_id", modelID, "error", err)
			continue
		}
		slog.Debug("Removed deprecated model", "model_id", modelID)
	}
}

func (s *Store) ensureRequiredGrokChatModels(ctx context.Context) {
	for _, src := range buildGrokSeedModels() {
		if _, err := s.GetModelByChannelAndModelID(ctx, src.Channel, src.ModelID); err == nil {
			continue
		}
		record := src
		record.ID = ""
		if err := s.CreateModel(ctx, &record); err != nil {
			slog.Warn("Failed to ensure Grok app-chat model", "model_id", src.ModelID, "error", err)
			continue
		}
		slog.Debug("Ensured Grok app-chat model", "model_id", src.ModelID)
	}
}

func buildGrokSeedModels() []Model {
	items := []struct {
		id   string
		name string
	}{
		{"grok-4.6", "Grok 4.6"},
		{"grok-4.5", "Grok 4.5"},
		{"grok-imagine-image-lite", "Grok Imagine Image Lite"},
		{"grok-imagine-image", "Grok Imagine Image"},
		{"grok-imagine-image-quality", "Grok Imagine Image Quality"},
		{"grok-imagine-image-edit", "Grok Imagine Image Edit"},
		{"grok-imagine-video", "Grok Imagine Video"},
	}
	models := make([]Model, 0, len(items))
	for i, item := range items {
		models = append(models, Model{
			ID:        fmt.Sprintf("grok-%03d", i+1),
			Channel:   "Grok",
			ModelID:   item.id,
			Name:      item.name,
			Status:    ModelStatusAvailable,
			Verified:  true,
			IsDefault: i == 0,
			SortOrder: i,
		})
	}
	return models
}

func (s *Store) Close() error {
	if rs, ok := s.accounts.(*redisStore); ok {
		return rs.Close()
	}
	return nil
}

// RedisClient returns the underlying Redis client, or nil if not using Redis.
func (s *Store) RedisClient() *redis.Client {
	if rs, ok := s.accounts.(*redisStore); ok {
		return rs.Client()
	}
	return nil
}

// RedisPrefix returns the configured key prefix.
func (s *Store) RedisPrefix() string {
	if s.accounts != nil {
		if rs, ok := s.accounts.(*redisStore); ok {
			return rs.prefix
		}
	}
	return "orchids:"
}

func (s *Store) CreateAccount(ctx context.Context, acc *Account) error {
	if s.accounts != nil {
		return s.accounts.CreateAccount(ctx, acc)
	}
	return fmt.Errorf("store not configured")
}

func (s *Store) UpdateAccount(ctx context.Context, acc *Account) error {
	if s.accounts != nil {
		return s.accounts.UpdateAccount(ctx, acc)
	}
	return fmt.Errorf("store not configured")
}

func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	if s.accounts != nil {
		return s.accounts.DeleteAccount(ctx, id)
	}
	return fmt.Errorf("store not configured")
}

func (s *Store) GetAccount(ctx context.Context, id int64) (*Account, error) {
	if s.accounts != nil {
		return s.accounts.GetAccount(ctx, id)
	}
	return nil, fmt.Errorf("store not configured")
}

func (s *Store) ListAccounts(ctx context.Context) ([]*Account, error) {
	if s.accounts != nil {
		return s.accounts.ListAccounts(ctx)
	}
	return nil, fmt.Errorf("store not configured")
}

func (s *Store) GetEnabledAccounts(ctx context.Context) ([]*Account, error) {
	if s.accounts != nil {
		return s.accounts.GetEnabledAccounts(ctx)
	}
	return nil, fmt.Errorf("store not configured")
}

func (s *Store) IncrementRequestCount(ctx context.Context, id int64) error {
	if s.accounts != nil {
		return s.accounts.IncrementRequestCount(ctx, id)
	}
	return fmt.Errorf("store not configured")
}

func (s *Store) IncrementAccountStats(ctx context.Context, id int64, usage float64, count int64) error {
	if s.accounts != nil {
		return s.accounts.IncrementAccountStats(ctx, id, usage, count)
	}
	return fmt.Errorf("store not configured")
}

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	if s.settings != nil {
		return s.settings.GetSetting(ctx, key)
	}
	return "", fmt.Errorf("settings store not configured")
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	if s.settings != nil {
		return s.settings.SetSetting(ctx, key, value)
	}
	return fmt.Errorf("settings store not configured")
}

func (s *Store) CreateApiKey(ctx context.Context, key *ApiKey) error {
	if s.apiKeys != nil {
		return s.apiKeys.CreateApiKey(ctx, key)
	}
	return fmt.Errorf("api keys store not configured")
}

func (s *Store) ListApiKeys(ctx context.Context) ([]*ApiKey, error) {
	if s.apiKeys != nil {
		return s.apiKeys.ListApiKeys(ctx)
	}
	return nil, fmt.Errorf("api keys store not configured")
}

func (s *Store) UpdateApiKeyEnabled(ctx context.Context, id int64, enabled bool) error {
	if s.apiKeys != nil {
		return s.apiKeys.UpdateApiKeyEnabled(ctx, id, enabled)
	}
	return fmt.Errorf("api keys store not configured")
}

func (s *Store) DeleteApiKey(ctx context.Context, id int64) error {
	if s.apiKeys != nil {
		return s.apiKeys.DeleteApiKey(ctx, id)
	}
	return fmt.Errorf("api keys store not configured")
}

func (s *Store) GetApiKeyByID(ctx context.Context, id int64) (*ApiKey, error) {
	if s.apiKeys != nil {
		return s.apiKeys.GetApiKeyByID(ctx, id)
	}
	return nil, fmt.Errorf("api keys store not configured")
}

// Model wrappers

func (s *Store) CreateModel(ctx context.Context, m *Model) error {
	if s.models == nil {
		return fmt.Errorf("models store not configured")
	}
	s.clearOtherModelDefaults(ctx, m, false)
	return s.models.CreateModel(ctx, m)
}

func (s *Store) UpdateModel(ctx context.Context, m *Model) error {
	if s.models == nil {
		return fmt.Errorf("models store not configured")
	}
	s.clearOtherModelDefaults(ctx, m, true)
	return s.models.UpdateModel(ctx, m)
}

func (s *Store) clearOtherModelDefaults(ctx context.Context, m *Model, excludeSelf bool) {
	if !m.IsDefault {
		return
	}
	models, err := s.models.ListModels(ctx)
	if err != nil {
		return
	}
	for _, other := range models {
		if other.Channel != m.Channel || excludeSelf && other.ID == m.ID || !other.IsDefault {
			continue
		}
		other.IsDefault = false
		if err := s.models.UpdateModel(ctx, other); err != nil {
			slog.Warn("Failed to clear default flag on model", "model_id", other.ModelID, "error", err)
		}
	}
}

func (s *Store) DeleteModel(ctx context.Context, id string) error {
	if s.models != nil {
		return s.models.DeleteModel(ctx, id)
	}
	return fmt.Errorf("models store not configured")
}

func (s *Store) GetModel(ctx context.Context, id string) (*Model, error) {
	if s.models != nil {
		return s.models.GetModel(ctx, id)
	}
	return nil, fmt.Errorf("models store not configured")
}

func (s *Store) GetModelByModelID(ctx context.Context, modelID string) (*Model, error) {
	if s.models != nil {
		return s.models.GetModelByModelID(ctx, modelID)
	}
	return nil, fmt.Errorf("models store not configured")
}

func (s *Store) GetModelByChannelAndModelID(ctx context.Context, channel, modelID string) (*Model, error) {
	if s.models != nil {
		return s.models.GetModelByChannelAndModelID(ctx, channel, modelID)
	}
	return nil, fmt.Errorf("models store not configured")
}

func (s *Store) ListModels(ctx context.Context) ([]*Model, error) {
	if s.models != nil {
		return s.models.ListModels(ctx)
	}
	return nil, fmt.Errorf("models store not configured")
}
