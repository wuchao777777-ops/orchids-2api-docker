package grok

import (
	"context"
	"fmt"
	"github.com/goccy/go-json"
	"io"
	"log/slog"
	"net/http"
	"orchids-api/internal/debug"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"orchids-api/internal/util"
)

func appendChatCompletionChunk(dst []byte, id string, created int64, model, role, content string, finish string, hasFinish bool) []byte {
	dst = append(dst, `{"id":`...)
	dst = strconv.AppendQuote(dst, id)
	dst = append(dst, `,"object":"chat.completion.chunk","created":`...)
	dst = strconv.AppendInt(dst, created, 10)
	dst = append(dst, `,"model":`...)
	dst = strconv.AppendQuote(dst, model)
	dst = append(dst, `,"choices":[{"index":0,"delta":`...)
	switch {
	case role != "":
		dst = append(dst, `{"role":`...)
		dst = strconv.AppendQuote(dst, role)
		dst = append(dst, '}')
	case content != "":
		dst = append(dst, `{"content":`...)
		dst = strconv.AppendQuote(dst, content)
		dst = append(dst, '}')
	default:
		dst = append(dst, `{}`...)
	}
	dst = append(dst, `,"logprobs":null,"finish_reason":`...)
	if hasFinish {
		dst = strconv.AppendQuote(dst, finish)
	} else {
		dst = append(dst, `null`...)
	}
	dst = append(dst, `}]}`...)
	return dst
}

func detectPublicBaseURL(r *http.Request) string {
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return ""
	}
	return proto + "://" + host
}

func isThinkingResponse(resp map[string]interface{}) bool {
	if resp == nil {
		return false
	}
	raw, ok := resp["isThinking"]
	if !ok {
		return false
	}
	val, err := parseLooseBoolAnyForField(raw, "isThinking")
	if err != nil {
		return false
	}
	return val
}

func (h *Handler) defaultChatStream() bool {
	if h == nil || h.cfg == nil {
		return true
	}
	return h.cfg.ChatDefaultStream()
}

func (h *Handler) applyDefaultChatStream(req *ChatCompletionsRequest) {
	if req == nil {
		return
	}
	if req.StreamProvided {
		return
	}
	req.Stream = h.defaultChatStream()
}

