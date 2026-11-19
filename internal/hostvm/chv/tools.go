package chv

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// A tool is one of the programs that run the VM, fetched into Layout.Bin. All
// of them are static, so they run on any Linux distribution, and all are
// checked against a SHA256 pinned here: nothing AgentBox runs the VM with can
// change under it without a new AgentBox. Each is fetched from its own
// project, on the user's machine, but Cloud Hypervisor (chvBundle), which
// AgentBox builds with patches and publishes itself.
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

// A bundle is a tarball of tools AgentBox builds itself, published as a
// release of its own repository (scripts/chv-gpu/build.sh, by the Cloud
// Hypervisor workflow): pinned by the tarball's SHA256, and each file in it by
// its own, which EnsureTools compares with what's installed, as for a tool.
type bundle struct {
	name    string // for messages
	version string
	url     string
	sha256  string // the tarball's
	files   []bundleFile
}

type bundleFile struct {
	member string // its path in the tarball, under its top directory
	name   string // its file in Layout.Bin, which may be in a directory
	sha256 string
	exec   bool
}

// chvBundle is Cloud Hypervisor, which every Linux VM runs, and the GPU
// device that runs beside it when "GPU for agents" is on (gpu.go): v53.0 with
// Spectrum OS's virtio-gpu patchset rebased onto it (scripts/chv-gpu/patches),
// built static, which without --gpu runs as upstream's v53.0 does; crosvm's
// GPU device and virglrenderer with Venus, which use the host's own graphics
// libraries (Mesa, libdrm, libgbm, the Vulkan loader). crosvm finds
// libvirglrenderer in ../lib and virgl_render_server is passed to it, so the
// three keep the tarball's layout under gpuDir.
var chvBundle = bundle{
	name:    "cloud-hypervisor",
	version: "v53.0 (AgentBox build bc3e70f239d7)",
	url:     "https://github.com/leciric/agentbox/releases/download/chv-53.0-bc3e70f239d7/chv-53.0-bc3e70f239d7-x86_64.tar.gz",
	sha256:  "928e8a3eecd23e57e6ff7d0ee0f3c0ffd4d65ef023d4402eb748e6f405f25875",
	files: []bundleFile{
		// We don't need ch-remote: the supervisor talks to Cloud
		// Hypervisor's REST API over its socket itself.
		{member: "bin/cloud-hypervisor", name: "cloud-hypervisor", sha256: "010a1b969da846bcda2ceaa6c835971200c0c459a5e073fbf299d0ce1be438e8", exec: true},
		{member: "bin/crosvm", name: gpuDir + "/bin/crosvm", sha256: "e632d3721d94d4bf8b359ac89ec77f08de9cb0ae027950eb858f0ff475028315", exec: true},
		{member: "libexec/virgl_render_server", name: gpuDir + "/libexec/virgl_render_server", sha256: "fd82fe769454b0c7d0fe0477ce78e78bf1d3cc10eacdf5e8c97db26e56b58246", exec: true},
		{member: "lib/libvirglrenderer.so.1.10.0", name: gpuDir + "/lib/libvirglrenderer.so.1", sha256: "e7ba0059daeb04cc5468a162d7e4b57fa8c20833eb018dbf1b4bc5ae6340b7f2"},
	},
}

// gpuDir is where chvBundle's GPU device goes, under Layout.Bin.
const gpuDir = "gpu"

var errArch = fmt.Errorf("AgentBox's Cloud Hypervisor VM runs only on x86_64 (amd64) Linux for now, not %s", runtime.GOARCH)

