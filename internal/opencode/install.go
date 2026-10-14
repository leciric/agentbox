package opencode

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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"agentbox/internal/image"
)

// AgentBox signs agents in to OpenCode, and asks it for their models, on the
// machine the daemon runs on: the VM on Linux and the Mac, which has no
// OpenCode of its own, nor Node or mise to install one with. So the OpenCode
// CLI is fetched on first use, the way cloudflared is (internal/tunnel): the
// version tools.txt pins for agents, so the login is written in the format
// the agents' OpenCode reads, the official release's binary, checked against
// the sha256 recorded below, and kept under AgentBox's tools directory.
// Nothing goes on the user's PATH.

// Version is the OpenCode AgentBox runs: the one tools.txt pins.
var Version = func() string {
	spec := image.Pin("opencode")
	return spec[strings.LastIndex(spec, "@")+1:]
}()

// sums are the sha256 of each release tarball AgentBox may download, as
// GitHub lists them for the release's assets: OpenCode publishes no checksums
// file, and a hash kept here can't be swapped along with the tarball it
// vouches for. Moving the pin in tools.txt on means adding the new release's
// lines here (gh release view v<version> -R anomalyco/opencode --json assets);
// a test fails until it does.
var sums = map[string]string{
	"1.18.31/opencode-linux-x64.tar.gz":   "e9312be75ed803b7415fc2aeabda1f4fe938912a39673762dc0c38c0e11ebde4",
	"1.18.31/opencode-linux-arm64.tar.gz": "d4e332f46b227448582c0d9fc75f6f826dfe95c9f751bc2011fc4d937a042be6",
}

// Releases is where OpenCode's release tarballs are downloaded from.
const Releases = "https://github.com/anomalyco/opencode/releases/download"

// installTimeout bounds the download: the tarball is about 60 MiB.
const installTimeout = 10 * time.Minute

// Installer fetches the OpenCode CLI into Dir.
type Installer struct {
	Dir      string // AgentBox's tools directory; opencode goes in <Dir>/opencode/<version>
	Releases string // "" is Releases; tests point it elsewhere
	Arch     string // "" is runtime.GOARCH
}

// Path is where the pinned OpenCode is once installed.
func (i Installer) Path() string {
	return filepath.Join(i.Dir, "opencode", Version, Command)
}

func (i Installer) asset() (string, error) {
	arch := i.Arch
	if arch == "" {
		arch = runtime.GOARCH
	}
	switch arch {
	case "amd64":
		return "opencode-linux-x64.tar.gz", nil
	case "arm64":
		return "opencode-linux-arm64.tar.gz", nil
	}
	return "", fmt.Errorf("OpenCode publishes no linux/%s build AgentBox knows", arch)
}

// Ensure returns the path of an OpenCode CLI to run: the pinned one, downloaded
// the first time. When that download fails, an opencode already on PATH will
// do; only with neither is it an error. status says what it is doing while it
// downloads.
func (i Installer) Ensure(ctx context.Context, status func(string)) (string, error) {
	own := i.Path()
	if st, err := os.Stat(own); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
		return own, nil
	}
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	err := i.install(ctx, status)
	if err == nil {
		return own, nil
	}
	if path, lookErr := exec.LookPath(Command); lookErr == nil {
		return path, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("downloading OpenCode took longer than %s: check this machine's connection", installTimeout)
	}
	return "", fmt.Errorf("installing OpenCode %s: %w", Version, err)
}

// install downloads the release tarball, checks its sha256, unpacks its one
// binary and only then moves it into place, so an interrupted install leaves
// nothing half done.
func (i Installer) install(ctx context.Context, status func(string)) error {
	asset, err := i.asset()
	if err != nil {
		return err
	}
	want, ok := sums[Version+"/"+asset]
	if !ok {
		return fmt.Errorf("AgentBox has no checksum for %s %s", asset, Version)
	}
	base := i.Releases
	if base == "" {
		base = Releases
	}
	url := fmt.Sprintf("%s/v%s/%s", strings.TrimSuffix(base, "/"), Version, asset)

	dir := filepath.Dir(i.Path())
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	tarball, err := os.CreateTemp(parent, ".download-*.tar.gz")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tarball.Name()) }()
	defer func() { _ = tarball.Close() }()

	status("Downloading OpenCode, once for this machine")
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
		return fmt.Errorf("downloading %s: %s", asset, resp.Status)
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tarball, hash), io.LimitReader(resp.Body, 512<<20)); err != nil {
		return fmt.Errorf("downloading %s: %w", asset, err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		return fmt.Errorf("%s doesn't match its checksum (got sha256 %s, want %s)", asset, got, want)
	}

	staging, err := os.MkdirTemp(parent, ".staging-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if _, err := tarball.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := extract(tarball, filepath.Join(staging, Command)); err != nil {
		return fmt.Errorf("unpacking %s: %w", asset, err)
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(staging, dir); err != nil {
		return err
	}
	// Versions an older AgentBox installed are no use to anything now.
	if entries, err := os.ReadDir(parent); err == nil {
		for _, e := range entries {
			if e.IsDir() && e.Name() != Version && !strings.HasPrefix(e.Name(), ".") {
				_ = os.RemoveAll(filepath.Join(parent, e.Name()))
			}
		}
	}
	return nil
}

// extract writes the tarball's opencode, the only file in it, to dst.
func extract(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("it has no opencode")
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || strings.TrimPrefix(h.Name, "./") != Command {
			continue
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	}
}
