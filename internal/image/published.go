package image

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"agentbox/internal/incus"
)

// The published image is the base image as CI builds it from main, with no
// optional components and without the tools AgentBox may not redistribute:
// Debian plus provision.sh, around the placeholder user. A new machine
// downloads it instead of building one (Build), installs the tools it leaves
// out at their tools.txt pins, and those of Codex and OpenCode when they're on, and makes it its own as it would a local build.
// It lives on this repository's GitHub releases, one prerelease per image
// version, tagged image-<version> and never marked latest, so app releases and
// their update check don't see it, and on the CDN the app's releases are
// downloaded from, under images/image-<version>/, which is tried first.
// Assets are flat, so each carries its architecture, and the image is split
// into parts, which Setup downloads at once, joins and checks:
//
//	agentbox-base-<arch>.tar.zst.000, .001, …  the image, compressed with zstd, in parts
//	agentbox-base-<arch>.tar.gz.000, .001, …   the same as `incus image export` writes it, gzipped (GitHub only, see gzipUntil)
//	image-<arch>.json                          what it is (Published)
//	packages-<arch>.txt                        its Debian packages, with their versions
//	SOURCES-<arch>.md                          where their sources are, and the tools' licenses
//	SHA256SUMS                                 the sums of all of the above, for every architecture
//
// Only the newest image release is kept (scripts/github-images.sh): an
// AgentBox whose version is gone falls back to building its own, as one does
// when the download fails or a component is on.

// DefaultImagesURLs are where published images are, the first preferred,
// unless the daemon's AGENTBOX_IMAGES_URL says otherwise: the CDN in front of
// the R2 bucket the app's releases are downloaded from, and GitHub's release
// downloads, which need no authentication since the repository is public and
// have every image the CDN has. Build adds /image-<version>/<asset>.
var DefaultImagesURLs = []string{
	"https://downloads.agentbox.linting.dev/images",
	"https://github.com/leciric/agentbox/releases/download",
}

// partSize is how big the image's parts are: small enough that several
// download at once, each over a connection of its own, and well under
// GitHub's cap of 2 GiB per asset. A variable for the tests.
var partSize int64 = 128 << 20

// gzipUntil is the last image version also published gzipped, as before zstd:
// an AgentBox from before then reads only the gzipped image, and finds it on
// GitHub under the same names. One of a later version can only be this
// AgentBox or a newer one, which reads the zstd one, so the gzipped one stops
// with the next image version, and nothing needs to remember to drop it.
const gzipUntil = "2026.10.20.1"

// zstdArgs say how hard BuildForPublishing compresses the image. Its window
// stays at zstd's default limit of 128 MiB (--long=27), which every zstd
// unpacks without being told to allow more.
var zstdArgs = []string{"-19", "--long=27", "-T0", "-q", "-c"}

// Asset names, per architecture.
func imageFile(arch, ext string) string  { return "agentbox-base-" + arch + ext }
func manifestFile(arch string) string    { return "image-" + arch + ".json" }
func packagesFile(arch string) string    { return "packages-" + arch + ".txt" }
func sourcesFile(arch string) string     { return "SOURCES-" + arch + ".md" }
func partFile(file string, i int) string { return fmt.Sprintf("%s.%03d", file, i) }

// ImagesURLs are where Build looks for a published image, in order:
// AGENTBOX_IMAGES_URL, one URL or several separated by spaces, or
// DefaultImagesURLs. "off" turns downloading off, so every build is local.
func ImagesURLs() []string {
	switch v := os.Getenv("AGENTBOX_IMAGES_URL"); strings.TrimSpace(v) {
	case "":
		return DefaultImagesURLs
	case "off":
		return nil
	default:
		var out []string
		for _, u := range strings.Fields(v) {
			out = append(out, strings.TrimRight(u, "/"))
		}
		return out
	}
}

// unpublished are the tools tools.txt pins for every image that the published
// image leaves out, because AgentBox may not redistribute them: Claude Code
// is Anthropic's proprietary software, its ACP adapter bundles Anthropic's
// Agent SDK, and Cursor's SDK is Cursor's. Each machine installs them itself,
// from their own registries, as it always has. Every other tool is in
// toolLicenses, which says why it may be published.
var unpublished = []string{"claude", "npm:@agentclientprotocol/claude-agent-acp", "npm:@cursor/sdk"}