func (h *Handler) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ChatCompletionsRequest
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	debugEnabled := h != nil && h.cfg != nil && h.cfg.DebugEnabled
	debugLogSSE := h != nil && h.cfg != nil && h.cfg.DebugLogSSE
	logger := debug.New(debugEnabled, debugLogSSE)
	defer logger.Close()
	logger.LogIncomingRequest(req)
	req.Model = normalizeModelID(req.Model)
	if req.ImageConfig != nil {
		req.ImageConfig.Normalize()
	}
	h.applyDefaultChatStream(&req)
	if err := req.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ImageConfig != nil && req.Model != "grok-imagine-1.0" && req.Model != "grok-imagine-1.0-edit" {
		originalModel := req.Model
		req.Model = "grok-imagine-1.0"
		slog.Info("Auto mapped image_config request to image model", "from", originalModel, "to", req.Model)
	}
	if err := h.ensureModelEnabled(r.Context(), req.Model); err != nil {
		msg := strings.TrimSpace(err.Error())
		if strings.EqualFold(msg, "model not found") && h.tryAutoRegisterModel(r.Context(), req.Model) {
			if retryErr := h.ensureModelEnabled(r.Context(), req.Model); retryErr != nil {
				http.Error(w, modelValidationMessage(req.Model, retryErr), http.StatusBadRequest)
				return
			}
		} else {
			http.Error(w, modelValidationMessage(req.Model, err), http.StatusBadRequest)
			return
		}
	}

	spec, ok := ResolveModelOrDynamic(req.Model)
	if !ok {
		http.Error(w, modelNotFoundMessage(req.Model), http.StatusBadRequest)
		return
	}

	publicBase := detectPublicBaseURL(r)
	if spec.IsImage {
		prompt, imageURLs := extractPromptAndImageURLs(req.Messages)
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			http.Error(w, "prompt is required", http.StatusBadRequest)
			return
		}
		imageCfg := req.ImageConfig
		if imageCfg == nil {
			imageCfg = &ImageConfig{}
			imageCfg.Normalize()
		}
		if imageCfg.N < 1 || imageCfg.N > 10 {
			http.Error(w, "image_config.n must be between 1 and 10", http.StatusBadRequest)
			return
		}
		if req.Stream && imageCfg.N > 2 {
			http.Error(w, "streaming is only supported when image_config.n=1 or n=2", http.StatusBadRequest)
			return
		}
		if imageCfg.ResponseFormat == "" {
			imageCfg.ResponseFormat = "url"
		}
		if imageCfg.ResponseFormat != "" {
			imageCfg.ResponseFormat = normalizeImageResponseFormat(imageCfg.ResponseFormat)
		}
		if imageCfg.Size != "" {
			size, err := normalizeImageSize(imageCfg.Size)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			imageCfg.Size = size
		} else {
			imageCfg.Size = "1024x1024"
		}

		if spec.ID == "grok-imagine-1.0-edit" {
			if len(imageURLs) == 0 {
				http.Error(w, "image_url is required for image edits", http.StatusBadRequest)
				return
			}
			// Keep grok2api compatibility in chat mode: use the last provided image as edit source.
			editInputs := imageURLs
			if len(editInputs) > 1 {
				editInputs = editInputs[len(editInputs)-1:]
			}
			h.handleChatImageEdit(r.Context(), w, req, spec, prompt, editInputs)
			return
		}

		genReq := ImagesGenerationsRequest{
			Model:          req.Model,
			Prompt:         prompt,
			N:              imageCfg.N,
			Size:           imageCfg.Size,
			Stream:         req.Stream,
			ResponseFormat: imageCfg.ResponseFormat,
		}
		h.serveImagesGenerations(r.Context(), w, genReq, publicBase)
		return
	}

	text, attachments, err := extractMessageAndAttachments(req.Messages, spec.IsVideo)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userPrompt := strings.TrimSpace(extractLastUserText(req.Messages))
	if userPrompt == "" {
		userPrompt = strings.TrimSpace(text)
	}
	slog.Debug("grok chat prompt context",
		"model", req.Model,
		"history_chars", utf8.RuneCountInString(strings.TrimSpace(text)),
		"latest_user_chars", utf8.RuneCountInString(userPrompt),
		"attachments", len(attachments),
		"stream", req.Stream,
	)
	if strings.TrimSpace(text) == "" && len(attachments) == 0 {
		http.Error(w, "empty message", http.StatusBadRequest)
		return
	}

	if spec.IsVideo {
		if cfg, err := validateVideoConfig(req.VideoConfig); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		} else {
			req.VideoConfig = cfg
		}
	}

	sess, err := h.openChatAccountSession(r.Context())
	if err != nil {
		http.Error(w, "no available grok token: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer sess.Close()

	buildPayload := func(token string) (map[string]interface{}, error) {
		fileAttachments := []string(nil)
		if !spec.IsVideo {
			var upErr error
			fileAttachments, upErr = h.uploadAttachmentInputs(r.Context(), token, attachments)
			if upErr != nil {
				return nil, fmt.Errorf("attachment upload failed: %w", upErr)
			}
		}
		return h.buildChatPayload(r.Context(), token, spec, text, fileAttachments, attachments, req.VideoConfig, &req)
	}

	payload, err := buildPayload(sess.token)
	if err != nil {
		h.markAccountStatus(r.Context(), sess.acc, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	logger.LogUpstreamRequest(h.client.baseURL()+defaultChatPath, debugHeaderMap(h.client.headers(sess.token)), payload)

	resp, err := h.doChatWithAutoSwitchRebuild(r.Context(), sess, &payload, buildPayload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	h.syncGrokQuota(sess.acc, resp.Header)

	hasAttachments := len(attachments) > 0
	if req.Stream {
		h.streamChat(w, req.Model, spec, sess.token, publicBase, hasAttachments, resp.Body, logger)
		return
	}
	h.collectChat(w, req.Model, spec, sess.token, publicBase, resp.Body, logger)
}

func (h *Handler) buildChatPayload(
	ctx context.Context,
	token string,
	spec ModelSpec,
	text string,
	fileAttachments []string,
	attachmentInputs []AttachmentInput,
	videoCfg *VideoConfig,
	req *ChatCompletionsRequest,
) (map[string]interface{}, error) {
	payload := h.client.chatPayload(spec, text, true, 0)
	if len(fileAttachments) > 0 {
		payload["fileAttachments"] = fileAttachments
	}
	if req != nil {
		override := map[string]interface{}{}
		if req.Temperature != nil {
			override["temperature"] = *req.Temperature
		}
		if req.TopP != nil {
			override["topP"] = *req.TopP
		}
		if req.ReasoningEffort != nil {
			if v := strings.TrimSpace(*req.ReasoningEffort); v != "" {
				override["reasoningEffort"] = strings.ToLower(v)
			}
		}
		if len(override) > 0 {
			responseMetadata, _ := payload["responseMetadata"].(map[string]interface{})
			if responseMetadata == nil {
				responseMetadata = map[string]interface{}{}
				payload["responseMetadata"] = responseMetadata
			}
			modelConfigOverride, _ := responseMetadata["modelConfigOverride"].(map[string]interface{})
			if modelConfigOverride == nil {
				modelConfigOverride = map[string]interface{}{}
				responseMetadata["modelConfigOverride"] = modelConfigOverride
			}
			for k, v := range override {
				modelConfigOverride[k] = v
			}
		}
	}

	if !spec.IsVideo {
		return payload, nil
	}

	if videoCfg == nil {
		videoCfg = &VideoConfig{}
	}
	videoCfg.Normalize()

	postType := "MEDIA_POST_TYPE_VIDEO"
	postPrompt := text
	postMediaURL := ""
	for _, item := range attachmentInputs {
		if !strings.EqualFold(strings.TrimSpace(item.Type), "image") {
			continue
		}
		if strings.TrimSpace(item.Data) == "" {
			continue
		}
		_, fileURI, upErr := h.uploadSingleInput(ctx, token, item.Data)
		if upErr != nil {
			return nil, fmt.Errorf("video image upload failed: %w", upErr)
		}
		u := strings.TrimSpace(fileURI)
		if u == "" {
			return nil, fmt.Errorf("video image upload failed: empty file uri")
		}
		if !strings.HasPrefix(strings.ToLower(u), "http://") && !strings.HasPrefix(strings.ToLower(u), "https://") {
			u = "https://assets.grok.com/" + strings.TrimLeft(u, "/")
		}
		postType = "MEDIA_POST_TYPE_IMAGE"
		postPrompt = ""
		postMediaURL = u
		break
	}

	postID, err := h.client.createMediaPost(ctx, token, postType, postPrompt, postMediaURL)
	if err != nil {
		return nil, fmt.Errorf("create video post failed: %w", err)
	}

	modeFlag := videoPresetFlag(videoCfg.Preset)
	message := strings.TrimSpace(text)
	if modeFlag != "" {
		message = strings.TrimSpace(message + " " + modeFlag)
	}

	return map[string]interface{}{
		"temporary":        true,
		"modelName":        spec.UpstreamModel,
		"message":          message,
		"toolOverrides":    map[string]interface{}{"videoGen": true},
		"enableSideBySide": true,
		"deviceEnvInfo": map[string]interface{}{
			"darkModeEnabled":  false,
			"devicePixelRatio": 2,
			"screenWidth":      1920,
			"screenHeight":     1080,
			"viewportWidth":    1920,
			"viewportHeight":   1080,
		},
		"responseMetadata": map[string]interface{}{
			"modelConfigOverride": map[string]interface{}{
				"modelMap": map[string]interface{}{
					"videoGenModelConfig": map[string]interface{}{
						"aspectRatio":    videoCfg.AspectRatio,
						"parentPostId":   postID,
						"resolutionName": videoCfg.ResolutionName,
						"videoLength":    videoCfg.VideoLength,
					},
				},
			},
		},
	}, nil
}

func videoPresetFlag(preset string) string {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "fun":
		return "--mode=extremely-crazy"
	case "normal":
		return "--mode=normal"
	case "spicy":
		return "--mode=extremely-spicy-or-crazy"
	case "", "custom":
		return "--mode=custom"
	default:
		return "--mode=custom"
	}
}

func (h *Handler) uploadAttachmentInputs(ctx context.Context, token string, inputs []AttachmentInput) ([]string, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(inputs))
	for _, item := range inputs {
		fileID, fileURI, err := h.uploadSingleInput(ctx, token, item.Data)
		if err != nil {
			return nil, err
		}
		id := strings.TrimSpace(fileID)
		if id == "" {
			id = strings.TrimSpace(fileURI)
		}
		if id != "" {
			out = append(out, id)
		}
	}
	return uniqueStrings(out), nil
}

func (h *Handler) uploadSingleInput(ctx context.Context, token, input string) (string, string, error) {
	data := strings.TrimSpace(input)
	if data == "" {
		return "", "", fmt.Errorf("empty attachment")
	}
	if isRemoteURL(data) {
		var err error
		proxyFunc := http.ProxyFromEnvironment
		if h != nil && h.cfg != nil {
			proxyFunc = util.ProxyFunc(h.cfg.ProxyHTTP, h.cfg.ProxyHTTPS, h.cfg.ProxyUser, h.cfg.ProxyPass, h.cfg.ProxyBypass)
		}
		data, err = fetchRemoteAsDataURI(data, 30*time.Second, proxyFunc)
		if err != nil {
			return "", "", err
		}
	}
	filename, contentBase64, mime, err := parseDataURI(data)
	if err != nil {
		// Fallback for plain base64 payloads.
		filename = "file.bin"
		mime = "application/octet-stream"
		contentBase64 = data
	}
	return h.client.uploadFile(ctx, token, filename, mime, contentBase64)
}

type streamMarkupFilter struct {
	pending  string
	inTool   bool
	inRender bool
}

const (
	streamToolStartTag   = "xai:tool_usage_card"
	streamToolEndTag     = "</xai:tool_usage_card>"
	streamRenderStartTag = "<grok:render"
	streamRenderEndTag   = "</grok:render>"
)

func asciiLowerByte(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if asciiLowerByte(a[i]) != asciiLowerByte(b[i]) {
			return false
		}
	}
	return true
}

func indexFoldASCII(s, sub string) int {
	if sub == "" {
		return 0
	}
	n := len(sub)
	if len(s) < n {
		return -1
	}
	first := asciiLowerByte(sub[0])
	for i := 0; i <= len(s)-n; i++ {
		if asciiLowerByte(s[i]) != first {
			continue
		}
		if equalFoldASCII(s[i:i+n], sub) {
			return i
		}
	}
	return -1
}

// Streaming sanitizer/tokenizer.
// Goal: never leak tool/render markup, never corrupt UTF-8, and keep streaming responsive.
func (f *streamMarkupFilter) feed(chunk string) string {
	if f == nil || chunk == "" {
		return ""
	}
	f.pending += chunk
	// Bound memory
	if len(f.pending) > 64*1024 {
		f.pending = f.pending[len(f.pending)-64*1024:]
	}

	var out strings.Builder
	endsWithNewline := false
	writeOut := func(s string) {
		if s == "" {
			return
		}
		out.WriteString(s)
		endsWithNewline = s[len(s)-1] == '\n'
	}

	for {
		if f.inTool {
			end := indexFoldASCII(f.pending, streamToolEndTag)
			if end < 0 {
				// wait for more data
				break
			}
			raw := f.pending[:end+len(streamToolEndTag)]
			if line := extractToolUsageCardText(raw); line != "" {
				if out.Len() > 0 && !endsWithNewline {
					writeOut("\n")
				}
				writeOut(line)
				writeOut("\n")
			}
			f.pending = f.pending[end+len(streamToolEndTag):]
			f.inTool = false
			continue
		}
		if f.inRender {
			end := indexFoldASCII(f.pending, streamRenderEndTag)
			if end < 0 {
				break
			}
			f.pending = f.pending[end+len(streamRenderEndTag):]
			f.inRender = false
			continue
		}

		idxTool := indexFoldASCII(f.pending, streamToolStartTag)
		idxRender := indexFoldASCII(f.pending, streamRenderStartTag)
		idx := -1
		kind := ""
		if idxTool >= 0 {
			idx = idxTool
			kind = "tool"
		}
		if idxRender >= 0 && (idx < 0 || idxRender < idx) {
			idx = idxRender
			kind = "render"
		}

		if idx < 0 {
			// No markers. Emit everything except a tail to avoid cutting potential markers.
			keep := 512
			if len(f.pending) <= keep {
				break
			}
			safe := validUTF8Prefix(f.pending[:len(f.pending)-keep])
			safe = stripLeadingAngleNoise(sanitizeText(safe))
			if safe != "" {
				writeOut(safe)
			}
			f.pending = f.pending[len(f.pending)-keep:]
			break
		}

		// Emit prefix before marker
		prefix := validUTF8Prefix(f.pending[:idx])
		prefix = stripLeadingAngleNoise(sanitizeText(prefix))
		if prefix != "" {
			writeOut(prefix)
		}
		f.pending = f.pending[idx:]
		if kind == "tool" {
			f.inTool = true
		} else {
			f.inRender = true
		}
	}

	return out.String()
}

func (f *streamMarkupFilter) flush() string {
	if f == nil {
		return ""
	}
	if f.inTool || f.inRender {
		return ""
	}
	if strings.TrimSpace(f.pending) == "" {
		return ""
	}
	out := stripLeadingAngleNoise(sanitizeText(stripToolAndRenderMarkup(validUTF8Prefix(f.pending))))
	f.pending = ""
	return out
}

func validUTF8Prefix(s string) string {
	if s == "" || utf8.ValidString(s) {
		return s
	}
	// Trim bytes until valid UTF-8.
	b := []byte(s)
	for len(b) > 0 {
		b = b[:len(b)-1]
		if utf8.Valid(b) {
			return string(b)
		}
	}
	return ""
}

func collapseDuplicatedLongChunk(text string) string {
	original := strings.TrimSpace(stripZeroWidth(text))
	if original == "" {
		return text
	}
	current := original
	for {
		next, ok := collapseDuplicatedLongChunkOnce(current)
		if !ok {
			break
		}
		current = next
	}
	if current == original {
		return text
	}
	return current
}

func collapseDuplicatedLongChunkOnce(trimmed string) (string, bool) {
	runes := []rune(trimmed)
	if len(runes) < 24 {
		return "", false
	}

	for sep := 0; sep <= 3; sep++ {
		total := len(runes) - sep
		if total <= 0 || total%2 != 0 {
			continue
		}
		half := total / 2
		first := strings.TrimSpace(stripZeroWidth(string(runes[:half])))
		second := strings.TrimSpace(stripZeroWidth(string(runes[half+sep:])))
		mid := strings.TrimSpace(stripZeroWidth(string(runes[half : half+sep])))
		if first == "" || second == "" || first != second || mid != "" {
			continue
		}
		if utf8.RuneCountInString(first) < 12 {
			return "", false
		}
		return first, true
	}
	return "", false
}

func stripZeroWidth(s string) string {
	if s == "" {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch r {
		case '\u200b', '\u200c', '\u200d', '\ufeff':
			return -1
		default:
			return r
		}
	}, s)
}

