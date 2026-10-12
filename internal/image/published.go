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
// It lives in the bucket app releases are downloaded from, one per image
// version and architecture:
//
//	images/<version>/<arch>/agentbox-base.tar.gz  the image, as `incus image export` writes it
//	images/<version>/<arch>/image.json             what it is (Published)
//	images/<version>/<arch>/packages.txt           its Debian packages, with their versions
//	images/<version>/<arch>/SOURCES.md             where their sources are, and the tools' licenses
//	images/<version>/<arch>/SHA256SUMS             the sums of the four above
//	images/index.json                              every image there, for people and scripts
//
// The bucket keeps the newest image version only (scripts/r2-images.sh): an
// AgentBox whose version is gone falls back to building its own, as one does
// when the download fails or a component is on.

// DefaultImagesURL is where published images are, unless the daemon's
// AGENTBOX_IMAGES_URL says otherwise.
const DefaultImagesURL = "https://downloads.agentbox.linting.dev/images"

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
// where their source is: SOURCES.md lists them. A tool tools.txt starts
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

// Published is image.json: what a published image is.
type Published struct {
	// Version is the image version it was built for, and Arch Go's name for
	// its architecture.
	Version string `json:"version"`
	Arch    string `json:"arch"`
	// File is the image's name next to image.json, and Size and SHA256 its
	// length and sum (SHA256SUMS has the sum too, and is what's checked).
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// Tools are the specs of the agent tools in it, like "go@1.27.2": tools.txt
	// may have moved on since, and a machine that downloads it installs
	// whatever differs.
	Tools        []string `json:"tools"`
	ToolsVersion string   `json:"toolsVersion"`
	// Commit is the AgentBox commit that built it, and Built when.
	Commit string    `json:"commit,omitempty"`
	Built  time.Time `json:"built"`
}

// progressEvery is how often, in percent, the download says how far it got.
const progressEvery = 10

// download fetches the published image for this image version and this
// machine's architecture into dir, and checks it against SHA256SUMS: the
// image file's path, and what image.json says it is. A missing image, a
// failed download or a sum that doesn't match is an error, and leaves no file.
func download(ctx context.Context, client *http.Client, base, dir string, log io.Writer) (string, Published, error) {
	if client == nil {
		client = http.DefaultClient
	}
	url := strings.TrimRight(base, "/") + "/" + Version + "/" + runtime.GOARCH + "/"
	sumsFile, err := fetchSmall(ctx, client, url+"SHA256SUMS")
	if err != nil {
		return "", Published{}, err
	}
	sums := parseSums(sumsFile)
	manifest, err := fetchSmall(ctx, client, url+"image.json")
	if err != nil {
		return "", Published{}, err
	}
	if got := sha256Hex(manifest); got != sums["image.json"] {
		return "", Published{}, fmt.Errorf("image.json doesn't match its SHA256SUMS line")
	}
	var p Published
	if err := json.Unmarshal(manifest, &p); err != nil {
		return "", Published{}, fmt.Errorf("reading image.json: %w", err)
	}
	switch {
	case p.Version != Version || p.Arch != runtime.GOARCH:
		return "", Published{}, fmt.Errorf("image.json is for version %s on %s, not %s on %s", p.Version, p.Arch, Version, runtime.GOARCH)
	case p.File == "" || p.File != filepath.Base(p.File) || strings.HasPrefix(p.File, "."):
		return "", Published{}, fmt.Errorf("image.json names no image file it could have: %q", p.File)
	case sums[p.File] == "":
		return "", Published{}, fmt.Errorf("SHA256SUMS has no line for %s", p.File)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", Published{}, err
	}
	path := filepath.Join(dir, p.File)
	if err := fetchFile(ctx, client, url+p.File, path, sums[p.File], p.Size, log); err != nil {
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

// fetchFile downloads url to path through a .part file, which it renames only
// once the sum is right. size, when known, caps what it reads, so a wrong
// file can't fill the disk.
func fetchFile(ctx context.Context, client *http.Client, url, path, wantSum string, size int64, log io.Writer) (err error) {
	resp, err := get(ctx, client, url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	total := size
	if total <= 0 {
		total = resp.ContentLength
	}
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
	var body io.Reader = resp.Body
	if size > 0 {
		body = io.LimitReader(resp.Body, size+1)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h, &progress{log: log, total: total}), body)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	if size > 0 && n != size {
		return fmt.Errorf("downloading %s: got %d bytes, not the %d image.json says", url, n, size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSum {
		return fmt.Errorf("%s doesn't match its SHA256SUMS line: its sum is %s, not %s", filepath.Base(path), got, wantSum)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(part, path)
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
	plan := PlanFor(true, Installed{Version: Version, ToolsVersion: ToolsVersion(has), Tools: p.Tools}, Components{})
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
// nothing behind in Incus. commit is AgentBox's commit, for image.json.
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
	file := "agentbox-base" + base[strings.Index(base, "."):]
	if err := os.Rename(exported[0], filepath.Join(dir, file)); err != nil {
		return err
	}
	return writePublished(dir, file, commit, tools, packages, time.Now().UTC())
}

// writePublished writes the files that go next to the image file in dir:
// image.json, packages.txt, SOURCES.md and SHA256SUMS over all four.
func writePublished(dir, file, commit string, tools []Tool, packages []debPackage, built time.Time) error {
	sum, size, err := sumFile(filepath.Join(dir, file))
	if err != nil {
		return err
	}
	p := Published{
		Version: Version, Arch: runtime.GOARCH, File: file, Size: size, SHA256: sum,
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
		{"image.json", append(manifest, '\n')},
		{"packages.txt", packageList(packages)},
		{"SOURCES.md", sourcesNotice(p, tools, packages)},
	}
	sums := sum + "  " + file + "\n"
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.content, 0o644); err != nil {
			return err
		}
		sums += sha256Hex(f.content) + "  " + f.name + "\n"
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o644)
}

func sumFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
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

// packageList is packages.txt: one installed package a line, tab-separated,
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

// sourcesNotice is SOURCES.md: what the image holds, under which licenses, and
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
	b.WriteString("packages.txt lists every installed package with its exact version and the source package\n" +
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
