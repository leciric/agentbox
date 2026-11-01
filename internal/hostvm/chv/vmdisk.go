package chv

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The ready-made VM disk is Debian's cloud image (debianBuild) after the part
// of the VM's first boot that's the same on every machine: the packages
// installed (socat and packages), man-db's trigger off and apt's caches
// emptied, then sealed so that the next boot is a new instance to cloud-init
// (vmdisk-build.yaml.tmpl). The VM's first boot from it does only what's the
// user's: the user, the shares, the keys, the units, Incus's pool. CI builds
// it on main (.github/workflows/published-vm-disk.yml, BuildDiskForPublishing)
// and publishes it as a prerelease of this repository, tagged
// vmdisk-<DiskVersion>, never latest, like the agents' base image:
//
//	agentbox-vm-<arch>.tar.xz   disk.raw, sparse, in a tarball as Debian's own image is
//	vm-disk-<arch>.json         what it is
//	vm-packages-<arch>.txt      its Debian packages, with their versions and sources
//	VM-SOURCES-<arch>.md        where their sources are, as the GPL asks
//	SHA256SUMS                  the sums of all of the above, every architecture
//
// Only the newest is kept (scripts/github-vm-disks.sh). An AgentBox whose
// DiskVersion isn't published, or that can't fetch it, makes the disk from
// Debian's cloud image as before, and its first boot installs the packages.

//go:embed vmdisk-build.yaml.tmpl
var diskBuildTemplate string

// diskFormat is bumped when the disk changes in a way DiskVersion doesn't see:
// what BuildDiskForPublishing does around the template, say.
const diskFormat = "1"

// DiskVersion names the ready-made disk this AgentBox boots: a hash of what
// goes into it, so a change to the Debian build, the packages or the build's
// first boot is a new disk, with no version to bump by hand.
func DiskVersion() string {
	h := sha256.New()
	for _, s := range []string{diskFormat, debianBuild, packages, diskBuildTemplate} {
		_, _ = fmt.Fprintf(h, "%d:%s\n", len(s), s)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// DiskTag is the release the ready-made disk is published under.
func DiskTag() string { return "vmdisk-" + DiskVersion() }

func diskFile(arch string) string         { return "agentbox-vm-" + arch + ".tar.xz" }
func diskManifestFile(arch string) string { return "vm-disk-" + arch + ".json" }
func diskPackagesFile(arch string) string { return "vm-packages-" + arch + ".txt" }
func diskSourcesFile(arch string) string  { return "VM-SOURCES-" + arch + ".md" }

// DefaultDisksURL is where ready-made disks are, unless AGENTBOX_VM_DISKS_URL
// says otherwise ("off" for none): GitHub's release downloads, public, with
// /<DiskTag>/<asset> added.
const DefaultDisksURL = "https://github.com/leciric/agentbox/releases/download"

func disksURL() string {
	if u := os.Getenv("AGENTBOX_VM_DISKS_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return DefaultDisksURL
}

// errNoDisk is a ready-made disk that isn't there to fetch: none published for
// this version or architecture, or fetching turned off.
var errNoDisk = errors.New("no ready-made disk is published for this AgentBox")

// readyMadeDisk makes dst, the VM's system disk at its full size, from the
// published ready-made disk for arch, fetching it into cache unless it's
// there already. Any error leaves dst as it was, for Debian's cloud image to
// make instead.
func readyMadeDisk(ctx context.Context, base, arch, cache, dst string, log io.Writer) error {
	if base == "off" {
		return errNoDisk
	}
	tarball, err := fetchDisk(ctx, base, arch, cache, log)
	if err != nil {
		return err
	}
	imageLogf(log, "Making the VM's system disk from it…\n")
	start := time.Now()
	part := dst + ".part"
	defer func() { _ = os.Remove(part) }()
	if err := extractDiskRaw(ctx, tarball, part); err != nil {
		// A tarball that checks out but won't unpack is no use kept.
		_ = os.Remove(tarball)
		return fmt.Errorf("unpacking the ready-made disk: %w", err)
	}
	if err := os.Truncate(part, RootDisk); err != nil {
		return err
	}
	if err := os.Rename(part, dst); err != nil {
		return err
	}
	imageLogf(log, "Made it in %s\n", time.Since(start).Round(100*time.Millisecond))
	return nil
}

// fetchDisk is the ready-made disk's tarball for arch in cache, fetched and
// checked against the release's SHA256SUMS, or kept from an earlier fetch
// that still checks out. Earlier versions' tarballs are removed.
func fetchDisk(ctx context.Context, base, arch, cache string, log io.Writer) (string, error) {
	url := base + "/" + DiskTag() + "/"
	sums, err := fetchSums(ctx, url+"SHA256SUMS")
	if err != nil {
		return "", err
	}
	want, ok := sums[diskFile(arch)]
	if !ok {
		return "", fmt.Errorf("%w (none for %s in %s)", errNoDisk, arch, DiskTag())
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return "", err
	}
	tarball := filepath.Join(cache, "agentbox-vm-"+DiskVersion()+"-"+arch+".tar.xz")
	if sum, err := fileSum(tarball, sha256.New()); err != nil || sum != want {
		imageLogf(log, "Fetching AgentBox's ready-made VM disk (%s, %s)…\n", DiskVersion(), arch)
		start := time.Now()
		if _, err := download(ctx, url+diskFile(arch), tarball, sha256.New(), want, log); err != nil {
			return "", fmt.Errorf("fetching the ready-made disk: %w", err)
		}
		imageLogf(log, "Fetched it in %s\n", time.Since(start).Round(time.Second))
	}
	old, _ := filepath.Glob(filepath.Join(cache, "agentbox-vm-*.tar.xz"))
	for _, f := range old {
		if f != tarball {
			_ = os.Remove(f)
		}
	}
	return tarball, nil
}

// fetchSums reads a SHA256SUMS: a release that isn't there is errNoDisk.
func fetchSums(ctx context.Context, url string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "agentbox")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w (%s)", errNoDisk, DiskTag())
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return parseSums(io.LimitReader(resp.Body, 1<<20))
}

// parseSums reads sha256sum's output: "<hex>  <name>" a line, the name
// maybe after a "*" (binary mode).
func parseSums(r io.Reader) (map[string]string, error) {
	sums := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		sum, name, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if !ok || len(sum) != 64 {
			continue
		}
		sums[strings.TrimPrefix(strings.TrimSpace(name), "*")] = strings.ToLower(sum)
	}
	return sums, sc.Err()
}
