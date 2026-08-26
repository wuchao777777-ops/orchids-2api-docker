package grok

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"orchids-api/internal/util"
)

const (
	voiceWSHandshakeTimeout = 20 * time.Second
	voiceWSMessageLimit     = 16 << 20
)

var consoleVoiceUpgrader = websocket.Upgrader{
	ReadBufferSize:    32 << 10,
	WriteBufferSize:   32 << 10,
	EnableCompression: true,
	CheckOrigin:       func(*http.Request) bool { return true },
}

// HandleRealtime proxies the Console realtime speech WebSocket.
func (h *Handler) HandleRealtime(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	h.handleVoiceWebSocket(w, r, "realtime", "grok-voice-latest")
}

func (h *Handler) handleVoiceWebSocket(w http.ResponseWriter, r *http.Request, path, defaultModel string) {
	if !websocket.IsWebSocketUpgrade(r) {
		writeResponsesAPIError(w, http.StatusBadRequest, "invalid_request", "a WebSocket Upgrade request is required")
		return
	}
	modelID := normalizeModelID(firstNonEmpty(r.URL.Query().Get("model"), defaultModel))
	capability := "realtime"
	if path == "stt" {
		capability = "stt"
	}
	spec, ok := h.requireVoiceModel(w, r, modelID, capability)
	if !ok {
		return
	}
	if h == nil || h.currentClient() == nil {
		writeResponsesAPIError(w, http.StatusServiceUnavailable, "service_unavailable", "grok client not configured")
		return
	}
	sess, err := h.openConsoleAccountSession(r.Context(), nil)
	if err != nil {
		writeResponsesAPIError(w, http.StatusServiceUnavailable, "account_unavailable", "no available Grok Console account: "+err.Error())
		return
	}
	defer sess.Close()

	upstream, response, err := h.currentClient().dialConsoleVoiceWebSocket(r.Context(), sess.token, path, spec.UpstreamModel)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if markAllGrokAccountStatuses(err) {
			h.markAccountStatus(r.Context(), sess.acc, err)
		}
		writeResponsesAPIError(w, upstreamHTTPResponseStatus(err), "upstream_error", err.Error())
		return
	}
	defer upstream.Close()
	client, err := consoleVoiceUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer client.Close()
	client.SetReadLimit(voiceWSMessageLimit)
	upstream.SetReadLimit(voiceWSMessageLimit)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = client.Close()
			_ = upstream.Close()
		})
	}
	go func() {
		<-ctx.Done()
		closeBoth()
	}()

	errCh := make(chan error, 2)
	go func() { errCh <- pumpVoiceWebSocket(client, upstream) }()
	go func() { errCh <- pumpVoiceWebSocket(upstream, client) }()
	_ = <-errCh
	closeBoth()
}

func pumpVoiceWebSocket(source, destination *websocket.Conn) error {
	for {
		messageType, payload, err := source.ReadMessage()
		if err != nil {
			return err
		}
		if err := destination.WriteMessage(messageType, payload); err != nil {
			return err
		}
	}
}

func (c *Client) dialConsoleVoiceWebSocket(ctx context.Context, token, path, model string) (*websocket.Conn, *http.Response, error) {
	if c == nil {
		return nil, nil, fmt.Errorf("grok client not configured")
	}
	// The current egress manager owns HTTP clients but not WebSocket dials. Do
	// not silently bypass an explicitly enabled fail-closed proxy pool.
	if c.egress != nil && c.egress.Enabled() {
		return nil, nil, fmt.Errorf("Console voice WebSocket is unavailable while managed egress is enabled")
	}
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path != "realtime" && path != "stt" {
		return nil, nil, fmt.Errorf("unsupported Console voice WebSocket path")
	}
	base := "https://console.x.ai/v1"
	if c.cfg != nil {
		base = c.cfg.GrokConsoleBaseURLOrDefault()
	}
	endpoint, err := url.Parse(strings.TrimRight(base, "/") + "/" + path)
	if err != nil || endpoint.Host == "" {
		return nil, nil, fmt.Errorf("invalid Console voice WebSocket endpoint")
	}
	switch strings.ToLower(endpoint.Scheme) {
	case "https":
		endpoint.Scheme = "wss"
	case "http":
		endpoint.Scheme = "ws"
	case "wss", "ws":
	default:
		return nil, nil, fmt.Errorf("unsupported Console voice WebSocket scheme")
	}
	query := endpoint.Query()
	if model = strings.TrimSpace(model); model != "" {
		query.Set("model", model)
	}
	endpoint.RawQuery = query.Encode()

	proofURL := *endpoint
	if proofURL.Scheme == "wss" {
		proofURL.Scheme = "https"
	} else {
		proofURL.Scheme = "http"
	}
	dialer := websocket.Dialer{
		HandshakeTimeout:  voiceWSHandshakeTimeout,
		Proxy:             util.ProxyFuncFromConfig(c.cfg),
		EnableCompression: true,
	}
	for attempt := 0; attempt < 2; attempt++ {
		session, cacheKey, sessionErr := c.dpopSession(ctx, token)
		if sessionErr != nil {
			return nil, nil, sessionErr
		}
		proofRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, proofURL.String(), nil)
		if requestErr != nil {
			return nil, nil, requestErr
		}
		proofRequest.Header = c.consoleHeaders(token)
		proofRequest.Header.Set("x-cluster", "https://us-east-1.api.x.ai")
		proofRequest.Header.Set("Cache-Control", "no-cache")
		proofRequest.Header.Set("Pragma", "no-cache")
		proofRequest.Header.Set("Sec-Fetch-Mode", "websocket")
		proofRequest.Header.Set("Sec-Fetch-Dest", "empty")
		if requestErr := applyDPoPAuthorization(proofRequest, session); requestErr != nil {
			return nil, nil, requestErr
		}
		connection, response, dialErr := dialer.DialContext(ctx, endpoint.String(), proofRequest.Header)
		if dialErr == nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			return connection, response, nil
		}
		if response == nil {
			return nil, nil, fmt.Errorf("dial Console voice WebSocket: %w", dialErr)
		}
		raw, headers := readBoundedResponse(response)
		dpopChallenge := response.StatusCode == http.StatusUnauthorized ||
			(response.StatusCode == http.StatusForbidden && IsDPoPProofRequiredBody(raw))
		if dpopChallenge && attempt == 0 {
			c.dpop.invalidate(cacheKey, session.accessToken)
			continue
		}
		return nil, response, newUpstreamError(response.StatusCode, headers, raw, "")
	}
	return nil, nil, fmt.Errorf("Console voice WebSocket DPoP retry exhausted")
}
