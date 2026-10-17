package image

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
// out at their tools.txt pins, and makes it its own as it would a local build.
// It lives on this repository's GitHub releases, one prerelease per image
// version, tagged image-<version> and never marked latest, so app releases and
// their update check don't see it. Assets are flat, so each carries its
// architecture, and the image is split into parts under GitHub's 2 GiB cap per
// asset, which Setup joins and checks:
//
//	agentbox-base-<arch>.tar.gz.000, .001, …  the image, as `incus image export` writes it, in parts
//	image-<arch>.json                         what it is (Published)
//	packages-<arch>.txt                       its Debian packages, with their versions
//	SOURCES-<arch>.md                         where their sources are, and the tools' licenses
//	SHA256SUMS                                the sums of all of the above, for every architecture
//
// Only the newest image release is kept (scripts/github-images.sh): an
// AgentBox whose version is gone falls back to building its own, as one does
// when the download fails or a component is on.

// DefaultImagesURL is where published images are, unless the daemon's
// AGENTBOX_IMAGES_URL says otherwise: GitHub's release downloads, which need
// no authentication since the repository is public. Build adds
// /image-<version>/<asset>.
const DefaultImagesURL = "https://github.com/leciric/agentbox/releases/download"

// partSize is how big the image's parts are, well under GitHub's cap of 2 GiB
// per asset so the image can grow. A variable for the tests.
var partSize int64 = 1 << 30

// Asset names, per architecture.
func imageFile(arch, ext string) string  { return "agentbox-base-" + arch + ext }
func manifestFile(arch string) string    { return "image-" + arch + ".json" }
func packagesFile(arch string) string    { return "packages-" + arch + ".txt" }
func sourcesFile(arch string) string     { return "SOURCES-" + arch + ".md" }
func partFile(file string, i int) string { return fmt.Sprintf("%s.%03d", file, i) }

