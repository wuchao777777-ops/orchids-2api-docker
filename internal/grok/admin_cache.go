package grok

import (
	"context"
	"github.com/goccy/go-json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type cacheEntry struct {
	MediaType  string `json:"media_type"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	URL        string `json:"url"`
	ViewURL    string `json:"view_url"`
	PreviewURL string `json:"preview_url,omitempty"`
	Size       int64  `json:"size"`
	SizeBytes  int64  `json:"size_bytes"`
	UpdatedAt  int64  `json:"updated_at"`
	MtimeMS    int64  `json:"mtime_ms"`
}

type cacheClearRequest struct {
	MediaType string `json:"media_type"`
	Type      string `json:"type"`
}

type cacheDeleteItemRequest struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	FileName  string `json:"file_name"`
}

type cacheOnlineClearRequest struct {
	Token  string   `json:"token"`
	Tokens []string `json:"tokens"`
}

type cacheOnlineLoadAsyncRequest struct {
	Scope  string   `json:"scope"`
	Token  string   `json:"token"`
	Tokens []string `json:"tokens"`
}

var (
	cacheOnlineClearMu sync.Mutex
	cacheOnlineClearAt = map[string]int64{}
)

func setOnlineAssetClearTime(token string, ts int64) {
	token = normalizeOnlineToken(token)
	if token == "" || ts <= 0 {
		return
	}
	cacheOnlineClearMu.Lock()
	cacheOnlineClearAt[token] = ts
	cacheOnlineClearMu.Unlock()
}

func getOnlineAssetClearTime(token string) interface{} {
	token = normalizeOnlineToken(token)
	if token == "" {
		return nil
	}
	cacheOnlineClearMu.Lock()
	ts := cacheOnlineClearAt[token]
	cacheOnlineClearMu.Unlock()
	if ts <= 0 {
		return nil
	}
	return ts
}

func bytesToMB(size int64) float64 {
	if size <= 0 {
		return 0
	}
	return math.Round((float64(size)/1024.0/1024.0)*100) / 100
}

func maskCacheToken(raw string) string {
	token := strings.TrimSpace(raw)
	if token == "" {
		return ""
	}
	if len(token) <= 24 {
		return token
	}
	return token[:8] + "..." + token[len(token)-16:]
}

func parsePositiveInt(raw string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func resolveCacheMediaType(query map[string][]string) string {
	for _, key := range []string{"media_type", "type", "cache_type"} {
		v := strings.ToLower(strings.TrimSpace(firstQueryValue(query, key)))
		if v != "" {
			return v
		}
	}
	return ""
}

func firstQueryValue(query map[string][]string, key string) string {
	values, ok := query[key]
	if !ok || len(values) == 0 {
		return ""
	}
	return values[0]
}

func paginateCacheEntries(entries []cacheEntry, page int, pageSize int) ([]cacheEntry, int) {
	total := len(entries)
	if total == 0 {
		return []cacheEntry{}, 0
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = total
	}
	start := (page - 1) * pageSize
	if start >= total {
		return []cacheEntry{}, total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return entries[start:end], total
}

func (h *Handler) listCacheOnlineAccounts(r *http.Request) []map[string]interface{} {
	if h == nil || h.lb == nil || h.lb.Store == nil || r == nil {
		return []map[string]interface{}{}
	}
	accounts, err := h.lb.Store.ListAccounts(r.Context())
	if err != nil {
		return []map[string]interface{}{}
	}

	out := make([]map[string]interface{}, 0, len(accounts))
	seen := map[string]struct{}{}
	for _, acc := range accounts {
		if !isGrokAccount(acc) || acc == nil || !acc.Enabled {
			continue
		}
		token := strings.TrimSpace(grokAccountToken(acc))
		if token == "" {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, map[string]interface{}{
			"token":               token,
			"token_masked":        maskCacheToken(token),
			"pool":                inferTokenPool(acc),
			"status":              adminTokenStatusFromAccount(acc),
			"last_asset_clear_at": getOnlineAssetClearTime(token),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		left, _ := out[i]["token"].(string)
		right, _ := out[j]["token"].(string)
		return left < right
	})
	return out
}

func normalizeOnlineToken(raw string) string {
	return NormalizeSSOToken(raw)
}

func normalizeOnlineTokenList(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	seen := map[string]struct{}{}
	for _, raw := range tokens {
		token := normalizeOnlineToken(raw)
		if token == "" {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func onlineAccountInfo(accountByToken map[string]map[string]interface{}, token string) (string, interface{}) {
	masked := maskCacheToken(token)
	lastClear := getOnlineAssetClearTime(token)
	if acc, ok := accountByToken[token]; ok {
		if s, ok := acc["token_masked"].(string); ok && strings.TrimSpace(s) != "" {
			masked = s
		}
		if v, ok := acc["last_asset_clear_at"]; ok && v != nil {
			lastClear = v
		}
	}
	return masked, lastClear
}

func listOnlineAccountTokens(onlineAccounts []map[string]interface{}) []string {
	out := make([]string, 0, len(onlineAccounts))
	for _, item := range onlineAccounts {
		token, _ := item["token"].(string)
		token = normalizeOnlineToken(token)
		if token == "" {
			continue
		}
		out = append(out, token)
	}
	return normalizeOnlineTokenList(out)
}

func detailErrorStatus(err error) string {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		msg = "request failed"
	}
	return "error: " + msg
}

func (h *Handler) fetchOnlineAssetDetails(
	r *http.Request,
	tokens []string,
	accountByToken map[string]map[string]interface{},
) ([]map[string]interface{}, int) {
	requestTokens := normalizeOnlineTokenList(tokens)
	if len(requestTokens) == 0 {
		return []map[string]interface{}{}, 0
	}

	details := make([]map[string]interface{}, len(requestTokens))
	if h == nil || h.client == nil || r == nil {
		for i, token := range requestTokens {
			masked, lastClear := onlineAccountInfo(accountByToken, token)
			details[i] = map[string]interface{}{
				"token":               token,
				"token_masked":        masked,
				"count":               0,
				"status":              "error: client not configured",
				"last_asset_clear_at": lastClear,
			}
		}
		return details, 0
	}

	type job struct {
		index int
		token string
	}
	workerCount := 4
	if len(requestTokens) < workerCount {
		workerCount = len(requestTokens)
	}

	var (
		totalCount int
		totalMu    sync.Mutex
	)
	jobs := make(chan job)
	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for item := range jobs {
			masked, lastClear := onlineAccountInfo(accountByToken, item.token)
			detail := map[string]interface{}{
				"token":               item.token,
				"token_masked":        masked,
				"count":               0,
				"status":              "not_loaded",
				"last_asset_clear_at": lastClear,
			}

			count, err := h.client.countAssets(r.Context(), item.token)
			if err != nil {
				detail["status"] = detailErrorStatus(err)
			} else {
				detail["count"] = count
				detail["status"] = "ok"
				totalMu.Lock()
				totalCount += count
				totalMu.Unlock()
			}
			details[item.index] = detail
		}
	}

	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go worker()
	}
	for i, token := range requestTokens {
		jobs <- job{index: i, token: token}
	}
	close(jobs)
	wg.Wait()
	return details, totalCount
}

func validCacheMediaType(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image", "video":
		return true
	default:
		return false
	}
}

func listCachedEntries(mediaType string) ([]cacheEntry, int64, error) {
	typ := strings.ToLower(strings.TrimSpace(mediaType))
	if !validCacheMediaType(typ) {
		return nil, 0, nil
	}
	dir := filepath.Join(cacheBaseDir, typ)
	items, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []cacheEntry{}, 0, nil
		}
		return nil, 0, err
	}

	out := make([]cacheEntry, 0, len(items))
	var totalSize int64
	for _, item := range items {
		if !item.Type().IsRegular() {
			continue
		}
		name := sanitizeCachedFilename(item.Name())
		if name == "" {
			continue
		}
		info, err := item.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		totalSize += info.Size()
		viewURL := "/v1/files/" + typ + "/" + name
		previewURL := ""
		if typ == "image" {
			previewURL = viewURL
		}
		out = append(out, cacheEntry{
			MediaType:  typ,
			Name:       name,
			Path:       typ + "/" + name,
			URL:        "/grok/v1/files/" + typ + "/" + name,
			ViewURL:    viewURL,
			PreviewURL: previewURL,
			Size:       info.Size(),
			SizeBytes:  info.Size(),
			UpdatedAt:  info.ModTime().UnixMilli(),
			MtimeMS:    info.ModTime().UnixMilli(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, totalSize, nil
}

func parseCacheDeleteTarget(req cacheDeleteItemRequest) (string, string, bool) {
	mediaType := strings.ToLower(strings.TrimSpace(req.MediaType))
	if mediaType == "" {
		mediaType = strings.ToLower(strings.TrimSpace(req.Type))
	}
	name := sanitizeCachedFilename(strings.TrimSpace(req.Name))
	if name == "" {
		name = sanitizeCachedFilename(strings.TrimSpace(req.FileName))
	}
	if validCacheMediaType(mediaType) && name != "" {
		return mediaType, name, true
	}

	rawPath := strings.TrimSpace(req.Path)
	if rawPath == "" {
		return "", "", false
	}
	if !strings.HasPrefix(rawPath, "/") {
		rawPath = "/grok/v1/files/" + strings.TrimLeft(rawPath, "/")
	}
	mt, fn, ok := parseFilesPath(rawPath)
	if !ok {
		return "", "", false
	}
	return mt, fn, true
}

func (h *Handler) HandleAdminCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	images, imageSize, err := listCachedEntries("image")
	if err != nil {
		http.Error(w, "failed to list cache", http.StatusInternalServerError)
		return
	}
	videos, videoSize, err := listCachedEntries("video")
	if err != nil {
		http.Error(w, "failed to list cache", http.StatusInternalServerError)
		return
	}

	onlineAccounts := h.listCacheOnlineAccounts(r)
	accountByToken := make(map[string]map[string]interface{}, len(onlineAccounts))
	for _, item := range onlineAccounts {
		token, _ := item["token"].(string)
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		accountByToken[token] = item
	}

	scope := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope")))
	selectedToken := normalizeOnlineToken(r.URL.Query().Get("token"))
	tokensParam := strings.TrimSpace(r.URL.Query().Get("tokens"))
	selectedTokens := make([]string, 0)
	if tokensParam != "" {
		selectedTokens = normalizeOnlineTokenList(strings.Split(tokensParam, ","))
	}

	onlineScope := "none"
	switch {
	case len(selectedTokens) > 0:
		onlineScope = "selected"
	case scope == "all":
		onlineScope = "all"
	case selectedToken != "":
		onlineScope = "single"
	}

	onlineStatus := "not_loaded"
	if len(onlineAccounts) == 0 {
		onlineStatus = "no_token"
	}

	online := map[string]interface{}{
		"count":               0,
		"status":              onlineStatus,
		"token":               nil,
		"token_masked":        nil,
		"last_asset_clear_at": nil,
	}
	onlineDetails := make([]map[string]interface{}, 0)
	switch onlineScope {
	case "single":
		details, _ := h.fetchOnlineAssetDetails(r, []string{selectedToken}, accountByToken)
		if len(details) > 0 {
			online["token"] = details[0]["token"]
			online["token_masked"] = details[0]["token_masked"]
			online["count"] = details[0]["count"]
			online["status"] = details[0]["status"]
			online["last_asset_clear_at"] = details[0]["last_asset_clear_at"]
		} else {
			online["token"] = selectedToken
			online["token_masked"] = maskCacheToken(selectedToken)
		}
	case "selected":
		details, total := h.fetchOnlineAssetDetails(r, selectedTokens, accountByToken)
		onlineDetails = details
		online["count"] = total
		if len(selectedTokens) > 0 {
			online["status"] = "ok"
		} else {
			online["status"] = "no_token"
		}
	case "all":
		allTokens := listOnlineAccountTokens(onlineAccounts)
		details, total := h.fetchOnlineAssetDetails(r, allTokens, accountByToken)
		onlineDetails = details
		online["count"] = total
		if len(allTokens) > 0 {
			online["status"] = "ok"
		} else {
			online["status"] = "no_token"
		}
	}

	imageStats := map[string]interface{}{
		"count":      len(images),
		"bytes":      imageSize,
		"size_bytes": imageSize,
		"size_mb":    bytesToMB(imageSize),
	}
	videoStats := map[string]interface{}{
		"count":      len(videos),
		"bytes":      videoSize,
		"size_bytes": videoSize,
		"size_mb":    bytesToMB(videoSize),
	}
	out := map[string]interface{}{
		"status":   "success",
		"base_dir": cacheBaseDir,
		"image":    imageStats,
		"video":    videoStats,
		"total": map[string]interface{}{
			"count": len(images) + len(videos),
			"bytes": imageSize + videoSize,
		},
		"local_image":     imageStats,
		"local_video":     videoStats,
		"online":          online,
		"online_accounts": onlineAccounts,
		"online_scope":    onlineScope,
		"online_details":  onlineDetails,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *Handler) HandleAdminCacheList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rawPage := strings.TrimSpace(r.URL.Query().Get("page"))
	rawPageSize := strings.TrimSpace(r.URL.Query().Get("page_size"))
	mediaType := resolveCacheMediaType(r.URL.Query())
	if mediaType == "" {
		mediaType = "image"
	}
	if !validCacheMediaType(mediaType) {
		http.Error(w, "invalid media_type", http.StatusBadRequest)
		return
	}
	entries, _, err := listCachedEntries(mediaType)
	if err != nil {
		http.Error(w, "failed to list cache", http.StatusInternalServerError)
		return
	}

	page := parsePositiveInt(rawPage, 1)
	pageSize := parsePositiveInt(rawPageSize, 1000)
	if rawPage == "" && rawPageSize == "" {
		page = 1
		pageSize = len(entries)
		if pageSize == 0 {
			pageSize = 1000
		}
	}
	paged, total := paginateCacheEntries(entries, page, pageSize)

	out := map[string]interface{}{
		"status":    "success",
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"items":     paged,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *Handler) HandleAdminCacheClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req cacheClearRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	mediaTypes := []string{"image"}
	targetType := strings.ToLower(strings.TrimSpace(req.MediaType))
	if targetType == "" {
		targetType = strings.ToLower(strings.TrimSpace(req.Type))
	}
	if targetType != "" {
		typ := targetType
		if !validCacheMediaType(typ) {
			http.Error(w, "invalid media_type", http.StatusBadRequest)
			return
		}
		mediaTypes = []string{typ}
	}

	removedFiles := 0
	removedBytes := int64(0)
	for _, typ := range mediaTypes {
		list, size, err := listCachedEntries(typ)
		if err != nil {
			http.Error(w, "failed to read cache", http.StatusInternalServerError)
			return
		}
		removedFiles += len(list)
		removedBytes += size
		dir := filepath.Join(cacheBaseDir, typ)
		if err := os.RemoveAll(dir); err != nil {
			http.Error(w, "failed to clear cache", http.StatusInternalServerError)
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			http.Error(w, "failed to recreate cache dir", http.StatusInternalServerError)
			return
		}
	}

	out := map[string]interface{}{
		"status":        "success",
		"removed_count": removedFiles,
		"removed_bytes": removedBytes,
		"result": map[string]interface{}{
			"count":      removedFiles,
			"size_mb":    bytesToMB(removedBytes),
			"size_bytes": removedBytes,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *Handler) HandleAdminCacheItemDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req cacheDeleteItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	mediaType, name, ok := parseCacheDeleteTarget(req)
	if !ok {
		http.Error(w, "invalid delete target", http.StatusBadRequest)
		return
	}

	full := filepath.Join(cacheBaseDir, mediaType, name)
	err := os.Remove(full)
	removed := true
	if err != nil {
		if os.IsNotExist(err) {
			removed = false
		} else {
			http.Error(w, "failed to delete cache item", http.StatusInternalServerError)
			return
		}
	}
	out := map[string]interface{}{
		"status":     "success",
		"removed":    removed,
		"media_type": mediaType,
		"name":       name,
		"result": map[string]interface{}{
			"deleted": removed,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *Handler) HandleAdminCacheOnlineClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h == nil || h.client == nil {
		http.Error(w, "grok client not configured", http.StatusServiceUnavailable)
		return
	}

	var req cacheOnlineClearRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	tokens, clearMode, errMsg := resolveCacheOnlineClearTargets(req, h.listCacheOnlineAccounts(r))
	if errMsg != "" {
		http.Error(w, errMsg, http.StatusBadRequest)
		return
	}

	results := map[string]map[string]interface{}{}
	totalAll := 0
	successAll := 0
	failedAll := 0
	for _, token := range tokens {
		total, success, failed, err := h.client.clearAssets(r.Context(), token)
		if err != nil {
			results[token] = map[string]interface{}{
				"status": "error",
				"error":  strings.TrimSpace(err.Error()),
			}
			continue
		}
		now := time.Now().UnixMilli()
		setOnlineAssetClearTime(token, now)

		totalAll += total
		successAll += success
		failedAll += failed
		results[token] = map[string]interface{}{
			"status": "success",
			"result": map[string]interface{}{
				"total":   total,
				"success": success,
				"failed":  failed,
			},
		}
	}

	if clearMode == "single" {
		single := results[tokens[0]]
		if status, _ := single["status"].(string); status == "success" {
			resp := map[string]interface{}{
				"status": "success",
				"result": single["result"],
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		errText, _ := single["error"].(string)
		errText = strings.TrimSpace(errText)
		if errText == "" {
			errText = "failed to clear cache"
		}
		resp := map[string]interface{}{
			"status": "error",
			"error":  errText,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	resp := map[string]interface{}{
		"status":  "success",
		"results": results,
		"result": map[string]interface{}{
			"total":   totalAll,
			"success": successAll,
			"failed":  failedAll,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func resolveCacheOnlineClearTargets(
	req cacheOnlineClearRequest,
	onlineAccounts []map[string]interface{},
) ([]string, string, string) {
	if req.Tokens != nil {
		tokens := normalizeOnlineTokenList(req.Tokens)
		if len(tokens) == 0 {
			return nil, "batch", "No tokens provided"
		}
		return tokens, "batch", ""
	}

	if token := normalizeOnlineToken(req.Token); token != "" {
		return []string{token}, "single", ""
	}

	allTokens := listOnlineAccountTokens(onlineAccounts)
	if len(allTokens) == 0 {
		return nil, "single", "No available token to perform cleanup"
	}
	return []string{allTokens[0]}, "single", ""
}

func (h *Handler) resolveCacheOnlineLoadTargets(
	r *http.Request,
	req cacheOnlineLoadAsyncRequest,
) ([]string, string, []map[string]interface{}) {
	onlineAccounts := h.listCacheOnlineAccounts(r)
	scope := strings.ToLower(strings.TrimSpace(req.Scope))

	tokens := normalizeOnlineTokenList(req.Tokens)
	if len(tokens) > 0 {
		return tokens, "selected", onlineAccounts
	}
	if single := normalizeOnlineToken(req.Token); single != "" {
		return []string{single}, "single", onlineAccounts
	}
	if scope == "all" {
		return listOnlineAccountTokens(onlineAccounts), "all", onlineAccounts
	}
	return []string{}, scope, onlineAccounts
}

func (h *Handler) HandleAdminCacheOnlineLoadAsync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h == nil || h.client == nil {
		http.Error(w, "grok client not configured", http.StatusServiceUnavailable)
		return
	}

	var req cacheOnlineLoadAsyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	tokens, scope, onlineAccounts := h.resolveCacheOnlineLoadTargets(r, req)
	if len(tokens) == 0 {
		http.Error(w, "no tokens provided", http.StatusBadRequest)
		return
	}

	accountByToken := make(map[string]map[string]interface{}, len(onlineAccounts))
	for _, item := range onlineAccounts {
		token, _ := item["token"].(string)
		token = normalizeOnlineToken(token)
		if token == "" {
			continue
		}
		accountByToken[token] = item
	}

	ctx, cancel := context.WithCancel(context.Background())
	task := newNSFWBatchTask(len(tokens), cancel)

	go func() {
		defer scheduleDeleteNSFWBatchTask(task.ID, nsfwBatchTaskTTL)

		type job struct {
			index int
			token string
		}
		details := make([]map[string]interface{}, len(tokens))
		jobs := make(chan job)
		workerCount := 4
		if len(tokens) < workerCount {
			workerCount = len(tokens)
		}

		var (
			totalCount int
			totalMu    sync.Mutex
			wg         sync.WaitGroup
		)
		worker := func() {
			defer wg.Done()
			for item := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				masked, lastClear := onlineAccountInfo(accountByToken, item.token)
				detail := map[string]interface{}{
					"token":               item.token,
					"token_masked":        masked,
					"count":               0,
					"status":              "not_loaded",
					"last_asset_clear_at": lastClear,
				}

				count, err := h.client.countAssets(ctx, item.token)
				if err != nil {
					detail["status"] = detailErrorStatus(err)
				} else {
					detail["count"] = count
					detail["status"] = "ok"
					totalMu.Lock()
					totalCount += count
					totalMu.Unlock()
				}
				details[item.index] = detail
				task.record(item.token, err == nil, detail)
			}
		}

		wg.Add(workerCount)
		for i := 0; i < workerCount; i++ {
			go worker()
		}

	sendLoop:
		for i, token := range tokens {
			select {
			case <-ctx.Done():
				break sendLoop
			case jobs <- job{index: i, token: token}:
			}
		}
		close(jobs)
		wg.Wait()

		if ctx.Err() != nil {
			task.finish("cancelled", "")
			return
		}

		images, imageSize, err := listCachedEntries("image")
		if err != nil {
			task.finish("error", "failed to list image cache")
			return
		}
		videos, videoSize, err := listCachedEntries("video")
		if err != nil {
			task.finish("error", "failed to list video cache")
			return
		}

		onlineStatus := "ok"
		if len(tokens) == 0 {
			onlineStatus = "no_token"
		}
		online := map[string]interface{}{
			"count":               totalCount,
			"status":              onlineStatus,
			"token":               nil,
			"token_masked":        nil,
			"last_asset_clear_at": nil,
		}
		if scope == "single" && len(details) > 0 {
			online["count"] = details[0]["count"]
			online["status"] = details[0]["status"]
			online["token"] = details[0]["token"]
			online["token_masked"] = details[0]["token_masked"]
			online["last_asset_clear_at"] = details[0]["last_asset_clear_at"]
		}

		result := map[string]interface{}{
			"local_image": map[string]interface{}{
				"count":      len(images),
				"bytes":      imageSize,
				"size_bytes": imageSize,
				"size_mb":    bytesToMB(imageSize),
			},
			"local_video": map[string]interface{}{
				"count":      len(videos),
				"bytes":      videoSize,
				"size_bytes": videoSize,
				"size_mb":    bytesToMB(videoSize),
			},
			"online":          online,
			"online_accounts": onlineAccounts,
			"online_scope":    scope,
			"online_details":  details,
		}
		task.setResult(result)
		task.finish("done", "")
	}()

	out := map[string]interface{}{
		"status":  "success",
		"task_id": task.ID,
		"total":   len(tokens),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *Handler) HandleAdminCacheOnlineClearAsync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h == nil || h.client == nil {
		http.Error(w, "grok client not configured", http.StatusServiceUnavailable)
		return
	}

	var req cacheOnlineClearRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	tokens := make([]string, 0, len(req.Tokens)+1)
	if strings.TrimSpace(req.Token) != "" {
		tokens = append(tokens, req.Token)
	}
	tokens = append(tokens, req.Tokens...)
	tokens = normalizeOnlineTokenList(tokens)
	if len(tokens) == 0 {
		http.Error(w, "no tokens provided", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	task := newNSFWBatchTask(len(tokens), cancel)

	go func() {
		defer scheduleDeleteNSFWBatchTask(task.ID, nsfwBatchTaskTTL)

		type job struct {
			token string
		}
		jobs := make(chan job)
		workerCount := 4
		if len(tokens) < workerCount {
			workerCount = len(tokens)
		}

		var (
			results   = map[string]map[string]interface{}{}
			okCount   int
			failCount int
			mu        sync.Mutex
			wg        sync.WaitGroup
		)
		worker := func() {
			defer wg.Done()
			for item := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}

				total, success, failed, err := h.client.clearAssets(ctx, item.token)
				ok := err == nil
				entry := map[string]interface{}{}
				if err != nil {
					entry["status"] = "error"
					entry["error"] = strings.TrimSpace(err.Error())
				} else {
					setOnlineAssetClearTime(item.token, time.Now().UnixMilli())
					entry["status"] = "success"
					entry["result"] = map[string]interface{}{
						"total":   total,
						"success": success,
						"failed":  failed,
					}
				}

				mu.Lock()
				results[item.token] = entry
				if ok {
					okCount++
				} else {
					failCount++
				}
				mu.Unlock()

				task.record(item.token, ok, entry)
			}
		}

		wg.Add(workerCount)
		for i := 0; i < workerCount; i++ {
			go worker()
		}

	sendLoop:
		for _, token := range tokens {
			select {
			case <-ctx.Done():
				break sendLoop
			case jobs <- job{token: token}:
			}
		}
		close(jobs)
		wg.Wait()

		if ctx.Err() != nil {
			task.finish("cancelled", "")
			return
		}

		result := map[string]interface{}{
			"status": "success",
			"summary": map[string]interface{}{
				"total": len(tokens),
				"ok":    okCount,
				"fail":  failCount,
			},
			"results": results,
		}
		task.setResult(result)
		task.finish("done", "")
	}()

	out := map[string]interface{}{
		"status":  "success",
		"task_id": task.ID,
		"total":   len(tokens),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