func sanitizeUpstreamText(raw string) string {
	return stripLeadingAngleNoise(sanitizeText(stripToolAndRenderMarkup(raw)))
}

const streamImageRefTailKeep = 1024

type streamTextImageRefCollector struct {
	tail      string
	urls      []string
	assets    []string
	seenURLs  map[string]struct{}
	seenAsset map[string]struct{}
}

func newStreamTextImageRefCollector() *streamTextImageRefCollector {
	return &streamTextImageRefCollector{
		urls:      make([]string, 0, 8),
		assets:    make([]string, 0, 8),
		seenURLs:  map[string]struct{}{},
		seenAsset: map[string]struct{}{},
	}
}

func streamRefMaybePresent(s string) bool {
	if s == "" {
		return false
	}
	return strings.Contains(s, "http://") ||
		strings.Contains(s, "https://") ||
		strings.Contains(s, "assets.grok.com") ||
		strings.Contains(s, "/generated/") ||
		strings.Contains(s, "users/") ||
		strings.Contains(s, "user/") ||
		strings.Contains(s, ".png") ||
		strings.Contains(s, ".jpg") ||
		strings.Contains(s, ".jpeg") ||
		strings.Contains(s, ".webp") ||
		strings.Contains(s, ".gif")
}

func keepLastBytes(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[len(s)-maxLen:]
}

