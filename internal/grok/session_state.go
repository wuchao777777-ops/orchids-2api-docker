package grok

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/middleware"
	"orchids-api/internal/store"
)

const grokSessionStateTTL = time.Hour

type sessionAffinityEntry struct {
	AccountID int64
	ExpiresAt time.Time
}

type reasoningReplayEntry struct {
	EncryptedContent string
	ExpiresAt        time.Time
}

type grokSessionContext struct {
	Key    string
	Replay bool
	Model  string
}

type grokSessionContextKey struct{}

func sessionFromContext(ctx context.Context) grokSessionContext {
	value, _ := ctx.Value(grokSessionContextKey{}).(grokSessionContext)
	return value
}

func withGrokSession(ctx context.Context, session grokSessionContext) context.Context {
	if strings.TrimSpace(session.Key) == "" {
		return ctx
	}
	return context.WithValue(ctx, grokSessionContextKey{}, session)
}

// prepareGrokSession produces a stable, tenant- and model-isolated upstream
// identity. Explicit client identities permit encrypted reasoning replay;
// message-prefix fallback identities are affinity-only.
func prepareGrokSession(r *http.Request, model, explicit string, messages []ChatMessage) grokSessionContext {
	seed := strings.TrimSpace(explicit)
	if seed == "" && r != nil {
		for _, header := range []string{"x-grok-session-id", "x-grok-conv-id", "x-session-id", "session-id"} {
			if seed = strings.TrimSpace(r.Header.Get(header)); seed != "" {
				break
			}
		}
	}
	explicitSession := seed != ""
	if seed == "" {
		var system, firstUser string
		for _, message := range messages {
			role := strings.ToLower(strings.TrimSpace(message.Role))
			text := strings.TrimSpace(chatMessageContentText(message.Content))
			if text == "" {
				continue
			}
			if (role == "system" || role == "developer") && system == "" {
				system = truncateSessionAnchor(text, 100)
			}
			if role == "user" && firstUser == "" {
				firstUser = truncateSessionAnchor(text, 200)
			}
		}
		if firstUser == "" {
			return grokSessionContext{}
		}
		seed = system + "\x00" + firstUser
	}
	owner := "anonymous"
	if r != nil {
		if value := strings.TrimSpace(middleware.APIKeyFingerprint(r.Context())); value != "" {
			owner = value
		}
	}
	source := fmt.Sprintf("orchids:grok-session:v1:%s:%s:%s", owner, normalizeModelID(model), seed)
	digest := sha256.Sum256([]byte(source))
	return grokSessionContext{Key: hex.EncodeToString(digest[:]), Replay: explicitSession, Model: normalizeModelID(model)}
}

func truncateSessionAnchor(value string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return string(runes)
}

func affinityMapKey(session grokSessionContext, provider string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" + session.Model + "\x00" + session.Key
}

func (h *Handler) affinityAccount(ctx context.Context, provider string) int64 {
	session := sessionFromContext(ctx)
	if h == nil || session.Key == "" {
		return 0
	}
	key := affinityMapKey(session, provider)
	h.sessionMu.Lock()
	entry, ok := h.affinity[key]
	if ok && time.Now().Before(entry.ExpiresAt) {
		h.sessionMu.Unlock()
		return entry.AccountID
	}
	if ok {
		delete(h.affinity, key)
	}
	h.sessionMu.Unlock()
	if h.lb == nil || h.lb.Store == nil {
		return 0
	}
	persisted, err := h.lb.Store.GetSessionAffinity(ctx, provider, session.Model, session.Key)
	if err != nil || persisted == nil || persisted.AccountID == 0 {
		return 0
	}
	h.sessionMu.Lock()
	h.affinity[key] = sessionAffinityEntry{AccountID: persisted.AccountID, ExpiresAt: persisted.ExpiresAt}
	h.sessionMu.Unlock()
	return persisted.AccountID
}

func (h *Handler) bindAffinity(ctx context.Context, provider string, accountID int64) {
	session := sessionFromContext(ctx)
	if h == nil || session.Key == "" || accountID == 0 {
		return
	}
	h.sessionMu.Lock()
	h.affinity[affinityMapKey(session, provider)] = sessionAffinityEntry{AccountID: accountID, ExpiresAt: time.Now().Add(grokSessionStateTTL)}
	h.sessionMu.Unlock()
	if h.lb != nil && h.lb.Store != nil {
		_ = h.lb.Store.SaveSessionAffinity(ctx, &store.StoredSessionAffinity{
			Provider: provider, Model: session.Model, SessionKey: session.Key, AccountID: accountID,
		}, grokSessionStateTTL)
	}
}

func replayMapKey(model, key string) string {
	return normalizeModelID(model) + "\x00" + strings.TrimSpace(key)
}

