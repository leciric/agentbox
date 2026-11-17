package tunnel

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
	"path/filepath"
	"runtime"
	"strings"

	"agentbox/internal/image"
)

// TailscaleVersion is the Tailscale AgentBox runs on a hub that gives out
// tailnet addresses (Tailscale): the one tools.txt pins.
var TailscaleVersion = func() string {
	spec := image.Pin("tailscale")
	return spec[strings.LastIndex(spec, "@")+1:]
}()

// tailscaleSums are the sha256 of each static tarball AgentBox may download,
// as pkgs.tailscale.com publishes them next to it (<tarball>.sha256). Kept
// here, like cloudflared's, so the hash can't be swapped along with the file;
// moving the pin in tools.txt on means adding the new release's lines.
var tailscaleSums = map[string]string{
	"1.104.2/tailscale_1.104.2_amd64.tgz": "512f613ed7d04fc7282f7dc5bd76aa105aae28168fbb9357eb749ebcbc29f0ae",
	"1.104.2/tailscale_1.104.2_arm64.tgz": "5776aaa901b5ef1916e66be5bb54b9f481cd399c9c28db123a8cab620efa237b",
}

// TailscaleReleases is where Tailscale's static tarballs are downloaded from.
const TailscaleReleases = "https://pkgs.tailscale.com/stable"

// TailscaleInstaller fetches tailscale and tailscaled into Dir.
type TailscaleInstaller struct {
	Dir      string // AgentBox's tools directory; they go in <Dir>/tailscale/<version>
	Releases string // "" is TailscaleReleases; tests point it elsewhere
	Arch     string // "" is runtime.GOARCH
}

// Path is the directory the pinned tailscale and tailscaled are in once
// installed.
func (i TailscaleInstaller) Path() string {
	return filepath.Join(i.Dir, "tailscale", TailscaleVersion)
}

func (i TailscaleInstaller) asset() (string, error) {
	arch := i.Arch
	if arch == "" {
		arch = runtime.GOARCH
	}
	switch arch {
	case "amd64", "arm64":
		return fmt.Sprintf("tailscale_%s_%s.tgz", TailscaleVersion, arch), nil
	}
	return "", fmt.Errorf("tailscale publishes no linux/%s build AgentBox knows", arch)
}

// Ensure returns the directory of the pinned tailscale and tailscaled,
// downloading them the first time. status says what it is doing meanwhile.
func (i TailscaleInstaller) Ensure(ctx context.Context, status func(string)) (string, error) {
	dir := i.Path()
	if executable(filepath.Join(dir, "tailscale")) && executable(filepath.Join(dir, "tailscaled")) {
		return dir, nil
	}
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	if err := i.install(ctx, status); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("downloading tailscale took longer than %s: check this machine's connection", installTimeout)
		}
		return "", fmt.Errorf("installing tailscale %s: %w", TailscaleVersion, err)
	}
	return dir, nil
}

func executable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0
}

// install downloads the tarball, checks its sha256, and only then unpacks
// its two binaries into place.
func (i TailscaleInstaller) install(ctx context.Context, status func(string)) error {
	asset, err := i.asset()
	if err != nil {
		return err
	}
	want, ok := tailscaleSums[TailscaleVersion+"/"+asset]
	if !ok {
		return fmt.Errorf("AgentBox has no checksum for %s", asset)
	}
	base := i.Releases
	if base == "" {
		base = TailscaleReleases
	}
	url := strings.TrimSuffix(base, "/") + "/" + asset

	dir := i.Path()
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

	status("Downloading Tailscale, once for this machine")
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

	tmp, err := os.MkdirTemp(parent, ".unpack-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := unpackTailscale(f, tmp); err != nil {
		return fmt.Errorf("unpacking %s: %w", asset, err)
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	// Versions an older AgentBox installed are no use to anything now.
	if entries, err := os.ReadDir(parent); err == nil {
		for _, e := range entries {
			if e.IsDir() && e.Name() != TailscaleVersion && !strings.HasPrefix(e.Name(), ".") {
				_ = os.RemoveAll(filepath.Join(parent, e.Name()))
			}
		}
	}
	return nil
}

// unpackTailscale takes tailscale and tailscaled out of the tarball into dir,
// and nothing else: its systemd files are no use here.
func unpackTailscale(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	found := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Base(h.Name)
		if h.Typeflag != tar.TypeReg || (name != "tailscale" && name != "tailscaled") || strings.Count(strings.Trim(h.Name, "/"), "/") != 1 {
			continue
		}
		out, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, io.LimitReader(tr, 256<<20))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		found++
	}
	if found != 2 {
		return errors.New("it doesn't have tailscale and tailscaled")
	}
	return nil
}
