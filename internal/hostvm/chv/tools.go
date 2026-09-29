package chv

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// A tool is one of the programs that run the VM, fetched into Layout.Bin. All
// of them are static, so they run on any Linux distribution, and all are
// checked against a SHA256 pinned here: nothing AgentBox runs the VM with can
// change under it without a new AgentBox. None of them is ever published by
// AgentBox; each is fetched from its own project, on the user's machine.
type tool struct {
	name    string // its file in Layout.Bin
	version string // for messages
	url     string
	// sha256 is the installed file's: EnsureTools skips a tool whose file in
	// Layout.Bin already has it, so a tool changes exactly when its pin does.
	sha256 string
	// zipMember, for a download that's a zip, is the file in it to install,
	// and zipSHA256 is the zip's own sum, checked before it's opened.
	zipMember string
	zipSHA256 string
	exec      bool
	// latestOnly is a tool its project publishes only as its latest build,
	// so the pin can't be fetched again once upstream moves on: a download
	// that isn't the pinned build is still taken, over HTTPS, with a
	// warning, and one already installed is kept whatever its sum.
	latestOnly bool
}

// tools are the programs EnsureTools fetches, for linux/amd64. Other
// architectures fail with errArch: virtiofsd publishes no static build for
// arm64, and Debian's image and the firmware would need pins of their own.
var tools = []tool{
	{
		// We don't need ch-remote: the supervisor talks to Cloud
		// Hypervisor's REST API over its socket itself.
		name:    "cloud-hypervisor",
		version: "v53.0",
		url:     "https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/v53.0/cloud-hypervisor-static",
		sha256:  "448af3d4e59b22c2987f7df94c213ad40fb53a10d437e42b5ee6c4fce7c29ecc",
		exec:    true,
	},
	{
		// Cloud Hypervisor's own build of EDK2. rust-hypervisor-firmware,
		// the smaller alternative, can't boot Debian's image: its shim fails
		// under it.
		name:    "CLOUDHV.fd",
		version: "edk2 ch-97eeb7b09",
		url:     "https://github.com/cloud-hypervisor/edk2/releases/download/ch-97eeb7b09/CLOUDHV.fd",
		sha256:  "dc2fc8f0e43b96712d9fccc52a3a590769606412b3e1bc911d217addd3bef624",
	},
	{
		// passt.top publishes a static build only as "latest": there's no
		// URL for a version, so the pin is the checksum of the build this
		// AgentBox was tested with, and a newer one is taken with a warning
		// rather than leaving a new install with no network. passt.avx2, which passt runs instead of itself when it's next
		// to it and the CPU has AVX2, is only faster, so it isn't fetched.
		name:       "passt",
		version:    "2026_07_28.f8df3f1-18-gdf90211",
		url:        "https://passt.top/builds/latest/x86_64/passt",
		sha256:     "73320bcc96eaad082660d57d25228e1230f252d9d1fcc7a55c52bedbd0c64385",
		exec:       true,
		latestOnly: true,
	},
	{
		// virtiofsd's CI builds a static (musl) binary for each release,
		// uploaded to GitLab as a zip.
		name:      "virtiofsd",
		version:   "v1.13.3",
		url:       "https://gitlab.com/-/project/21523468/uploads/2da76a374dc47de1cf0186724845d5e1/virtiofsd-v1.13.3.zip",
		zipSHA256: "c79055af8189dcd3d942a16e5c165aa336aabbc47ea8e015c3a6cf9980ff73ab",
		zipMember: "target/x86_64-unknown-linux-musl/release/virtiofsd",
		sha256:    "b3f7d24d7a530515b1a44b035f426c700553cb4f0cd14189051d54c0e6b6ef78",
		exec:      true,
	},
}

var errArch = fmt.Errorf("AgentBox's Cloud Hypervisor VM runs only on x86_64 (amd64) Linux for now, not %s", runtime.GOARCH)

// EnsureTools fetches the programs that run the VM into l.Bin, when they
// aren't there at the versions this build pins: cloud-hypervisor, its UEFI
// firmware (CLOUDHV.fd), virtiofsd and passt, all static, all checked
// against pinned SHA256 sums.
func EnsureTools(ctx context.Context, l Layout, log io.Writer) error {
	if runtime.GOARCH != "amd64" {
		return errArch
	}
	if err := os.MkdirAll(filepath.Dir(l.Bin("x")), 0o755); err != nil {
		return err
	}
	for _, t := range tools {
		if err := ensureTool(ctx, l, t, log); err != nil {
			return err
		}
	}
	return nil
}