func (c *streamTextImageRefCollector) feed(raw string) {
	if c == nil || strings.TrimSpace(raw) == "" {
		return
	}
	scan := raw
	if c.tail != "" {
		scan = c.tail + raw
	}
	if streamRefMaybePresent(scan) {
		for _, u := range extractImageURLsFromText(scan) {
			u = strings.TrimSpace(u)
			if u == "" {
				continue
			}
			if _, ok := c.seenURLs[u]; ok {
				continue
			}
			c.seenURLs[u] = struct{}{}
			c.urls = append(c.urls, u)
		}
		for _, p := range extractGrokAssetPathsFromText(scan) {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, ok := c.seenAsset[p]; ok {
				continue
			}
			c.seenAsset[p] = struct{}{}
			c.assets = append(c.assets, p)
		}
	}
	c.tail = keepLastBytes(scan, streamImageRefTailKeep)
}

func (c *streamTextImageRefCollector) emit(emitURL func(string)) {
	if c == nil || emitURL == nil {
		return
	}
	for _, u := range c.urls {
		emitURL(u)
	}
	for _, p := range c.assets {
		emitURL("https://assets.grok.com/" + strings.TrimPrefix(p, "/"))
	}
}

func forEachImageCandidateFromValue(value interface{}, includeStructured bool, includeAssetLike bool, assetLimit int, emitURL func(string)) {
	if emitURL == nil {
		return
	}
	if includeStructured {
		for _, u := range extractImageURLs(value) {
			emitURL(u)
		}
	}
	for _, u := range extractRenderableImageLinks(value) {
		emitURL(u)
	}
	if !includeAssetLike {
		return
	}
	for _, p := range collectAssetLikeStrings(value, assetLimit) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if isLikelyImageURL(p) {
			emitURL(p)
			continue
		}
		if isLikelyImageAssetPath(p) {
			emitURL("https://assets.grok.com/" + strings.TrimPrefix(p, "/"))
		}
	}
}

