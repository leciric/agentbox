package hostvm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/paths"
)

type tarEntry struct {
	name, body, link string
	dir              bool
	mode             int64
}

func makeTarball(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch {
		case e.dir:
			hdr.Typeflag, hdr.Size = tar.TypeDir, 0
		case e.link != "":
			hdr.Typeflag, hdr.Linkname, hdr.Size = tar.TypeSymlink, e.link, 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Lima's tarball, in little: entries under ./, a symlink inside it.
var limaTarball = []tarEntry{
	{name: "./", dir: true, mode: 0o755},
	{name: "./bin/", dir: true, mode: 0o755},
	{name: "./bin/limactl", body: "#!/bin/sh\necho limactl\n", mode: 0o755},
	{name: "./libexec/lima/lima-driver-krunkit", body: "driver", mode: 0o755},
	{name: "./share/lima/templates/default.yaml", body: "images: []\n", mode: 0o644},
	{name: "./share/doc/lima/templates", link: "../../lima/templates"},
}

// noLimaHere hides every Lima FindLimactl would find, and gives AgentBox a
// data directory of its own.
func noLimaHere(t *testing.T) paths.Paths {
	t.Helper()
	t.Setenv("AGENTBOX_LIMACTL", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, p := range []string{"/opt/homebrew/bin/limactl", "/usr/local/bin/limactl"} {
		if _, err := os.Stat(p); err == nil {
			t.Skipf("%s is installed here", p)
		}
	}
	p, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func serveLima(t *testing.T, body []byte) (url, sum string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	h := sha256.Sum256(body)
	return srv.URL + "/lima.tar.gz", hex.EncodeToString(h[:])
}

func TestEnsureLimaDownloadsAndUnpacks(t *testing.T) {
	p := noLimaHere(t)
	if _, err := FindLimactl(); err == nil || !strings.Contains(err.Error(), "agentbox vm init downloads it") {
		t.Fatalf("FindLimactl with no Lima = %v, want it to say init downloads it", err)
	}
	url, sum := serveLima(t, makeTarball(t, limaTarball))
	old := limaDownload
	limaDownload = func() (string, string) { return url, sum }
	t.Cleanup(func() { limaDownload = old })

	var log bytes.Buffer
	limactl, err := EnsureLima(t.Context(), p, &log)
	if err != nil {
		t.Fatal(err)
	}
	if limactl != ownLimactl(p) {
		t.Errorf("EnsureLima = %s, want %s", limactl, ownLimactl(p))
	}
	fi, err := os.Stat(limactl)
	if err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("limactl = %v, %v; want an executable", fi, err)
	}
	if b, err := os.ReadFile(filepath.Join(ownLimaDir(p), "share", "doc", "lima", "templates", "default.yaml")); err != nil || string(b) != "images: []\n" {
		t.Errorf("the templates through the symlink = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(ownLimaDir(p), "libexec", "lima", "lima-driver-krunkit")); err != nil {
		t.Errorf("krunkit's driver: %v", err)
	}
	if !strings.Contains(log.String(), "Downloading Lima "+limaRelease.version) {
		t.Errorf("log = %q", log.String())
	}
	// Found from now on, and not downloaded again.
	if got, err := FindLimactl(); err != nil || got != limactl {
		t.Errorf("FindLimactl = %s, %v; want %s", got, err, limactl)
	}
	limaDownload = func() (string, string) { t.Fatal("downloaded again"); return "", "" }
	if got, err := EnsureLima(t.Context(), p, io.Discard); err != nil || got != limactl {
		t.Errorf("EnsureLima again = %s, %v", got, err)
	}
	// Nothing left beside it.
	if entries, _ := os.ReadDir(filepath.Dir(ownLimaDir(p))); len(entries) != 1 {
		t.Errorf("lima/ has %d entries, want only %s", len(entries), limaRelease.version)
	}
}

func TestEnsureLimaRefusesAnotherTarball(t *testing.T) {
	p := noLimaHere(t)
	url, _ := serveLima(t, makeTarball(t, limaTarball))
	old := limaDownload
	limaDownload = func() (string, string) { return url, strings.Repeat("0", 64) }
	t.Cleanup(func() { limaDownload = old })
	_, err := EnsureLima(t.Context(), p, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not the "+strings.Repeat("0", 64)) {
		t.Fatalf("EnsureLima with the wrong sum = %v", err)
	}
	if _, err := os.Stat(ownLimaDir(p)); !os.IsNotExist(err) {
		t.Errorf("a Lima that failed its check was left in %s", ownLimaDir(p))
	}
}

func TestUntarStaysInside(t *testing.T) {
	for name, entries := range map[string][]tarEntry{
		"dot dot":       {{name: "../evil", body: "x", mode: 0o644}},
		"deep dot dot":  {{name: "bin/../../evil", body: "x", mode: 0o644}},
		"absolute":      {{name: "/etc/evil", body: "x", mode: 0o644}},
		"link out":      {{name: "share/x", link: "../../../etc"}},
		"absolute link": {{name: "share/x", link: "/etc"}},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			tarball := filepath.Join(dir, "x.tar.gz")
			if err := os.WriteFile(tarball, makeTarball(t, entries), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := untar(tarball, filepath.Join(dir, "out")); err == nil {
				t.Errorf("untar took %v", entries)
			}
		})
	}
}

func TestLimaPins(t *testing.T) {
	for arch, want := range map[string]string{
		"arm64": "https://github.com/lima-vm/lima/releases/download/v" + limaRelease.version + "/lima-" + limaRelease.version + "-Darwin-arm64.tar.gz",
		"amd64": "https://github.com/lima-vm/lima/releases/download/v" + limaRelease.version + "/lima-" + limaRelease.version + "-Darwin-x86_64.tar.gz",
	} {
		if got := limaURL(arch); got != want {
			t.Errorf("limaURL(%s) = %s, want %s", arch, got, want)
		}
		if len(limaRelease.sha256[arch]) != 64 {
			t.Errorf("no SHA256 pinned for %s", arch)
		}
	}
}