func ensureTool(ctx context.Context, l Layout, t tool, log io.Writer) error {
	dst := l.Bin(t.name)
	if sum, err := fileSum(dst, sha256.New()); err == nil && (sum == t.sha256 || t.latestOnly) {
		return nil
	}
	imageLogf(log, "Fetching %s %s…\n", t.name, t.version)
	start := time.Now()
	if t.zipMember == "" {
		want := t.sha256
		if t.latestOnly {
			want = ""
		}
		got, err := download(ctx, t.url, dst, sha256.New(), want, log)
		if err != nil {
			return toolError(t, err)
		}
		if got != t.sha256 {
			imageLogf(log, "warning: %s's latest build (sha256 %s) isn't the one this AgentBox was tested with (%s); using it anyway\n", t.name, got, t.version)
		}
	} else {
		zipFile := dst + ".zip"
		defer func() { _ = os.Remove(zipFile) }()
		if _, err := download(ctx, t.url, zipFile, sha256.New(), t.zipSHA256, log); err != nil {
			return toolError(t, err)
		}
		if err := unzipMember(zipFile, t.zipMember, dst, t.sha256); err != nil {
			return toolError(t, err)
		}
	}
	mode := os.FileMode(0o644)
	if t.exec {
		mode = 0o755
	}
	if err := os.Chmod(dst, mode); err != nil {
		return err
	}
	imageLogf(log, "Fetched %s in %s\n", t.name, time.Since(start).Round(100*time.Millisecond))
	return nil
}

// errChecksum is a download that isn't what its pin says.
var errChecksum = errors.New("checksum mismatch")

func toolError(t tool, err error) error {
	return fmt.Errorf("fetching %s %s: %w", t.name, t.version, err)
}

// download fetches url to dst, checking it against want (hex, of h; ""
// checks nothing): into dst+".part" first and renamed only once it's checked,
// so dst is never a partial or unchecked file. It returns the sum it got.
func download(ctx context.Context, url, dst string, h hash.Hash, want string, log io.Writer) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "agentbox")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	part := dst + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(part) }() // a no-op once it's renamed
	var body io.Reader = resp.Body
	if resp.ContentLength > 64<<20 {
		body = &progress{r: resp.Body, total: resp.ContentLength, log: log}
	}
	if _, err := io.Copy(io.MultiWriter(f, h), body); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("%s: %w", url, err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if want != "" && got != want {
		return "", fmt.Errorf("%s: %w (got %s, want %s)", url, errChecksum, got, want)
	}
	return got, os.Rename(part, dst)
}

// progress says how far a large download has got, every tenth of it.
type progress struct {
	r           io.Reader
	total, done int64
	shown       int64
	log         io.Writer
}

func (p *progress) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if tenth := p.done * 10 / p.total; tenth > p.shown && tenth < 10 {
		p.shown = tenth
		imageLogf(p.log, "  %d%% of %d MB\n", tenth*10, p.total>>20)
	}
	return n, err
}

// unzipMember installs the file name from the zip at zipFile as dst, checked
// against want (SHA256, hex).
func unzipMember(zipFile, name, dst, want string) error {
	z, err := zip.OpenReader(zipFile)
	if err != nil {
		return err
	}
	defer func() { _ = z.Close() }()
	for _, f := range z.File {
		if f.Name != name {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		part := dst + ".part"
		defer func() { _ = os.Remove(part) }()
		out, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		h := sha256.New()
		if _, err := io.Copy(io.MultiWriter(out, h), r); err != nil {
			_ = out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			return fmt.Errorf("%s in %s: %w (got %s, want %s)", name, filepath.Base(zipFile), errChecksum, got, want)
		}
		return os.Rename(part, dst)
	}
	return fmt.Errorf("%s has no %s", filepath.Base(zipFile), name)
}

// fileSum is file's sum with h, in hex.
func fileSum(file string, h hash.Hash) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// newSHA512 is for pins that are SHA512 sums (Debian's).
func newSHA512() hash.Hash { return sha512.New() }
