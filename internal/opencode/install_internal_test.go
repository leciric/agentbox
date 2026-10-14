package opencode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEveryPinnedReleaseHasItsChecksums(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		asset, err := Installer{Arch: arch}.asset()
		if err != nil {
			t.Fatal(err)
		}
		if len(sums[Version+"/"+asset]) != 64 {
			t.Errorf("tools.txt pins opencode %s, but install.go has no sha256 for its %s", Version, asset)
		}
	}
}

func tarball(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The machine the daemon runs on has no OpenCode, so `agentbox auth opencode`
// and `opencode models` used to fail there: Ensure is what gives them one.
func TestEnsureInstallsThePinnedReleaseWithNoOpenCodeOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	good := tarball(t, "opencode", []byte("#!/bin/sh\necho "+Version+"\n"))
	sum := sha256.Sum256(good)
	key := Version + "/opencode-linux-x64.tar.gz"
	was := sums[key]
	t.Cleanup(func() { sums[key] = was })
	sums[key] = hex.EncodeToString(sum[:])

	body := []byte("something else")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v"+Version+"/opencode-linux-x64.tar.gz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	inst := Installer{Dir: t.TempDir(), Releases: srv.URL, Arch: "amd64"}

	if _, err := inst.Ensure(context.Background(), func(string) {}); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a download that doesn't match: %v", err)
	}
	if _, err := os.Stat(inst.Path()); err == nil {
		t.Fatal("a download that didn't match was installed")
	}

	body = good
	var said []string
	path, err := inst.Ensure(context.Background(), func(s string) { said = append(said, s) })
	if err != nil {
		t.Fatal(err)
	}
	if path != inst.Path() || len(said) == 0 {
		t.Fatalf("path %s, status %q", path, said)
	}
	if st, err := os.Stat(path); err != nil || st.Mode()&0o111 == 0 {
		t.Fatalf("installed %v, %v", st, err)
	}
	// Installed, it isn't downloaded again.
	srv.Close()
	if _, err := inst.Ensure(context.Background(), func(string) {}); err != nil {
		t.Fatal(err)
	}
}

// Offline, an opencode the user installed themselves still does.
func TestEnsureFallsBackToPathWhenTheDownloadFails(t *testing.T) {
	bin := t.TempDir()
	own := filepath.Join(bin, "opencode")
	if err := os.WriteFile(own, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	got, err := Installer{Dir: t.TempDir(), Releases: srv.URL, Arch: "amd64"}.Ensure(context.Background(), func(string) {})
	if err != nil || got != own {
		t.Fatalf("Ensure() = %q, %v; want %s", got, err, own)
	}

	t.Setenv("PATH", t.TempDir())
	if _, err := (Installer{Dir: t.TempDir(), Releases: srv.URL, Arch: "amd64"}).Ensure(context.Background(), func(string) {}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("with neither: %v", err)
	}
}