// toolLicenses are the licenses of the tools the published image has, and
// where their source is: SOURCES-<arch>.md lists them. A tool tools.txt starts
// pinning for every image goes here, once its license allows publishing it,
// or into unpublished (TestEveryToolIsPublishedOrNot).
var toolLicenses = map[string]struct{ License, Source string }{
	"go":                  {"BSD-3-Clause", "https://go.googlesource.com/go"},
	"node":                {"MIT, with its bundled dependencies' permissive licenses", "https://github.com/nodejs/node"},
	"pnpm":                {"MIT", "https://github.com/pnpm/pnpm"},
	"gh":                  {"MIT", "https://github.com/cli/cli"},
	"npm:@playwright/mcp": {"Apache-2.0", "https://github.com/microsoft/playwright-mcp"},
}

// PublishedTools are the tools the published image has: every image's tools
// but the ones in unpublished.
func PublishedTools() []Tool {
	var out []Tool
	for _, t := range ToolsFor(Components{}) {
		if !slices.Contains(unpublished, t.Name()) {
			out = append(out, t)
		}
	}
	return out
}

// Published is image-<arch>.json: what a published image is.
type Published struct {
	// Version is the image version it was built for, and Arch Go's name for
	// its architecture.
	Version string `json:"version"`
	Arch    string `json:"arch"`
	// Archive is the image as Incus imports it: gzipped while gzipUntil
	// says, for the AgentBox releases that read only this, and compressed
	// with zstd after. Zstd is the same image compressed with zstd, smaller
	// and much quicker to unpack, next to a gzipped Archive.
	Archive
	Zstd *Archive `json:"zstd,omitempty"`
	// Tools are the specs of the agent tools in it, like "go@1.27.2": tools.txt
	// may have moved on since, and a machine that downloads it installs
	// whatever differs.
	Tools        []string `json:"tools"`
	ToolsVersion string   `json:"toolsVersion"`
	// Commit is the AgentBox commit that built it, and Built when.
	Commit string    `json:"commit,omitempty"`
	Built  time.Time `json:"built"`
}

// Archive is one file of the image, as Incus imports it. File is its name once
// joined, and Size and SHA256 its length and sum. Parts are the assets it is
// split into, in order, with theirs (SHA256SUMS has the sums too, and is
// what's checked).
type Archive struct {
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Parts  []Part `json:"parts"`
}

// zstd says whether a is compressed with zstd.
func (a Archive) zstd() bool { return strings.HasSuffix(a.File, ".tar.zst") }

// pick is the archive to download: the zstd one, smaller and quicker to
// unpack, unless this machine has no zstd for Incus to unpack it with and a
// gzipped one is there too. A zstd one picked without zstd is unpacked here
// before Incus imports it (unzstd).
func (p Published) pick(zstd bool) Archive {
	if p.Zstd != nil && zstd {
		return *p.Zstd
	}
	return p.Archive
}

// Part is one asset of a published image split in parts.
type Part struct {
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// progressEvery is how often, in percent, the download says how far it got.
const progressEvery = 10

// download fetches the published image for this image version and this
// machine's architecture into dir from the first of bases that has it, joins
// its parts and checks them against SHA256SUMS: each part, the whole, and what
// image-<arch>.json says it is. A missing image, a failed download or a sum
// that doesn't match is an error. A download that fails leaves what it got
// in dir, and the next one carries on from there. It returns the image's
// path, ready for Incus.
func download(ctx context.Context, client *http.Client, bases []string, dir string, log io.Writer) (string, Published, error) {
	if client == nil {
		client = http.DefaultClient
	}
	m := newMirrors(bases)
	var p Published
	var sums map[string]string
	var errs []error
	for i, url := range m {
		var err error
		if p, sums, err = fetchManifest(ctx, client, url); err == nil {
			// Parts from the mirror that answered first, then from the
			// others: they are checked against the same sums.
			m = append(mirrors{url}, append(m[:i:i], m[i+1:]...)...)
			break
		}
		if ctx.Err() != nil {
			return "", Published{}, ctx.Err()
		}
		errs = append(errs, err)
	}
	if len(errs) == len(m) {
		return "", Published{}, errors.Join(errs...)
	}
	a := p.pick(haveZstd())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", Published{}, err
	}
	if a.zstd() {
		_, _ = fmt.Fprintf(log, "%s: %d MB, compressed with zstd\n", a.File, a.Size>>20)
	}
	path, err := fetchArchive(ctx, client, m, dir, a, sums, log)
	if err != nil {
		return "", Published{}, err
	}
	if a.zstd() && !haveZstd() {
		_, _ = fmt.Fprintln(log, "Decompressing it, since this machine has no zstd for Incus to unpack it with")
		tar, err := unzstd(path)
		if err != nil {
			_ = os.Remove(path)
			return "", Published{}, err
		}
		path = tar
	}
	return path, p, nil
}

