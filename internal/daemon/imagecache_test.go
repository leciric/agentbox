package daemon

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/state"
)

// The shared image cache is on until it's turned off, with its default cap,
// and settings show how much it holds.
func TestImageCacheSettings(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	out, err := d.srv.currentSettings(httptest.NewRequest(http.MethodGet, "/v1/settings", nil))
	if err != nil {
		t.Fatal(err)
	}
	if !out.ImageCache || out.ImageCacheMaxBytes != out.DefaultImageCacheMaxBytes || out.DefaultImageCacheMaxBytes > state.DefaultImageCacheMax || out.ImageCacheBytes != 0 {
		t.Errorf("a fresh installation's cache: %+v", out)
	}

	blob := filepath.Join(d.srv.cfg.Paths.ImageCache(), "blobs", "sha256", strings.Repeat("a", 64))
	if err := os.MkdirAll(filepath.Dir(blob), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, make([]byte, 1234), 0o600); err != nil {
		t.Fatal(err)
	}
	// Measured afresh: what a daemon starting on a cache from before finds.
	d.srv.imageCache = d.srv.newImageCache()
	// The smallest cap: a cap is fitted to the disk t.TempDir() is on, which
	// in an agent is /t, 2 GiB.
	if out, err := patchSettings(t, d, `{"imageCacheMaxBytes":1073741824}`); err != nil || out.ImageCacheMaxBytes != 1<<30 || out.ImageCacheBytes != 1234 {
		t.Errorf("after setting a 1 GiB cap: %d of %d, %v", out.ImageCacheBytes, out.ImageCacheMaxBytes, err)
	}
	if _, err := patchSettings(t, d, `{"imageCacheMaxBytes":1000}`); err == nil {
		t.Error("a cap under 1 GiB was taken")
	}
	if out, err := patchSettings(t, d, `{"imageCacheMaxBytes":0}`); err != nil || out.ImageCacheMaxBytes != out.DefaultImageCacheMaxBytes {
		t.Errorf("0 didn't go back to the default: %d, %v", out.ImageCacheMaxBytes, err)
	}
	if out, err := patchSettings(t, d, `{"clearImageCache":true}`); err != nil || out.ImageCacheBytes != 0 {
		t.Errorf("after clearing: %d bytes, %v", out.ImageCacheBytes, err)
	}
	if _, err := os.Stat(blob); err == nil {
		t.Error("clearing left the blob")
	}
	if out, err := patchSettings(t, d, `{"imageCache":false}`); err != nil || out.ImageCache {
		t.Errorf("after turning it off: %t, %v", out.ImageCache, err)
	}
}

// Agents are pointed at the cache only while it's on and its socket is served.
func TestImageCacheSocketIsEmptyWhileOffOrDown(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	d.srv.imageCacheUp.Store(true)
	if got := d.srv.imageCacheSocket(ctx); got != d.srv.cfg.Paths.ImageCacheSocket() {
		t.Errorf("on and up: %q", got)
	}
	d.srv.imageCacheUp.Store(false)
	if got := d.srv.imageCacheSocket(ctx); got != "" {
		t.Errorf("down: %q, want none", got)
	}
	d.srv.imageCacheUp.Store(true)
	if err := d.srv.store.SetFlag(ctx, state.SettingImageCache, false); err != nil {
		t.Fatal(err)
	}
	if got := d.srv.imageCacheSocket(ctx); got != "" {
		t.Errorf("off: %q, want none", got)
	}
}

// The daemon serves the cache on its socket, which is what agents' proxy
// devices connect to.
func TestTheDaemonServesTheImageCache(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	if !d.srv.imageCacheUp.Load() {
		t.Fatal("the image cache isn't served")
	}
	client := http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", d.srv.cfg.Paths.ImageCacheSocket())
	}}}
	resp, err := client.Get("http://cache/v2/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Docker-Distribution-API-Version") != "registry/2.0" {
		t.Errorf("/v2/ = %d", resp.StatusCode)
	}
}