// ImagesURL is where Build looks for a published image: AGENTBOX_IMAGES_URL,
// or DefaultImagesURL. "off" turns downloading off, so every build is local.
func ImagesURL() string {
	switch v := strings.TrimRight(os.Getenv("AGENTBOX_IMAGES_URL"), "/"); v {
	case "":
		return DefaultImagesURL
	case "off":
		return ""
	default:
		return v
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
	// File is the image's name once joined, and Size and SHA256 its length
	// and sum. Parts are the assets it is split into, in order, with theirs
	// (SHA256SUMS has the sums too, and is what's checked).
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Parts  []Part `json:"parts"`
	// Tools are the specs of the agent tools in it, like "go@1.27.2": tools.txt
	// may have moved on since, and a machine that downloads it installs
	// whatever differs.
	Tools        []string `json:"tools"`
	ToolsVersion string   `json:"toolsVersion"`
	// Commit is the AgentBox commit that built it, and Built when.
	Commit string    `json:"commit,omitempty"`
	Built  time.Time `json:"built"`
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
// machine's architecture into dir, joins its parts and checks them against
// SHA256SUMS: each part, the whole, and what image-<arch>.json says it is. A missing
// image, a failed download or a sum that doesn't match is an error, and leaves
// no file. It returns the joined image's path.
func download(ctx context.Context, client *http.Client, base, dir string, log io.Writer) (string, Published, error) {
	if client == nil {
		client = http.DefaultClient
	}
	url := strings.TrimRight(base, "/") + "/image-" + Version + "/"
	sumsFile, err := fetchSmall(ctx, client, url+"SHA256SUMS")
	if err != nil {
		return "", Published{}, err
	}
	sums := parseSums(sumsFile)
	name := manifestFile(runtime.GOARCH)
	manifest, err := fetchSmall(ctx, client, url+name)
	if err != nil {
		return "", Published{}, err
	}
	if sums[name] == "" || sha256Hex(manifest) != sums[name] {
		return "", Published{}, fmt.Errorf("%s doesn't match its SHA256SUMS line", name)
	}
	var p Published
	if err := json.Unmarshal(manifest, &p); err != nil {
		return "", Published{}, fmt.Errorf("reading %s: %w", name, err)
	}
	switch {
	case p.Version != Version || p.Arch != runtime.GOARCH:
		return "", Published{}, fmt.Errorf("%s is for version %s on %s, not %s on %s", name, p.Version, p.Arch, Version, runtime.GOARCH)
	case p.File == "" || p.File != filepath.Base(p.File) || strings.HasPrefix(p.File, "."):
		return "", Published{}, fmt.Errorf("%s names no image file it could have: %q", name, p.File)
	case len(p.Parts) == 0:
		return "", Published{}, fmt.Errorf("%s lists no parts of the image", name)
	}
	var total int64
	for i, part := range p.Parts {
		switch {
		case part.File != partFile(p.File, i):
			return "", Published{}, fmt.Errorf("%s lists part %d as %q, not %q", name, i, part.File, partFile(p.File, i))
		case sums[part.File] == "":
			return "", Published{}, fmt.Errorf("SHA256SUMS has no line for %s", part.File)
		case part.Size <= 0:
			return "", Published{}, fmt.Errorf("%s gives %s no size", name, part.File)
		}
		total += part.Size
	}
	if total != p.Size {
		return "", Published{}, fmt.Errorf("%s: the parts add up to %d bytes, not the image's %d", name, total, p.Size)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", Published{}, err
	}
	path := filepath.Join(dir, p.File)
	if err := fetchImage(ctx, client, url, path, p, sums, log); err != nil {
		return "", Published{}, err
	}
	return path, p, nil
}

// fetchSmall gets a small file whole, refusing one over a megabyte.
func fetchSmall(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	resp, err := get(ctx, client, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
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

func get(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("none is published for image version %s on %s (%s answered 404)", Version, runtime.GOARCH, url)
		}
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	return resp, nil
}

// fetchImage downloads the parts of p under url, appending each to path
// through a .part file, which it renames only once every part and the whole
// have the right sum. A part's size caps what it reads, so a wrong file can't
// fill the disk.
func fetchImage(ctx context.Context, client *http.Client, url, path string, p Published, sums map[string]string, log io.Writer) (err error) {
	part := path + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(part)
		}
	}()
	whole := sha256.New()
	prog := &progress{log: log, total: p.Size}
	for _, pt := range p.Parts {
		if err := fetchPart(ctx, client, url+pt.File, f, whole, prog, pt, sums[pt.File]); err != nil {
			return err
		}
	}
	if got := hex.EncodeToString(whole.Sum(nil)); got != p.SHA256 {
		return fmt.Errorf("%s doesn't match its SHA256SUMS line: its sum is %s, not %s", filepath.Base(path), got, p.SHA256)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(part, path)
}

func fetchPart(ctx context.Context, client *http.Client, url string, f io.Writer, whole io.Writer, prog *progress, pt Part, wantSum string) error {
	resp, err := get(ctx, client, url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, whole, h, prog), io.LimitReader(resp.Body, pt.Size+1))
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	if n != pt.Size {
		return fmt.Errorf("downloading %s: got %d bytes, not the %d image-<arch>.json says", url, n, pt.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSum || got != pt.SHA256 {
		return fmt.Errorf("%s doesn't match its SHA256SUMS line: its sum is %s, not %s", pt.File, got, wantSum)
	}
	return nil
}

// progress writes a line to the build log at every tenth of the download.
type progress struct {
	log         io.Writer
	total, done int64
	shown       int
}

func (p *progress) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.total > 0 {
		if pct := int(p.done * 100 / p.total); pct/progressEvery > p.shown/progressEvery && pct <= 100 {
			p.shown = pct
			_, _ = fmt.Fprintf(p.log, "%d%% of %d MB\n", pct/progressEvery*progressEvery, p.total>>20)
		}
	}
	return len(b), nil
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
// image with no components, stopped. The image is downloaded, checked and
// imported into Incus for as long as next is made from it, then deleted.
func fromPublished(ctx context.Context, inc incus.Client, next string, u User, opts Options, log io.Writer) error {
	step := stepper(log)
	step("Downloading the published base image %s for %s from %s", Version, runtime.GOARCH, opts.ImagesURL)
	file, p, err := download(ctx, opts.HTTP, opts.ImagesURL, opts.DownloadDir, log)
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
	plan := PlanFor(true, Installed{Version: Version, ToolsVersion: ToolsVersion(has), Tools: p.Tools}, Components{}, nil)
	step("Installing the agent tools it leaves out, as on a local build")
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
	// gzip rather than zstd or xz, though it's a little bigger: Incus unpacks
	// an image with its host's own tools, and only gzip is in every Debian.
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
	// Incus names it <fingerprint>.tar.gz; it keeps the extension.
	base := filepath.Base(exported[0])
	file := imageFile(runtime.GOARCH, base[strings.Index(base, "."):])
	step("Splitting it into parts of at most %d MiB", partSize>>20)
	parts, err := splitFile(exported[0], filepath.Join(dir, file), partSize)
	if err != nil {
		return err
	}
	return writePublished(dir, file, commit, tools, packages, parts, time.Now().UTC())
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
// over them and the parts. file is the image's name once joined, and parts
// are what it was split into, already in dir.
func writePublished(dir, file, commit string, tools []Tool, packages []debPackage, parts []Part, built time.Time) error {
	whole := sha256.New()
	var size int64
	for _, pt := range parts {
		f, err := os.Open(filepath.Join(dir, pt.File))
		if err != nil {
			return err
		}
		n, err := io.Copy(whole, f)
		_ = f.Close()
		if err != nil {
			return err
		}
		size += n
	}
	arch := runtime.GOARCH
	p := Published{
		Version: Version, Arch: arch, File: file, Size: size, SHA256: hex.EncodeToString(whole.Sum(nil)), Parts: parts,
		Tools: specsOf(tools), ToolsVersion: ToolsVersion(tools), Commit: commit, Built: built,
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
	for _, pt := range parts {
		sums += pt.SHA256 + "  " + pt.File + "\n"
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
	fmt.Fprintf(&b, "%s is Debian 13 with AgentBox's provision.sh applied", p.File)
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