// fetchManifest gets SHA256SUMS and image-<arch>.json from url and checks
// that they describe an image this machine can download.
func fetchManifest(ctx context.Context, client *http.Client, url string) (Published, map[string]string, error) {
	sumsFile, err := fetchSmall(ctx, client, url+"SHA256SUMS")
	if err != nil {
		return Published{}, nil, err
	}
	sums := parseSums(sumsFile)
	name := manifestFile(runtime.GOARCH)
	manifest, err := fetchSmall(ctx, client, url+name)
	if err != nil {
		return Published{}, nil, err
	}
	if sums[name] == "" || sha256Hex(manifest) != sums[name] {
		return Published{}, nil, fmt.Errorf("%s doesn't match its SHA256SUMS line", name)
	}
	var p Published
	if err := json.Unmarshal(manifest, &p); err != nil {
		return Published{}, nil, fmt.Errorf("reading %s: %w", name, err)
	}
	if p.Version != Version || p.Arch != runtime.GOARCH {
		return Published{}, nil, fmt.Errorf("%s is for version %s on %s, not %s on %s", name, p.Version, p.Arch, Version, runtime.GOARCH)
	}
	archives := []Archive{p.Archive}
	if p.Zstd != nil {
		archives = append(archives, *p.Zstd)
	}
	for _, a := range archives {
		if err := checkArchive(name, a, sums); err != nil {
			return Published{}, nil, err
		}
	}
	return p, sums, nil
}

// checkArchive checks that a, from the manifest name, is an image's archive
// the download can write and check.
func checkArchive(name string, a Archive, sums map[string]string) error {
	switch {
	case a.File == "" || a.File != filepath.Base(a.File) || strings.HasPrefix(a.File, "."):
		return fmt.Errorf("%s names no image file it could have: %q", name, a.File)
	case len(a.SHA256) != 64:
		return fmt.Errorf("%s gives %s no sum", name, a.File)
	case len(a.Parts) == 0:
		return fmt.Errorf("%s lists no parts of the image", name)
	}
	var total int64
	for i, part := range a.Parts {
		switch {
		case part.File != partFile(a.File, i):
			return fmt.Errorf("%s lists part %d as %q, not %q", name, i, part.File, partFile(a.File, i))
		case sums[part.File] == "":
			return fmt.Errorf("SHA256SUMS has no line for %s", part.File)
		case part.Size <= 0:
			return fmt.Errorf("%s gives %s no size", name, part.File)
		}
		total += part.Size
	}
	if total != a.Size {
		return fmt.Errorf("%s: the parts add up to %d bytes, not the image's %d", name, total, a.Size)
	}
	return nil
}

// fetchSmall gets a small file whole, refusing one over a megabyte.
func fetchSmall(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("none is published for image version %s on %s (%s answered 404)", Version, runtime.GOARCH, url)
		}
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	const limit = 1 << 20
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	if len(b) > limit {
		return nil, fmt.Errorf("%s is bigger than it can be", url)
	}
	return b, nil
}

