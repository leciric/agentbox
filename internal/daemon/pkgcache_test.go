package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"agentbox/internal/pkgcache"
	"agentbox/internal/state"
)

// The shared package caches are on until they're turned off, with their
// default cap, and settings show how much they hold.
func TestPackageCacheSettings(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	out, err := d.srv.currentSettings(httptest.NewRequest(http.MethodGet, "/v1/settings", nil))
	if err != nil {
		t.Fatal(err)
	}
	if !out.PackageCache || out.PackageCacheMaxBytes != state.DefaultPackageCacheMax || out.DefaultPackageCacheMaxBytes != state.DefaultPackageCacheMax {
		t.Errorf("a fresh installation's caches: %+v", out)
	}

	file := filepath.Join(d.srv.cfg.Paths.PackageCache(), pkgcache.Npm, "_cacache", "content-v2", "x")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, make([]byte, 8192), 0o644); err != nil {
		t.Fatal(err)
	}
	d.srv.packageCache = d.srv.newPackageCache()
	if out, err := patchSettings(t, d, `{"packageCacheMaxBytes":2147483648}`); err != nil || out.PackageCacheMaxBytes != 2<<30 || out.PackageCacheBytes < 8192 {
		t.Errorf("after setting a 2 GiB cap: %d of %d, %v", out.PackageCacheBytes, out.PackageCacheMaxBytes, err)
	}
	if _, err := patchSettings(t, d, `{"packageCacheMaxBytes":1000}`); err == nil {
		t.Error("a cap under 1 GiB was taken")
	}
	if out, err := patchSettings(t, d, `{"packageCacheMaxBytes":0}`); err != nil || out.PackageCacheMaxBytes != state.DefaultPackageCacheMax {
		t.Errorf("0 didn't go back to the default: %d, %v", out.PackageCacheMaxBytes, err)
	}
	if out, err := patchSettings(t, d, `{"clearPackageCache":true}`); err != nil || out.PackageCacheBytes != 0 {
		t.Errorf("after clearing: %d bytes, %v", out.PackageCacheBytes, err)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("clearing left the file")
	}
	if out, err := patchSettings(t, d, `{"packageCache":false}`); err != nil || out.PackageCache {
		t.Errorf("after turning it off: %t, %v", out.PackageCache, err)
	}
}

// Agents mount the caches only while they're on, and find every tool's
// directory made.
func TestPackageCacheDirIsEmptyWhileOff(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	dir := d.srv.packageCacheDir(ctx)
	if dir != d.srv.cfg.Paths.PackageCache() {
		t.Fatalf("on: %q", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, pkgcache.GoMod)); err != nil {
		t.Errorf("go/mod wasn't made: %v", err)
	}
	if err := d.srv.store.SetFlag(ctx, state.SettingPackageCache, false); err != nil {
		t.Fatal(err)
	}
	if dir := d.srv.packageCacheDir(ctx); dir != "" {
		t.Errorf("off: %q", dir)
	}
}