// EnsureTools fetches the programs that run the VM into l.Bin, when they
// aren't there at the versions this build pins: cloud-hypervisor and the GPU
// device (chvBundle), its UEFI firmware (CLOUDHV.fd), virtiofsd and passt,
// all checked against pinned SHA256 sums.
func EnsureTools(ctx context.Context, l Layout, log io.Writer) error {
	if runtime.GOARCH != "amd64" {
		return errArch
	}
	if err := os.MkdirAll(filepath.Dir(l.Bin("x")), 0o755); err != nil {
		return err
	}
	if err := ensureBundle(ctx, l, chvBundle, log); err != nil {
		// A VM that ran before has a Cloud Hypervisor: it starts with that
		// one rather than not at all, and without the GPU when the GPU
		// device isn't there (gpu.go says so).
		if _, statErr := os.Stat(l.Bin("cloud-hypervisor")); statErr != nil {
			return err
		}
		imageLogf(log, "warning: %v; the VM runs the Cloud Hypervisor it has, and AgentBox tries again at its next start\n", err)
	}
	for _, t := range tools {
		if err := ensureTool(ctx, l, t, log); err != nil {
			return err
		}
	}
	return nil
}

// ensureBundle installs b's files into l.Bin unless each is there with its
// pinned sum: from the tarball, checked against its pin, each file checked
// against its own and put in place with a rename, so a running VM's programs
// are never written over.
func ensureBundle(ctx context.Context, l Layout, b bundle, log io.Writer) error {
	missing := false
	for _, f := range b.files {
		if sum, err := fileSum(l.Bin(f.name), sha256.New()); err != nil || sum != f.sha256 {
			missing = true
			break
		}
	}
	if !missing {
		return nil
	}
	imageLogf(log, "Fetching %s %s…\n", b.name, b.version)
	start := time.Now()
	tarball := l.Bin(b.name + ".tar.gz")
	defer func() { _ = os.Remove(l.Bin(b.name + ".tar.gz")) }()
	fail := func(err error) error { return fmt.Errorf("fetching %s %s: %w", b.name, b.version, err) }
	if local := os.Getenv("AGENTBOX_CHV_TARBALL"); local != "" {
		// A build not published yet, to try it (scripts/chv-gpu/check-vm.sh):
		// it must still be the pinned one.
		sum, err := fileSum(local, sha256.New())
		if err != nil {
			return fail(err)
		}
		if sum != b.sha256 {
			return fail(fmt.Errorf("%s: %w (got %s, want %s)", local, errChecksum, sum, b.sha256))
		}
		tarball = local
	} else if _, err := download(ctx, b.url, tarball, sha256.New(), b.sha256, log); err != nil {
		return fail(err)
	}
	if err := untarFiles(tarball, b.files, l); err != nil {
		return fail(err)
	}
	imageLogf(log, "Fetched %s in %s\n", b.name, time.Since(start).Round(100*time.Millisecond))
	return nil
}

// untarFiles installs files from the .tar.gz at tarball, whose members are
// under one top directory, into l.Bin: each into a .part first and checked,
// and renamed only once all are, so a bad file installs none. Every file must
// be in it.
func untarFiles(tarball string, files []bundleFile, l Layout) error {
	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	want := map[string]bundleFile{}
	for _, bf := range files {
		want[bf.member] = bf
	}
	var parts []string
	defer func() {
		for _, p := range parts {
			_ = os.Remove(p)
		}
	}()
	tr := tar.NewReader(gz)
	for len(want) > 0 {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		_, member, _ := strings.Cut(hdr.Name, "/")
		bf, ok := want[member]
		if !ok || hdr.Typeflag != tar.TypeReg {
			continue
		}
		delete(want, member)
		part := l.Bin(bf.name) + ".part"
		parts = append(parts, part)
		if err := writePart(tr, part, bf.sha256, bf.exec); err != nil {
			return fmt.Errorf("%s: %w", member, err)
		}
	}
	if len(want) > 0 {
		return fmt.Errorf("%s has no %s", filepath.Base(tarball), strings.Join(slices.Sorted(maps.Keys(want)), ", "))
	}
	for _, p := range parts {
		if err := os.Rename(p, strings.TrimSuffix(p, ".part")); err != nil {
			return err
		}
	}
	return nil
}

// writePart writes r to part, if its SHA256 is want.
func writePart(r io.Reader, part, want string, exec bool) error {
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if exec {
		mode = 0o755
	}
	out, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
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
		return fmt.Errorf("%w (got %s, want %s)", errChecksum, got, want)
	}
	return os.Chmod(part, mode)
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
