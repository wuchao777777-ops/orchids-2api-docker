package grok

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func sanitizeCachedFilename(raw string) string {
	name := strings.TrimSpace(raw)
	name = strings.ReplaceAll(name, "\\", "-")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "..") {
		return ""
	}
	return name
}

func parseFilesPath(rawPath string) (mediaType string, fileName string, ok bool) {
	prefix := ""
	switch {
	case strings.HasPrefix(rawPath, "/grok/v1/files/"):
		prefix = "/grok/v1/files/"
	case strings.HasPrefix(rawPath, "/v1/files/"):
		prefix = "/v1/files/"
	default:
		return "", "", false
	}
	path := strings.TrimPrefix(rawPath, prefix)
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", false
	}
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	mediaType = strings.ToLower(strings.TrimSpace(parts[0]))
	if mediaType != "image" && mediaType != "video" {
		return "", "", false
	}
	fileName = sanitizeCachedFilename(parts[1])
	if fileName == "" {
		return "", "", false
	}
	return mediaType, fileName, true
}

func (h *Handler) HandleFiles(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	mediaType, fileName, ok := parseFilesPath(r.URL.Path)
	if !ok {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	fullPath := filepath.Join(cacheBaseDir, mediaType, fileName)
	info, err := os.Stat(fullPath)
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	ctype := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName)))
	if ctype == "" {
		if mediaType == "video" {
			ctype = "video/mp4"
		} else {
			ctype = "image/jpeg"
		}
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, fullPath)
}