// parseSums reads a SHA256SUMS file as sha256sum writes it: a sum, two spaces
// (or a space and a star) and a name.
func parseSums(b []byte) map[string]string {
	sums := map[string]string{}
	for line := range strings.Lines(string(b)) {
		sum, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || len(sum) != 64 {
			continue
		}
		sums[strings.TrimLeft(name, " *")] = strings.ToLower(sum)
	}
	return sums
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fromPublished makes next from the published image, up to where a local
// build would have it: the placeholder user, every tool tools.txt pins for an
// image with opts' components, stopped. Only components that are tools alone
// (Codex, OpenCode) can be added this way; makeNext builds the others here. The image is downloaded, checked and
// imported into Incus for as long as next is made from it, then deleted.
func fromPublished(ctx context.Context, inc incus.Client, next string, u User, opts Options, log io.Writer) error {
	step := stepper(log)
	step("Downloading the published base image %s for %s from %s", Version, runtime.GOARCH, strings.Join(opts.ImagesURLs, " or "))
	file, p, err := download(ctx, opts.HTTP, opts.ImagesURLs, opts.DownloadDir, log)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file) }()

	alias := Base + "-download"
	// One an interrupted build left behind.
	_ = inc.DeleteImage(ctx, alias)
	step("Importing it into Incus")
	if err := inc.ImportImage(ctx, file, alias); err != nil {
		return err
	}
	defer func() { _ = inc.DeleteImage(context.WithoutCancel(ctx), alias) }()
	_ = os.Remove(file)

	step("Creating %s from it", next)
	if err := all(
		func() error { return inc.Init(ctx, alias, next, "default", Profile) },
		func() error { return inc.SetConfig(ctx, next, "raw.idmap="+IDMap(u)) },
		func() error { return inc.Start(ctx, next) },
	); err != nil {
		return err
	}
	if _, err := inc.WaitReady(ctx, next, readyTimeout); err != nil {
		return err
	}
	// The published image records its tools the way a base image does, so
	// PlanFor says what to install: the ones it leaves out, and any tools.txt
	// has moved on since it was built.
	has := make([]Tool, 0, len(p.Tools))
	for _, spec := range p.Tools {
		has = append(has, Tool{Spec: spec})
	}
	// It's planned as if it had the components already, so the plan installs
	// their tools too.
	components := opts.Components
	plan := PlanFor(true, Installed{Version: Version, Components: components, ToolsVersion: ToolsVersion(has), Tools: p.Tools}, components, nil)
	if components.Any() {
		step("Installing the agent tools it leaves out and those of %s, as on a local build", components.Summary())
	} else {
		step("Installing the agent tools it leaves out, as on a local build")
	}
	if dirs := components.homeDirs(); len(dirs) > 0 {
		if _, err := inc.Exec(ctx, next, "runuser", "-l", Placeholder.Name, "-c", "mkdir -p "+strings.Join(dirs, " ")); err != nil {
			return err
		}
	}
	if err := installTools(ctx, inc, next, Placeholder.Name, plan, log); err != nil {
		return err
	}
	return inc.Stop(ctx, next)
}