func (h *Handler) loadReasoningReplay(model, key string) string {
	if h == nil || strings.TrimSpace(key) == "" {
		return ""
	}
	h.sessionMu.Lock()
	mapKey := replayMapKey(model, key)
	entry, ok := h.replay[mapKey]
	if ok && time.Now().Before(entry.ExpiresAt) {
		h.sessionMu.Unlock()
		return entry.EncryptedContent
	}
	if ok {
		delete(h.replay, mapKey)
	}
	h.sessionMu.Unlock()
	if h.lb == nil || h.lb.Store == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	persisted, err := h.lb.Store.GetReasoningReplay(ctx, model, key)
	if err != nil || persisted == nil || strings.TrimSpace(persisted.EncryptedContent) == "" {
		return ""
	}
	h.sessionMu.Lock()
	h.replay[mapKey] = reasoningReplayEntry{EncryptedContent: persisted.EncryptedContent, ExpiresAt: persisted.ExpiresAt}
	h.sessionMu.Unlock()
	return persisted.EncryptedContent
}

func (h *Handler) storeReasoningReplay(model, key, encrypted string) {
	encrypted = strings.TrimSpace(encrypted)
	if h == nil || strings.TrimSpace(key) == "" || encrypted == "" || len(encrypted) > 8<<20 {
		return
	}
	h.sessionMu.Lock()
	h.replay[replayMapKey(model, key)] = reasoningReplayEntry{EncryptedContent: encrypted, ExpiresAt: time.Now().Add(grokSessionStateTTL)}
	h.sessionMu.Unlock()
	if h.lb != nil && h.lb.Store != nil {
		persistentStore := h.lb.Store
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = persistentStore.SaveReasoningReplay(ctx, &store.StoredReasoningReplay{
				Model: model, SessionKey: key, EncryptedContent: encrypted,
			}, grokSessionStateTTL)
		}()
	}
}

func (h *Handler) applyNativeReasoningReplay(model, key string, payload map[string]interface{}) {
	if h == nil || payload == nil || strings.TrimSpace(parseLooseStringAny(payload["previous_response_id"])) != "" {
		return
	}
	encrypted := h.loadReasoningReplay(model, key)
	if encrypted == "" {
		return
	}
	input, ok := payload["input"].([]interface{})
	if !ok {
		if text, textOK := payload["input"].(string); textOK && strings.TrimSpace(text) != "" {
			input = []interface{}{map[string]interface{}{
				"type": "message", "role": "user", "content": []interface{}{map[string]interface{}{"type": "input_text", "text": text}},
			}}
		} else {
			return
		}
	}
	if len(input) == 0 || nativeInputHasEncryptedReasoning(input) {
		return
	}
	replay := map[string]interface{}{"type": "reasoning", "summary": []interface{}{}, "content": nil, "encrypted_content": encrypted}
	insertAt := len(input)
	for index := len(input) - 1; index >= 0; index-- {
		item, _ := input[index].(map[string]interface{})
		if item != nil && strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["role"])), "user") {
			insertAt = index
			break
		}
	}
	next := make([]interface{}, 0, len(input)+1)
	next = append(next, input[:insertAt]...)
	next = append(next, replay)
	next = append(next, input[insertAt:]...)
	payload["input"] = next
	includes := interfaceSlice(payload["include"])
	found := false
	for _, value := range includes {
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(value)), "reasoning.encrypted_content") {
			found = true
		}
	}
	if !found {
		includes = append(includes, "reasoning.encrypted_content")
		payload["include"] = includes
	}
}

func nativeInputHasEncryptedReasoning(input []interface{}) bool {
	for _, raw := range input {
		item, _ := raw.(map[string]interface{})
		if item == nil || !strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["type"])), "reasoning") {
			continue
		}
		if value := strings.TrimSpace(fmt.Sprint(item["encrypted_content"])); value != "" && value != "<nil>" {
			return true
		}
	}
	return false
}

func encryptedReasoningFromResponse(raw []byte) string {
	lines := strings.Split(string(raw), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if line == "" || line == "[DONE]" {
			continue
		}
		var value interface{}
		if json.Unmarshal([]byte(line), &value) == nil {
			if encrypted := findEncryptedReasoning(value); encrypted != "" {
				return encrypted
			}
		}
	}
	var value interface{}
	if json.Unmarshal(raw, &value) == nil {
		return findEncryptedReasoning(value)
	}
	return ""
}

func findEncryptedReasoning(value interface{}) string {
	switch item := value.(type) {
	case map[string]interface{}:
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["type"])), "reasoning") {
			if encrypted := strings.TrimSpace(fmt.Sprint(item["encrypted_content"])); encrypted != "" && encrypted != "<nil>" {
				return encrypted
			}
		}
		for _, child := range item {
			if encrypted := findEncryptedReasoning(child); encrypted != "" {
				return encrypted
			}
		}
	case []interface{}:
		for _, child := range item {
			if encrypted := findEncryptedReasoning(child); encrypted != "" {
				return encrypted
			}
		}
	}
	return ""
}
