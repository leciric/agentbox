package hostvm

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"agentbox/internal/paths"
)

// A Mac without Lima gets AgentBox's own: `agentbox vm init` downloads Lima's
// release for this Mac from Lima's GitHub releases, checks it against the
// SHA256 pinned here, and unpacks it under ~/.local/share/agentbox/lima/<version>,
// so nobody needs Homebrew for it. A Lima already on the Mac (on PATH, or
// Homebrew's) is still used first, as before: an existing VM keeps the Lima it
// was made with.
//
// Downloaded rather than bundled in the app: the release tarball is 38 MB for
// each of two architectures, which every download and update of the app would
// carry for a Mac that has Lima already; a nested limactl in the app would be
// signed and notarized with it, needing the virtualization entitlement on the
// app's signature too; and it moves on with a pin here rather than an app
// release. Lima's own release is what Lima's install instructions give
// besides Homebrew: limactl comes ad-hoc signed with the virtualization
// entitlement it needs, and a file a program downloads isn't quarantined, so
// Gatekeeper doesn't stop it. The tarball has lima-driver-krunkit in it too,
// so krunkit (still Homebrew's) is all a Mac needs to give memory back.
var limaRelease = struct {
	version string
	sha256  map[string]string // the tarball's, by GOARCH
}{
	version: "2.2.1",
	sha256: map[string]string{
		"arm64": "9e9eacce88f37e185c346bad73aa6136f738d8cdf8c3bb23cd42b071824bc66e",
		"amd64": "6b776e4bc41f358e6ee01fefcc78e2eb26f11635a0fcff632234af77a566c6c4",
	},
}

// limaURL is Lima's release tarball for a Mac of arch.
func limaURL(arch string) string {
	name := map[string]string{"arm64": "arm64", "amd64": "x86_64"}[arch]
	return fmt.Sprintf("https://github.com/lima-vm/lima/releases/download/v%s/lima-%s-Darwin-%s.tar.gz", limaRelease.version, limaRelease.version, name)
}

// ownLimaDir is where AgentBox's own Lima goes.
func ownLimaDir(p paths.Paths) string {
	return filepath.Join(p.Data, "lima", limaRelease.version)
}

// ownLimactl is AgentBox's own limactl, there or not.
func ownLimactl(p paths.Paths) string {
	return filepath.Join(ownLimaDir(p), "bin", "limactl")
}

// errNoLima is FindLimactl's when there's no Lima.
var errNoLima = errors.New("AgentBox runs in a Linux VM made with Lima, which isn't on this Mac yet: agentbox vm init downloads it (Lima " + limaRelease.version + ")")

// limaDownload is the URL EnsureLima fetches, and its SHA256: a variable for
// tests.
var limaDownload = func() (url, sum string) {
	return limaURL(runtime.GOARCH), limaRelease.sha256[runtime.GOARCH]
}

// EnsureLima is a limactl to use: the one FindLimactl finds, or, when it finds
// none, AgentBox's own Lima, downloaded, checked and unpacked first.
func EnsureLima(ctx context.Context, p paths.Paths, log io.Writer) (string, error) {
	if limactl, err := FindLimactl(); err == nil {
		return limactl, nil
	}
	url, sum := limaDownload()
	if sum == "" {
		return "", fmt.Errorf("no Lima for %s/%s to download", runtime.GOOS, runtime.GOARCH)
	}
	_, _ = fmt.Fprintf(log, "==> Downloading Lima %s, which runs AgentBox's VM (%s)\n", limaRelease.version, url)
	dir := ownLimaDir(p)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".lima-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	tarball := filepath.Join(tmp, "lima.tar.gz")
	if err := fetchChecked(ctx, url, sum, tarball); err != nil {
		return "", fmt.Errorf("downloading Lima: %w", err)
	}
	unpacked := filepath.Join(tmp, "lima")
	if err := untar(tarball, unpacked); err != nil {
		return "", fmt.Errorf("unpacking Lima: %w", err)
	}
	if _, err := os.Stat(filepath.Join(unpacked, "bin", "limactl")); err != nil {
		return "", fmt.Errorf("lima's release has no bin/limactl: %w", err)
	}
	// Whole or not at all: a half-unpacked Lima is never found.
	_ = os.RemoveAll(dir)
	if err := os.Rename(unpacked, dir); err != nil {
		return "", err
	}
	_, _ = fmt.Fprintf(log, "Lima %s is in %s.\n", limaRelease.version, dir)
	return ownLimactl(p), nil
}

// fetchChecked downloads url to dst, and fails unless its SHA256 is sum.
func fetchChecked(ctx context.Context, url, sum, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("%s has SHA256 %s, not the %s this AgentBox expects", url, got, sum)
	}
	return nil
}

// untar unpacks the .tar.gz at tarball into dir: its directories, files and
// symbolic links, none of which may reach outside dir.
func untar(tarball, dir string) error {
	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(hdr.Name)
		if name == "." {
			continue
		}
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("%s is outside the archive", hdr.Name)
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o755|0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			to := path.Join(path.Dir(name), hdr.Linkname)
			if path.IsAbs(hdr.Linkname) || to == ".." || strings.HasPrefix(to, "../") {
				return fmt.Errorf("%s links outside the archive, to %s", hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}