// NOTE: streamMarkupFilter.feed is implemented earlier in this file.

func debugHeaderMap(headers http.Header) map[string]string {
	out := make(map[string]string, len(headers))
	for k, values := range headers {
		if strings.EqualFold(k, "Cookie") {
			out[k] = "[redacted]"
			continue
		}
		out[k] = strings.Join(values, ", ")
	}
	return out
}

func streamMessageDelta(previous, current string) string {
	if current == "" || current == previous {
		return ""
	}
	if previous == "" {
		return current
	}
	if strings.HasPrefix(current, previous) {
		return current[len(previous):]
	}
	prevRunes := []rune(previous)
	currRunes := []rune(current)
	maxPrefix := len(prevRunes)
	if len(currRunes) < maxPrefix {
		maxPrefix = len(currRunes)
	}
	prefix := 0
	for prefix < maxPrefix && prevRunes[prefix] == currRunes[prefix] {
		prefix++
	}
	if prefix >= len(currRunes) {
		return ""
	}
	return string(currRunes[prefix:])
}

func commonPrefixText(a, b string) string {
	if a == "" || b == "" {
		return ""
	}
	ar := []rune(a)
	br := []rune(b)
	limit := len(ar)
	if len(br) < limit {
		limit = len(br)
	}
	idx := 0
	for idx < limit && ar[idx] == br[idx] {
		idx++
	}
	if idx == 0 {
		return ""
	}
	return string(ar[:idx])
}

