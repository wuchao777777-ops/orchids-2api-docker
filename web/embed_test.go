package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
)

// TestAssetVersionIsContentDerived pins the property that replaced the
// hand-maintained ?v= strings: the version must come from the embedded bytes,
// so a deployment that changes a script also changes the URL it is fetched
// under. When the version was a literal, removing a provider from the UI left
// every browser on the cached copy of the previous one.
func TestAssetVersionIsContentDerived(t *testing.T) {
	// Compute the expected version independently from the embedded assets, not
	// from AssetVersion's cached value. WalkDir visits names in lexical order;
	// each asset contributes its path, byte length and content to the digest.
	sum := sha256.New()
	assetCounts := map[string]int{}
	err := fs.WalkDir(staticFS, "static", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		ext := strings.ToLower(path.Ext(name))
		if ext != ".js" && ext != ".css" {
			return nil
		}
		data, err := staticFS.ReadFile(name)
		if err != nil {
			return err
		}
		assetCounts[ext]++
		fmt.Fprintf(sum, "%s\x00%d\x00", name, len(data))
		_, _ = sum.Write(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded assets: %v", err)
	}
	if assetCounts[".js"] == 0 || assetCounts[".css"] == 0 {
		t.Fatalf("expected embedded JavaScript and CSS assets, got %v", assetCounts)
	}
	want := hex.EncodeToString(sum.Sum(nil))[:12]
	version := AssetVersion()
	if version != want {
		t.Fatalf("AssetVersion() = %q, want embedded-content SHA-256 prefix %q", version, want)
	}
	if len(version) != 12 {
		t.Fatalf("AssetVersion() = %q, want a 12-character hash", version)
	}
	if again := AssetVersion(); again != version {
		t.Fatalf("AssetVersion() is not stable: %q then %q", version, again)
	}
	if version == assetVersionPlaceholder {
		t.Fatalf("AssetVersion() returned the placeholder %q", version)
	}
}

// TestLoginPageResolvesAssetVersion covers the static login page, which is the
// one asset URL that cannot read PageData.
func TestLoginPageResolvesAssetVersion(t *testing.T) {
	page, err := LoginPage()
	if err != nil {
		t.Fatalf("LoginPage() error = %v", err)
	}
	if bytes.Contains(page, []byte(assetVersionPlaceholder)) {
		t.Fatalf("LoginPage() still carries %q", assetVersionPlaceholder)
	}
	want := []byte("main.css?v=" + AssetVersion())
	if !bytes.Contains(page, want) {
		t.Fatalf("LoginPage() does not link %s", want)
	}
	if !strings.Contains(string(page), "<title>") {
		t.Fatal("LoginPage() does not look like the login page")
	}
}

func TestStaticHandlerCachePolicy(t *testing.T) {
	handler := StaticHandler()
	tests := []struct {
		path string
		want string
	}{
		{"/css/main.css?v=release", "public, max-age=31536000, immutable"},
		{"/js/common.js?v=release", "public, max-age=31536000, immutable"},
		{"/css/main.css", "no-cache"},
		{"/login.html", "no-cache"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			handler.ServeHTTP(recorder, request)
			if got := recorder.Header().Get("Cache-Control"); got != tt.want {
				t.Fatalf("Cache-Control = %q, want %q", got, tt.want)
			}
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
		})
	}
}
