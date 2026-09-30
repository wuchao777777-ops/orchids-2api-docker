package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchids-api/internal/buildinfo"
)

type fixtureSource struct {
	release Release
	files   map[string][]byte
	err     error
	calls   int
}

func (f *fixtureSource) Latest(context.Context) (Release, error) { f.calls++; return f.release, f.err }
func (f *fixtureSource) Download(_ context.Context, a Asset, w io.Writer, limit int64) error {
	b, ok := f.files[a.Name]
	if !ok {
		return errors.New("missing asset")
	}
	if int64(len(b)) > limit {
		return errors.New("too large")
	}
	_, e := w.Write(b)
	return e
}
func fixture(t *testing.T, binary []byte) *Manager {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "server")
	if err := os.WriteFile(exe, []byte("old binary"), 0755); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(binary)
	name := "orchids-server-linux-amd64"
	f := &fixtureSource{release: Release{Tag: "v1.0.3", Assets: []Asset{{Name: name, URL: "binary"}, {Name: name + ".sha256", URL: "checksum"}, {Name: name + ".build-info.txt", URL: "metadata"}}}, files: map[string][]byte{name: binary, name + ".sha256": []byte(hex.EncodeToString(h[:]) + "  " + name), name + ".build-info.txt": []byte("version=v1.0.3\ncommit=abcdef0\ngoos=linux\ngoarch=amd64\n")}}
	return &Manager{Info: buildinfo.Info{Version: "v1.0.2", OS: "linux", Arch: "amd64", Commit: "oldcommit"}, Dir: dir, Executable: exe, Source: f, launch: func(string, string, string, string, string, string) error { return nil }, rename: os.Rename}
}
func TestSemVer(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want int
	}{{"v1.0.3", "1.0.2", 1}, {"1.0.2+local", "v1.0.2", 0}, {"1.0.0-alpha.2", "1.0.0-alpha.10", -1}, {"1.0.0-beta", "1.0.0", -1}, {"1.0.0-2", "1.0.0-alpha", -1}, {"1.0.0-alpha", "1.0.0-alpha.1", -1}, {"999999999999999999999.0.0", "2.0.0", 1}} {
		if c, ok := Compare(tt.a, tt.b); !ok || c != tt.want {
			t.Fatalf("Compare(%s,%s)=%d/%t", tt.a, tt.b, c, ok)
		}
	}
	for _, v := range []string{"dev", "v01.0.0", "1.0", "1.0.0-01", "1.0.0+"} {
		if _, ok := Compare(v, "1.0.0"); ok {
			t.Fatalf("accepted %s", v)
		}
	}
}
func TestCheckCacheAndUnknown(t *testing.T) {
	m := fixture(t, nil)
	f := m.Source.(*fixtureSource)
	a := m.Check(context.Background(), false)
	if !a.Available || !a.HasUpdate {
		t.Fatalf("%+v", a)
	}
	m.Check(context.Background(), false)
	if f.calls != 1 {
		t.Fatal("cache bypassed")
	}
	f.err = errors.New("offline")
	a = m.Check(context.Background(), true)
	if a.Warning == "" || !a.Available {
		t.Fatal("valid stale cache not identified")
	}
	m.checkedAt = time.Now().Add(-21 * time.Minute)
	a = m.Check(context.Background(), true)
	if a.Available || a.Warning == "" {
		t.Fatal("failed check presented as latest")
	}
	f.err = nil
	m.Info.Version = "dev"
	a = m.Check(context.Background(), true)
	if a.Warning == "" || a.HasUpdate {
		t.Fatal("unknown source version guessed")
	}
}
func TestAssetAndChecksumContracts(t *testing.T) {
	if _, _, e := selectAssets(Release{}, "linux", "amd64"); e == nil {
		t.Fatal("missing checksum accepted")
	}
	for _, value := range []string{"bad", "abcd  orchids-server-linux-amd64", ""} {
		if _, e := checksumDigest(value, "orchids-server-linux-amd64"); e == nil {
			t.Fatal("invalid checksum accepted")
		}
	}
	if _, e := checksumDigest(strings.Repeat("a", 64)+" *orchids-server-linux-amd64", "orchids-server-linux-amd64"); e != nil {
		t.Fatal(e)
	}
}
func TestIntegrityFailurePreservesExecutable(t *testing.T) {
	m := fixture(t, []byte("bad new binary"))
	f := m.Source.(*fixtureSource)
	f.files["orchids-server-linux-amd64.sha256"] = []byte(strings.Repeat("0", 64) + "  orchids-server-linux-amd64")
	op := Operation{ID: "test", Target: buildinfo.Info{Version: "v1.0.3"}}
	if e := m.perform(context.Background(), &op); e == nil || !strings.Contains(e.Error(), "SHA-256") {
		t.Fatalf("%v", e)
	}
	b, _ := os.ReadFile(m.Executable)
	if string(b) != "old binary" {
		t.Fatal("old binary modified before verification")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTrustedDownloadsAndLimits(t *testing.T) {
	for _, raw := range []string{"http://github.com/file", "https://evil.example/file", "https://github.com:444/file", "https://user@github.com/file"} {
		if trustedURL(raw) {
			t.Fatalf("trusted %s", raw)
		}
	}
	g := NewGitHub("zhangdailin/API-Console")
	g.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("12345")), Header: make(http.Header), ContentLength: -1, Request: r}, nil
	})
	var output bytes.Buffer
	asset := Asset{URL: "https://github.com/zhangdailin/API-Console/releases/download/v1.0.3/server"}
	if e := g.Download(context.Background(), asset, &output, 4); e == nil {
		t.Fatal("stream size limit bypassed")
	}
	asset.URL = "https://github.com/other/repo/releases/download/v1.0.3/server"
	if e := g.Download(context.Background(), asset, &output, 10); e == nil {
		t.Fatal("other repository accepted")
	}
	if e := g.Client.CheckRedirect(&http.Request{URL: mustURL(t, "https://evil.example/payload")}, nil); e == nil {
		t.Fatal("untrusted redirect accepted")
	}
}
func TestActionHTTPGuards(t *testing.T) {
	m := fixture(t, nil)
	m.Reason = "unsupported"
	for _, tt := range []struct {
		method, origin, content string
		want                    int
	}{{"GET", "", "", 405}, {"POST", "https://evil.example", "application/json", 403}, {"POST", "", "text/plain", 415}, {"POST", "", "application/json", 409}} {
		r := httptest.NewRequest(tt.method, "https://console.example/api/system/update", strings.NewReader(`{"version":"v1.0.3"}`))
		r.Header.Set("Origin", tt.origin)
		r.Header.Set("Content-Type", tt.content)
		w := httptest.NewRecorder()
		m.HandleAction("update")(w, r)
		if w.Code != tt.want {
			t.Fatalf("got %d want %d", w.Code, tt.want)
		}
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, e := url.Parse(raw)
	if e != nil {
		t.Fatal(e)
	}
	return u
}