func (h *Handler) streamChat(w http.ResponseWriter, model string, spec ModelSpec, token string, publicBase string, hasAttachments bool, body io.Reader, logger *debug.Logger) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	id := "chatcmpl_" + randomHex(8)
	sentRole := false
	lastMessage := ""
	sentAny := false
	// Image URL stream handling: prefer full image variants over -part-0 previews.
	seenFull := map[string]bool{}
	pendingPart := map[string]string{}
	emitted := map[string]bool{}
	sawModelMessage := false
	lastTextChunkNorm := ""
	var tokenFallback strings.Builder
	emittedModelMessage := ""
	pendingModelMessage := ""

	var mf *streamMarkupFilter
	if !hasAttachments {
		mf = &streamMarkupFilter{}
	}
	textRefCollector := newStreamTextImageRefCollector()
	chunkScratch := make([]byte, 0, 256)

	emitChunk := func(role, content string, finish string, hasFinish bool) {
		raw := appendChatCompletionChunk(chunkScratch[:0], id, time.Now().Unix(), model, role, content, finish, hasFinish)
		chunkScratch = raw[:0]
		writeSSEBytes(w, "", raw)
		if logger != nil {
			logger.LogOutputSSE("", string(raw))
		}
		if flusher != nil {
			flusher.Flush()
		}
		sentAny = true
	}

	var emitTextChunk func(string)
	emitTextChunk = func(content string) {
		collapsed := collapseDuplicatedLongChunk(content)
		if collapsed != content && h != nil && h.cfg != nil && h.cfg.DebugEnabled {
			slog.Debug("grok stream collapsed duplicated text chunk",
				"before_chars", utf8.RuneCountInString(strings.TrimSpace(content)),
				"after_chars", utf8.RuneCountInString(strings.TrimSpace(collapsed)))
		}
		content = collapsed
		norm := strings.TrimSpace(content)
		if norm == "" {
			return
		}

		if norm == lastTextChunkNorm && utf8.RuneCountInString(norm) >= 12 {
			if h != nil && h.cfg != nil && h.cfg.DebugEnabled {
				slog.Debug("grok stream skip duplicate text chunk", "chars", utf8.RuneCountInString(norm))
			}
			return
		}
		emitChunk("", content, "", false)
		if utf8.RuneCountInString(norm) >= 12 {
			lastTextChunkNorm = norm
		} else {
			lastTextChunkNorm = ""
		}
	}

	emitCleanText := func(raw string) {
		if raw == "" {
			return
		}
		if mf == nil {
			if cleaned := sanitizeUpstreamText(raw); cleaned != "" {
				emitTextChunk(cleaned)
			}
			return
		}
		if cleaned := mf.feed(raw); cleaned != "" {
			cleaned = stripLeadingAngleNoise(cleaned)
			if cleaned != "" {
				emitTextChunk(cleaned)
			}
		}
	}

	emitImageURL := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		// Track preview vs full variants on the raw upstream URL.
		if strings.Contains(raw, "-part-0/") {
			full := strings.ReplaceAll(raw, "-part-0/", "/")
			if seenFull[full] {
				return
			}
			pendingPart[full] = raw
			return
		}
		seenFull[raw] = true
		if emitted[raw] {
			return
		}

		val, errV := h.imageOutputValue(context.Background(), token, raw, "url")
		if errV != nil || strings.TrimSpace(val) == "" {
			val = raw
		}
		if publicBase != "" && strings.HasPrefix(val, "/") {
			val = publicBase + val
		}
		if md := formatImageMarkdown(val); md != "" {
			emitChunk("", md, "", false)
			emitted[raw] = true
		}
	}

	err := parseUpstreamLines(body, func(resp map[string]interface{}) error {
		if logger != nil {
			if raw, err := json.Marshal(resp); err == nil {
				logger.LogUpstreamSSE("response", string(raw))
			}
		}
		if !sentRole {
			emitChunk("assistant", "", "", false)
			sentRole = true
		}
		if tokenDelta, ok := resp["token"].(string); ok && tokenDelta != "" {
			if isThinkingResponse(resp) {
				// Suppress thinking tokens to avoid leaking chain-of-thought.
				return nil
			}
			textRefCollector.feed(tokenDelta)
			if !sawModelMessage {
				tokenFallback.WriteString(tokenDelta)
			}
		}
		if mr, ok := resp["modelResponse"].(map[string]interface{}); ok {
			if msg, ok := mr["message"].(string); ok && strings.TrimSpace(msg) != "" && msg != lastMessage {
				lastMessage = msg
				sawModelMessage = true
				textRefCollector.feed(msg)
				if pendingModelMessage != "" {
					stable := commonPrefixText(pendingModelMessage, msg)
					if stable != "" && stable != emittedModelMessage {
						if delta := streamMessageDelta(emittedModelMessage, stable); delta != "" {
							emitCleanText(delta)
							emittedModelMessage = stable
						}
					}
				}
				pendingModelMessage = msg
				if strings.Contains(msg, "<grok:render") || strings.Contains(msg, "tool_usage_card") {
					slog.Debug("grok message contains render/tool markup", "has_modelResponse", true)
				}
			}
			forEachImageCandidateFromValue(mr, true, true, 80, emitImageURL)
		}
		// Broader fallback: sometimes URLs live outside modelResponse.
		forEachImageCandidateFromValue(resp, true, true, 120, emitImageURL)
		if spec.IsVideo {
			if progress, videoURL, _, ok := extractVideoProgress(resp); ok {
				if progress > 0 && progress < 100 {
					emitChunk("", fmt.Sprintf("正在生成视频中，当前进度%d%%\n", progress), "", false)
				}
				if progress >= 100 && strings.TrimSpace(videoURL) != "" {
					finalURL := strings.TrimSpace(videoURL)
					if name, err := h.cacheMediaURL(context.Background(), token, finalURL, "video"); err == nil && name != "" {
						finalURL = "/grok/v1/files/video/" + name
					}
					emitChunk("", finalURL, "", false)
				}
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("grok stream parse failed", "error", err)
		if !sentAny {
			http.Error(w, "stream parse error: "+err.Error(), http.StatusBadGateway)
			return
		}
		emitChunk("", "\n[上游响应解析失败]\n", "", false)
	}

	// Flush any remaining buffered text (avoids "no content" when stream ends quickly).
	if mf != nil {
		if pendingModelMessage != "" && pendingModelMessage != emittedModelMessage {
			if delta := streamMessageDelta(emittedModelMessage, pendingModelMessage); delta != "" {
				emitCleanText(delta)
			}
		}
		if !sawModelMessage && tokenFallback.Len() > 0 {
			emitCleanText(tokenFallback.String())
		}
		if tail := mf.flush(); tail != "" {
			tail = stripLeadingAngleNoise(tail)
			if tail != "" {
				emitTextChunk(tail)
			}
		}
		if tokenFallback.Len() > 0 && !sawModelMessage && h != nil && h.cfg != nil && h.cfg.DebugEnabled {
			slog.Debug("grok stream fallback used token deltas (no modelResponse)", "model", model)
		}
	} else {
		if pendingModelMessage != "" && pendingModelMessage != emittedModelMessage {
			if delta := streamMessageDelta(emittedModelMessage, pendingModelMessage); delta != "" {
				if cleaned := sanitizeUpstreamText(delta); cleaned != "" {
					emitTextChunk(cleaned)
				}
			}
		} else if !sawModelMessage && tokenFallback.Len() > 0 {
			if cleaned := sanitizeUpstreamText(tokenFallback.String()); cleaned != "" {
				emitTextChunk(cleaned)
			}
		}
	}
	// Emit any pending part-0 previews only if we never saw a full variant.
	// Try to fetch/emit the full variant first; if it doesn't exist, fall back to the preview.
	for full, part := range pendingPart {
		if seenFull[full] {
			continue
		}
		// Try full (cache through this server for client reachability).
		if name, err := h.cacheMediaURL(context.Background(), token, full, "image"); err == nil && name != "" {
			val := "/grok/v1/files/image/" + name
			if publicBase != "" && strings.HasPrefix(val, "/") {
				val = publicBase + val
			}
			if md := formatImageMarkdown(val); md != "" {
				emitChunk("", md, "", false)
			}
			continue
		}
		// Fall back to preview.
		emitImageURL(part)
	}

	// Final pass: emit URL/path candidates found in incremental text collector.
	textRefCollector.emit(emitImageURL)

	emitChunk("", "", "stop", true)
	writeSSEBytes(w, "", []byte("[DONE]"))
	if logger != nil {
		logger.LogOutputSSE("", "[DONE]")
	}
	if flusher != nil {
		flusher.Flush()
	}
}

func (h *Handler) collectChat(w http.ResponseWriter, model string, spec ModelSpec, token string, publicBase string, body io.Reader, logger *debug.Logger) {
	id := "chatcmpl_" + randomHex(8)
	lastMessage := ""
	videoURL := ""
	imageCandidates := make([]string, 0, 8)
	imageCandidateSet := map[string]struct{}{}
	var tokenContent strings.Builder
	addImageCandidate := func(raw string) {
		u := strings.TrimSpace(raw)
		if u == "" {
			return
		}
		if _, ok := imageCandidateSet[u]; ok {
			return
		}
		imageCandidateSet[u] = struct{}{}
		imageCandidates = append(imageCandidates, u)
	}

	err := parseUpstreamLines(body, func(resp map[string]interface{}) error {
		if logger != nil {
			if raw, err := json.Marshal(resp); err == nil {
				logger.LogUpstreamSSE("response", string(raw))
			}
		}
		isThinking := isThinkingResponse(resp)
		if tokenDelta, ok := resp["token"].(string); ok && tokenDelta != "" {
			if isThinking {
				// Suppress thinking tokens to avoid leaking chain-of-thought in OpenAI-compatible output.
				return nil
			}
			tokenContent.WriteString(tokenDelta)
		}
		if mr, ok := resp["modelResponse"].(map[string]interface{}); ok {
			if msg, ok := mr["message"].(string); ok && strings.TrimSpace(msg) != "" && msg != lastMessage {
				lastMessage = msg
				if strings.Contains(msg, "<grok:render") || strings.Contains(msg, "tool_usage_card") {
					slog.Debug("grok message contains render/tool markup", "has_modelResponse", true)
				}
			}
			forEachImageCandidateFromValue(mr, true, false, 0, addImageCandidate)
		}
		forEachImageCandidateFromValue(resp, false, false, 0, addImageCandidate)
		if spec.IsVideo {
			if progress, vurl, _, ok := extractVideoProgress(resp); ok && progress >= 100 && strings.TrimSpace(vurl) != "" {
				videoURL = strings.TrimSpace(vurl)
			}
		}
		return nil
	})
	if err != nil {
		http.Error(w, "stream parse error: "+err.Error(), http.StatusBadGateway)
		return
	}

	tokenClean := sanitizeUpstreamText(tokenContent.String())
	modelClean := sanitizeUpstreamText(lastMessage)

	finalContent := modelClean
	if strings.TrimSpace(finalContent) == "" {
		finalContent = tokenClean
	}
	finalContent = collapseDuplicatedLongChunk(finalContent)

	if videoURL != "" {
		if name, err := h.cacheMediaURL(context.Background(), token, videoURL, "video"); err == nil && name != "" {
			videoURL = "/grok/v1/files/video/" + name
		}
		if strings.TrimSpace(finalContent) != "" {
			finalContent += "\n"
		}
		finalContent += videoURL
	}

	// Append any collected image links as Markdown, after text cleanup.
	imgs := normalizeImageURLs(imageCandidates, 8)
	for _, u := range imgs {
		val, errV := h.imageOutputValue(context.Background(), token, u, "url")
		if errV != nil || strings.TrimSpace(val) == "" {
			val = u
		}
		if publicBase != "" && strings.HasPrefix(val, "/") {
			val = publicBase + val
		}
		finalContent += formatImageMarkdown(val)
	}
	resp := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": finalContent,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
