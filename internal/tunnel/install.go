// Package tunnel runs a Cloudflare Tunnel, so a phone can chat with AgentBox
// from anywhere rather than only on the local network (internal/daemon/lan.go).
//
// cloudflared is fetched on first use, the way the lead's GitHub CLI is
// (internal/agent/hostgh.go): the version tools.txt pins, the official
// release's binary, checked against the sha256 recorded below, and kept under
// AgentBox's tools directory. Nothing goes on the user's PATH.
//
// The tunnel itself is a supervised cloudflared process (Tunnel): a quick
// tunnel, on a trycloudflare.com address that needs no account and changes
// every time it starts, or a named tunnel, from a token the user made in
// Cloudflare's dashboard, on a hostname of their own.
package tunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"agentbox/internal/image"
)

// Version is the cloudflared AgentBox runs: the one tools.txt pins.
var Version = func() string {
	spec := image.Pin("cloudflared")
	return spec[strings.LastIndex(spec, "@")+1:]
}()

// sums are the sha256 of each release binary AgentBox may download, as the
// release's notes list them: cloudflared publishes no checksums file, and a
// hash kept here can't be swapped along with the binary it vouches for.
// Moving the pin in tools.txt on means adding the new release's lines here;
// a test fails until it does.
var sums = map[string]string{
	"2026.9.3/cloudflared-linux-amd64": "77e26d8d900e0b8469f416239d14b5f296525fdf79fee6f511ef55609e3fbac2",
	"2026.9.3/cloudflared-linux-arm64": "aaeb2d7d0da3614634c7e03ab13487a1522c2e79165ed2929cfe23d5e95b326d",
}

// Releases is where cloudflared's release binaries are downloaded from.
const Releases = "https://github.com/cloudflare/cloudflared/releases/download"

// installTimeout bounds the download: the binary is about 40 MiB.
const installTimeout = 10 * time.Minute

// Installer fetches cloudflared into Dir.
type Installer struct {
	Dir      string // AgentBox's tools directory; cloudflared goes in <Dir>/cloudflared/<version>
	Releases string // "" is Releases; tests point it elsewhere
	Arch     string // "" is runtime.GOARCH
}

// Path is where the pinned cloudflared is once installed.
func (i Installer) Path() string {
	return filepath.Join(i.Dir, "cloudflared", Version, "cloudflared")
}

func (i Installer) asset() (string, error) {
	arch := i.Arch
	if arch == "" {
		arch = runtime.GOARCH
	}
	switch arch {
	case "amd64", "arm64":
		return "cloudflared-linux-" + arch, nil
	}
	return "", fmt.Errorf("cloudflared publishes no linux/%s build AgentBox knows", arch)
}

// Ensure returns the path of the pinned cloudflared, downloading it the first
// time. status says what it is doing while it downloads.
func (i Installer) Ensure(ctx context.Context, status func(string)) (string, error) {
	own := i.Path()
	if st, err := os.Stat(own); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
		return own, nil
	}
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	if err := i.install(ctx, status); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("downloading cloudflared took longer than %s: check this machine's connection", installTimeout)
		}
		return "", fmt.Errorf("installing cloudflared %s: %w", Version, err)
	}
	return own, nil
}

// install downloads the release binary, checks its sha256, and only then
// moves it into place, so an interrupted install leaves nothing half done.
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
	url := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(base, "/"), Version, asset)

	dir := filepath.Dir(i.Path())
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(parent, ".download-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	defer func() { _ = f.Close() }()

	status("Downloading cloudflared, once for this machine")
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
	if _, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, 256<<20)); err != nil {
		return fmt.Errorf("downloading %s: %w", asset, err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		return fmt.Errorf("%s doesn't match its checksum (got sha256 %s, want %s)", asset, got, want)
	}
	if err := f.Chmod(0o755); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), i.Path()); err != nil {
		return err
	}
	// Versions an older AgentBox installed are no use to anything now.
	if entries, err := os.ReadDir(parent); err == nil {
		for _, e := range entries {
			if e.IsDir() && e.Name() != Version {
				_ = os.RemoveAll(filepath.Join(parent, e.Name()))
			}
		}
	}
	return nil
}
