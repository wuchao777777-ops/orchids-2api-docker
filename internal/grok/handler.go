package grok

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"orchids-api/internal/config"
	"orchids-api/internal/handler"
	"orchids-api/internal/loadbalancer"
	"orchids-api/internal/store"
	"path/filepath"
	"strings"
	"time"
)

const maxEditImageBytes = 50 * 1024 * 1024

var cacheBaseDir = filepath.Join("data", "tmp")

type Handler struct {
	base   *handler.BaseHandler
	cfg    *config.Config
	lb     *loadbalancer.LoadBalancer
	client *Client
}

type chatAccountSession struct {
	acc     *store.Account
	token   string
	release func()
}

type imageEditUploadInput struct {
	mime string
	data []byte
}

func NewHandler(cfg *config.Config, lb *loadbalancer.LoadBalancer) *Handler {
	return &Handler{
		base:   handler.NewBaseHandler(lb),
		cfg:    cfg,
		lb:     lb,
		client: New(cfg),
	}
}

func (h *Handler) selectAccount(ctx context.Context) (*store.Account, string, error) {
	if h.lb == nil {
		return nil, "", fmt.Errorf("load balancer not configured")
	}
	acc, err := h.lb.GetNextAccountExcludingByChannel(ctx, nil, "grok")
	if err != nil {
		return nil, "", err
	}
	raw := strings.TrimSpace(acc.ClientCookie)
	if raw == "" {
		raw = strings.TrimSpace(acc.RefreshToken)
	}
	token := NormalizeSSOToken(raw)
	if token == "" {
		return nil, "", fmt.Errorf("grok account token is empty")
	}
	return acc, token, nil
}

func (h *Handler) ensureModelEnabled(ctx context.Context, modelID string) error {
	return h.base.EnsureModelEnabled(ctx, normalizeModelID(modelID), "grok")
}

func isAutoRegisterableGrokModel(modelID string) bool {
	id := normalizeModelID(modelID)
	if id == "" {
		return false
	}
	if strings.HasPrefix(id, "grok-imagine-") {
		return false
	}
	return strings.HasPrefix(id, "grok-")
}

func (h *Handler) tryAutoRegisterModel(ctx context.Context, modelID string) bool {
	if h == nil || h.lb == nil || h.lb.Store == nil {
		return false
	}
	id := normalizeModelID(modelID)
	if !isAutoRegisterableGrokModel(id) {
		return false
	}

	if m, err := h.lb.Store.GetModelByModelID(ctx, id); err == nil && m != nil {
		return true
	}

	newModel := &store.Model{
		Channel: "Grok",
		ModelID: id,
		Name:    id,
		Status:  store.ModelStatusAvailable,
	}
	if err := h.lb.Store.CreateModel(ctx, newModel); err != nil {
		// Handle create races gracefully.
		if m, checkErr := h.lb.Store.GetModelByModelID(ctx, id); checkErr == nil && m != nil {
			return true
		}
		slog.Warn("Auto register grok model failed", "model_id", id, "error", err)
		return false
	}

	slog.Info("Auto registered grok model", "model_id", id)
	return true
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
	return h.base.TrackAccount(acc)
}

func (h *Handler) markAccountStatus(ctx context.Context, acc *store.Account, err error) {
	h.base.MarkAccountStatus(ctx, acc, err)
}

func (h *Handler) openChatAccountSession(ctx context.Context) (*chatAccountSession, error) {
	acc, token, err := h.selectAccount(ctx)
	if err != nil {
		return nil, err
	}
	return &chatAccountSession{
		acc:     acc,
		token:   token,
		release: h.trackAccount(acc),
	}, nil
}

func (s *chatAccountSession) Close() {
	if s == nil || s.release == nil {
		return
	}
	s.release()
	s.release = nil
}

// doChatWithAutoSwitch runs one chat request and switches account once on 403/429.
func (h *Handler) doChatWithAutoSwitch(ctx context.Context, sess *chatAccountSession, payload map[string]interface{}) (*http.Response, error) {
	if sess == nil || strings.TrimSpace(sess.token) == "" {
		return nil, fmt.Errorf("empty chat session")
	}
	resp, err := h.client.doChat(ctx, sess.token, payload)
	if err == nil {
		return resp, nil
	}
	h.markAccountStatus(ctx, sess.acc, err)
	if !shouldSwitchGrokAccount(err) {
		return nil, err
	}

	sess.Close()
	next, err2 := h.openChatAccountSession(ctx)
	if err2 != nil {
		return nil, fmt.Errorf("account switch failed: %w (original: %v)", err2, err)
	}
	sess.acc = next.acc
	sess.token = next.token
	sess.release = next.release

	resp2, err3 := h.client.doChat(ctx, sess.token, payload)
	if err3 == nil {
		return resp2, nil
	}
	h.markAccountStatus(ctx, sess.acc, err3)
	return nil, err3
}

// doChatWithAutoSwitchRebuild retries once with a switched account and rebuilds payload for the new token.
func (h *Handler) doChatWithAutoSwitchRebuild(
	ctx context.Context,
	sess *chatAccountSession,
	payload *map[string]interface{},
	rebuild func(token string) (map[string]interface{}, error),
) (*http.Response, error) {
	if sess == nil || strings.TrimSpace(sess.token) == "" {
		return nil, fmt.Errorf("empty chat session")
	}
	if payload == nil {
		return nil, fmt.Errorf("empty payload")
	}
	resp, err := h.client.doChat(ctx, sess.token, *payload)
	if err == nil {
		return resp, nil
	}
	h.markAccountStatus(ctx, sess.acc, err)
	if !shouldSwitchGrokAccount(err) {
		return nil, err
	}

	sess.Close()
	next, err2 := h.openChatAccountSession(ctx)
	if err2 != nil {
		return nil, err
	}
	sess.acc = next.acc
	sess.token = next.token
	sess.release = next.release

	if rebuild != nil {
		newPayload, rbErr := rebuild(sess.token)
		if rbErr != nil {
			return nil, rbErr
		}
		*payload = newPayload
	}

	resp2, err3 := h.client.doChat(ctx, sess.token, *payload)
	if err3 == nil {
		return resp2, nil
	}
	h.markAccountStatus(ctx, sess.acc, err3)
	return nil, err3
}

func shouldSwitchGrokAccount(err error) bool {
	if err == nil {
		return false
	}
	status := classifyAccountStatusFromError(err.Error())
	if status == "403" || status == "429" {
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

func (h *Handler) syncGrokQuota(acc *store.Account, headers http.Header) {
	if acc == nil || h.lb == nil || h.lb.Store == nil {
		return
	}
	info := parseRateLimitInfo(headers)
	if info == nil || (info.Limit <= 0 && info.Remaining <= 0) {
		return
	}
	limit := info.Limit
	remaining := info.Remaining
	if remaining < 0 {
		remaining = 0
	}
	if limit <= 0 && remaining > 0 {
		limit = remaining
	}
	used := limit - remaining
	if used < 0 {
		used = 0
	}
	acc.UsageLimit = float64(limit)
	acc.UsageCurrent = float64(used)
	if !info.ResetAt.IsZero() {
		acc.QuotaResetAt = info.ResetAt
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.lb.Store.UpdateAccount(ctx, acc); err != nil {
		slog.Warn("grok quota update failed", "account_id", acc.ID, "error", err)
	}
}