// BuildForPublishing builds the image CI publishes into dir: what Build makes
// before it is made a machine's own, with no optional components and only
// PublishedTools, exported as one file with the files that go next to it
// (see the top of this file). It runs on CI, never in the daemon, and leaves
// nothing behind in Incus. commit is AgentBox's commit, for image-<arch>.json.
func BuildForPublishing(ctx context.Context, inc incus.Client, u User, dir, commit string, log io.Writer) error {
	step := stepper(log)
	if err := EnsureProfile(ctx, inc); err != nil {
		return err
	}
	name := Base + "-publish"
	if err := remove(ctx, inc, name); err != nil {
		return err
	}
	defer func() { _ = inc.Delete(context.WithoutCancel(ctx), name) }()
	tools := PublishedTools()
	if err := buildLocally(ctx, inc, name, u, Components{}, tools, log); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	step("Listing its Debian packages, for their sources")
	tmp, err := os.MkdirTemp(dir, ".export-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	status := filepath.Join(tmp, "status")
	if err := inc.PullFile(ctx, name, "/var/lib/dpkg/status", status); err != nil {
		return err
	}
	b, err := os.ReadFile(status)
	if err != nil {
		return err
	}
	packages := parseDpkgStatus(b)
	if len(packages) == 0 {
		return errors.New("the image has no Debian packages installed, which can't be right")
	}

	step("Exporting it")
	_ = inc.DeleteImage(ctx, name)
	// gzip, which every Incus unpacks, for the AgentBox releases that read
	// only that; the zstd one is made from it.
	if err := inc.Publish(ctx, name, name, "gzip"); err != nil {
		return err
	}
	defer func() { _ = inc.DeleteImage(context.WithoutCancel(ctx), name) }()
	if err := inc.ExportImage(ctx, name, tmp); err != nil {
		return err
	}
	exported, err := filepath.Glob(filepath.Join(tmp, "*.tar.*"))
	if err != nil || len(exported) != 1 {
		return fmt.Errorf("incus image export wrote %d image files, not one", len(exported))
	}
	step("Compressing it with zstd")
	gz := exported[0]
	zst := filepath.Join(tmp, "image.tar.zst")
	if err := recompress(ctx, gz, zst); err != nil {
		return err
	}
	step("Splitting it into parts of at most %d MiB", partSize>>20)
	var archives []Archive
	for _, f := range []struct{ src, ext string }{{zst, ".tar.zst"}, {gz, ".tar.gz"}} {
		if f.ext == ".tar.gz" && Version != gzipUntil {
			continue
		}
		file := imageFile(runtime.GOARCH, f.ext)
		parts, err := splitFile(f.src, filepath.Join(dir, file), partSize)
		if err != nil {
			return err
		}
		archives = append(archives, Archive{File: file, Parts: parts})
	}
	return writePublished(dir, commit, tools, packages, archives, time.Now().UTC())
}

// recompress writes the gzipped image gz again, compressed with zstd, into
// zst: with the zstd command, multithreaded and at a level the Go encoder
// doesn't have. Only CI runs it.
var recompress = func(ctx context.Context, gz, zst string) error {
	in, err := os.Open(gz)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	r, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	out, err := os.Create(zst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	cmd := exec.CommandContext(ctx, "zstd", zstdArgs...)
	cmd.Stdin, cmd.Stdout = r, out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zstd: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Close()
}

// splitFile writes src into parts of at most size bytes named after dest
// (partFile), removes src, and returns the parts with their sizes and sums.
func splitFile(src, dest string, size int64) ([]Part, error) {
	in, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer func() { _ = in.Close() }()
	var parts []Part
	for {
		name := partFile(filepath.Base(dest), len(parts))
		out, err := os.Create(filepath.Join(filepath.Dir(dest), name))
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, size))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		if n == 0 && len(parts) > 0 {
			_ = os.Remove(filepath.Join(filepath.Dir(dest), name))
			break
		}
		parts = append(parts, Part{File: name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))})
		if n < size {
			break
		}
	}
	return parts, os.Remove(src)
}

// writePublished writes the files that go next to the image's parts in dir:
// image-<arch>.json, packages-<arch>.txt, SOURCES-<arch>.md and SHA256SUMS
// over them and the parts. archives are the image's, the first preferred,
// with only their names and parts, already in dir.
func writePublished(dir, commit string, tools []Tool, packages []debPackage, archives []Archive, built time.Time) error {
	for i, a := range archives {
		whole := sha256.New()
		for _, pt := range a.Parts {
			f, err := os.Open(filepath.Join(dir, pt.File))
			if err != nil {
				return err
			}
			n, err := io.Copy(whole, f)
			_ = f.Close()
			if err != nil {
				return err
			}
			archives[i].Size += n
		}
		archives[i].SHA256 = hex.EncodeToString(whole.Sum(nil))
	}
	arch := runtime.GOARCH
	p := Published{
		Version: Version, Arch: arch, Archive: archives[0],
		Tools: specsOf(tools), ToolsVersion: ToolsVersion(tools), Commit: commit, Built: built,
	}
	// An AgentBox from before zstd reads only the top level, so a gzipped
	// archive goes there and the zstd one beside it.
	if len(archives) > 1 {
		p.Archive, p.Zstd = archives[1], &archives[0]
	}
	manifest, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	files := []struct {
		name    string
		content []byte
	}{
		{manifestFile(arch), append(manifest, '\n')},
		{packagesFile(arch), packageList(packages)},
		{sourcesFile(arch), sourcesNotice(p, tools, packages)},
	}
	var sums string
	for _, a := range archives {
		for _, pt := range a.Parts {
			sums += pt.SHA256 + "  " + pt.File + "\n"
		}
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.content, 0o644); err != nil {
			return err
		}
		sums += sha256Hex(f.content) + "  " + f.name + "\n"
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o644)
}

// debPackage is one installed Debian package, and the source package it was
// built from.
type debPackage struct {
	Name, Version, Arch   string
	Source, SourceVersion string
}

