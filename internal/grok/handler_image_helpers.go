package grok

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)



func normalizeImageResponseFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "b64_json", "base64":
		return "b64_json"
	case "url", "":
		return "url"
	default:
		return "url"
	}
}

func imageResponseField(format string) string {
	if normalizeImageResponseFormat(format) == "b64_json" {
		return "b64_json"
	}
	return "url"
}

func imageUsagePayload() map[string]interface{} {
	return map[string]interface{}{
		"total_tokens":  0,
		"input_tokens":  0,
		"output_tokens": 0,
		"input_tokens_details": map[string]interface{}{
			"text_tokens":  0,
			"image_tokens": 0,
		},
	}
}

func mediaExtFromMime(mediaType, mimeType, rawURL string) string {
	m := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	switch m {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	}
	trim := strings.TrimSpace(rawURL)
	if idx := strings.Index(trim, "?"); idx >= 0 {
		trim = trim[:idx]
	}
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(trim)))
	if ext != "" && len(ext) <= 10 {
		return ext
	}
	if strings.EqualFold(mediaType, "video") {
		return ".mp4"
	}
	return ".jpg"
}

func imageDimsFromBytes(data []byte) (int, int) {
	if len(data) == 0 {
		return 0, 0
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func (h *Handler) cacheMediaURL(ctx context.Context, token, rawURL, mediaType string) (string, error) {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "video" {
		mediaType = "image"
	}
	trimURL := strings.TrimSpace(rawURL)
	lurl := strings.ToLower(trimURL)
	// Never cache known low-res thumbnail hosts; they lead to blurry results.
	if mediaType == "image" && strings.Contains(lurl, "encrypted-tbn0.gstatic.com") {
		return "", fmt.Errorf("skip thumbnail url")
	}
	// If the client can't reach assets.grok.com (common in some regions), caching through this server
	// is required for images to display at all.
	forceCache := mediaType == "image" && strings.Contains(lurl, "assets.grok.com/")

	data, mimeType, err := h.client.downloadAsset(ctx, token, rawURL)
	if err != nil {
		return "", err
	}
	// Heuristic: avoid caching tiny/low-res images (often thumbnails/previews).
	if mediaType == "image" {
		w, hgt := imageDimsFromBytes(data)
		// For assets.grok.com, caching is required for display (clients may not reach grok CDN).
		if forceCache {
			// Always cache (even previews). We already avoid emitting -part-0 when full exists.
		} else {
			if (w > 0 && hgt > 0 && (w < 900 || hgt < 900)) || len(data) < 60*1024 {
				slog.Debug("skip caching low-res image", "url", trimURL, "bytes", len(data), "w", w, "h", hgt)
				return "", fmt.Errorf("skip low-res image")
			}
		}
	}
	return h.cacheMediaBytes(rawURL, mediaType, data, mimeType)
}

func (h *Handler) imageOutputValue(ctx context.Context, token, url, format string) (string, error) {
	if normalizeImageResponseFormat(format) == "url" {
		trim := strings.TrimSpace(url)
		// Stable contract: prefer full over -part-0. If we only got a preview URL,
		// try the full variant first.
		if strings.Contains(trim, "-part-0/") {
			full := strings.ReplaceAll(trim, "-part-0/", "/")
			if name, err := h.cacheMediaURL(ctx, token, full, "image"); err == nil && name != "" {
				return "/grok/v1/files/image/" + name, nil
			}
		}
		if name, err := h.cacheMediaURL(ctx, token, trim, "image"); err == nil && name != "" {
			return "/grok/v1/files/image/" + name, nil
		}
		return trim, nil
	}
	raw, _, err := h.client.downloadAsset(ctx, token, url)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func (h *Handler) cacheMediaBytes(rawURL, mediaType string, data []byte, mimeType string) (string, error) {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "video" {
		mediaType = "image"
	}
	if len(data) == 0 {
		return "", fmt.Errorf("empty media data")
	}

	dir := filepath.Join(cacheBaseDir, mediaType)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	sum := sha1.Sum([]byte(strings.TrimSpace(rawURL)))
	name := hex.EncodeToString(sum[:]) + mediaExtFromMime(mediaType, mimeType, rawURL)
	fullPath := filepath.Join(dir, name)

	if info, statErr := os.Stat(fullPath); statErr == nil && info.Mode().IsRegular() && info.Size() > 0 {
		return name, nil
	}

	tmp := fullPath + ".tmp-" + randomHex(4)
	if writeErr := os.WriteFile(tmp, data, 0o644); writeErr != nil {
		_ = os.Remove(tmp)
		return "", writeErr
	}
	if renameErr := os.Rename(tmp, fullPath); renameErr != nil {
		_ = os.Remove(tmp)
		return "", renameErr
	}
	return name, nil
}