// parseDpkgStatus reads /var/lib/dpkg/status for the packages installed.
func parseDpkgStatus(b []byte) []debPackage {
	var out []debPackage
	for para := range strings.SplitSeq(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n\n") {
		fields := map[string]string{}
		sc := bufio.NewScanner(strings.NewReader(para))
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if line == "" || line[0] == ' ' || line[0] == '\t' {
				continue
			}
			if k, v, ok := strings.Cut(line, ":"); ok {
				fields[k] = strings.TrimSpace(v)
			}
		}
		if fields["Package"] == "" || !strings.HasSuffix(fields["Status"], " installed") {
			continue
		}
		p := debPackage{Name: fields["Package"], Version: fields["Version"], Arch: fields["Architecture"],
			Source: fields["Package"], SourceVersion: fields["Version"]}
		// "Source: name" or "Source: name (version)", when they differ from the package's.
		if src := fields["Source"]; src != "" {
			name, ver, _ := strings.Cut(src, " ")
			p.Source = name
			if ver = strings.Trim(ver, "()"); ver != "" {
				p.SourceVersion = ver
			}
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b debPackage) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// packageList is packages-<arch>.txt: one installed package a line, tab-separated,
// with the source package it comes from.
func packageList(packages []debPackage) []byte {
	var b bytes.Buffer
	b.WriteString("# package\tversion\tarchitecture\tsource\tsource version\n")
	for _, p := range packages {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", p.Name, p.Version, p.Arch, p.Source, p.SourceVersion)
	}
	return b.Bytes()
}

// thirdPartySources are the packages system.sh installs from outside Debian,
// from Docker's own repository, and where their source is.
var thirdPartySources = map[string]string{
	"docker-ce":             "https://github.com/moby/moby",
	"docker-ce-cli":         "https://github.com/docker/cli",
	"containerd.io":         "https://github.com/containerd/containerd",
	"docker-compose-plugin": "https://github.com/docker/compose",
	"docker-buildx-plugin":  "https://github.com/docker/buildx",
}

// sourcesNotice is SOURCES-<arch>.md: what the image holds, under which licenses, and
// where the source of each part is, as the GPL asks of whoever publishes it.
func sourcesNotice(p Published, tools []Tool, packages []debPackage) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# AgentBox base image %s (%s): sources and licenses\n\n", p.Version, p.Arch)
	files := p.File
	if p.Zstd != nil {
		files = p.Zstd.File + " and " + p.File + ", the same image compressed two ways,"
	}
	fmt.Fprintf(&b, "%s is Debian 13 with AgentBox's provision.sh applied", files)
	if p.Commit != "" {
		fmt.Fprintf(&b, ", built from https://github.com/leciric/agentbox/tree/%s/internal/image", p.Commit)
	}
	b.WriteString(".\n\n")
	b.WriteString("It leaves out what AgentBox may not redistribute: Claude Code, its ACP adapter\n" +
		"(@agentclientprotocol/claude-agent-acp, which bundles Anthropic's Agent SDK) and Cursor's SDK.\n" +
		"Each machine installs those itself, from their own registries.\n\n")
	b.WriteString("## Debian packages\n\n")
	b.WriteString("packages-<arch>.txt lists every installed package with its exact version and the source package\n" +
		"it was built from. Debian keeps the source of every version it ever published at\n" +
		"snapshot.debian.org; each source package of this image is below.\n\n")
	type src struct{ name, version string }
	seen := map[src]bool{}
	for _, pkg := range packages {
		s := src{pkg.Source, pkg.SourceVersion}
		if seen[s] {
			continue
		}
		seen[s] = true
		if url, ok := thirdPartySources[pkg.Name]; ok {
			fmt.Fprintf(&b, "- %s %s: Docker's package (Apache-2.0), source at %s\n", s.name, s.version, url)
			continue
		}
		fmt.Fprintf(&b, "- %s %s: https://snapshot.debian.org/package/%s/%s/\n", s.name, s.version, s.name, s.version)
	}
	b.WriteString("\n## Other software\n\n")
	b.WriteString("- mise (MIT), which installs the tools below: https://github.com/jdx/mise\n")
	for _, t := range tools {
		l := toolLicenses[t.Name()]
		fmt.Fprintf(&b, "- %s (%s): %s\n", t.Spec, l.License, l.Source)
	}
	return b.Bytes()
}
