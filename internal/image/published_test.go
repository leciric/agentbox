package image_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"agentbox/internal/image"
)

// imageName is the joined image's name as CI publishes it.
var imageName = "agentbox-base-" + runtime.GOARCH + ".tar.gz"

// published serves image-<version>/ as CI publishes it, with the image's
// content in two parts and the tools the manifest lists; edit changes what is
// served after the sums are worked out, to break one.
type published struct {
	*httptest.Server
	files    map[string][]byte
	requests atomic.Int32
}

func sumLine(b []byte, name string) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]) + "  " + name + "\n"
}

func publish(t *testing.T, tools []string, edit func(files map[string][]byte)) *published {
	t.Helper()
	content := []byte("an incus image, as image export writes it")
	whole := sha256.Sum256(content)
	halves := [][]byte{content[:20], content[20:]}
	var parts []image.Part
	files := map[string][]byte{}
	var sums strings.Builder
	for i, half := range halves {
		name := fmt.Sprintf("%s.%03d", imageName, i)
		s := sha256.Sum256(half)
		parts = append(parts, image.Part{File: name, Size: int64(len(half)), SHA256: hex.EncodeToString(s[:])})
		files[name] = half
		sums.WriteString(sumLine(half, name))
	}
	manifest, err := json.Marshal(image.Published{
		Version: image.Version, Arch: runtime.GOARCH, File: imageName,
		Size: int64(len(content)), SHA256: hex.EncodeToString(whole[:]), Parts: parts, Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestName := "image-" + runtime.GOARCH + ".json"
	files[manifestName] = manifest
	sums.WriteString(sumLine(manifest, manifestName))
	// The other architecture's lines are in the same SHA256SUMS.
	sums.WriteString(sumLine([]byte("other"), "image-other.json"))
	files["SHA256SUMS"] = []byte(sums.String())
	if edit != nil {
		edit(files)
	}
	p := &published{files: files}
	prefix := "/image-" + image.Version + "/"
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests.Add(1)
		b, ok := files[strings.TrimPrefix(r.URL.Path, prefix)]
		if !ok || !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(p.Close)
	return p
}

func specs(tools []image.Tool) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Spec)
	}
	return out
}

func publishedOptions(t *testing.T, p *published) image.Options {
	return image.Options{ImagesURL: p.URL, DownloadDir: filepath.Join(t.TempDir(), "images"), HTTP: p.Client()}
}

func TestBuildFromThePublishedImage(t *testing.T) {
	p := publish(t, specs(image.PublishedTools()), nil)
	inc := fakeIncus(t, "")
	opts := publishedOptions(t, p)
	var sources []string
	opts.Source = func(s string) { sources = append(sources, s) }
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, opts, &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}
	if !slices.Equal(sources, []string{image.SourcePublished}) {
		t.Errorf("sources = %v", sources)
	}

	file := filepath.Join(opts.DownloadDir, imageName)
	imported := inc.ran(t, "image|import|"+file+"|--alias|agentbox-base-download|")
	created := inc.ran(t, "init|agentbox-base-download|agentbox-base-next|--profile|default|--profile|agentbox|")
	idmap := inc.ran(t, "raw.idmap=uid 1000 1000 gid 1000 1000")
	started := inc.ran(t, "start|agentbox-base-next")
	// The tools it leaves out are installed as the placeholder user, before
	// the image is snapshotted as generic and made this machine's, as on a
	// local build.
	installed := inc.ran(t, "/root/tools.sh|install|agent|/root/tools.list|")
	verified := inc.ran(t, "/root/tools.sh|verify|agent|/root/tools.current|")
	generic := inc.ran(t, "snapshot|create|agentbox-base-next|generic")
	personalised := inc.ran(t, "/root/personalise.sh|agent|dev|1000|1000")
	ready := inc.ran(t, "snapshot|create|agentbox-base-next|ready")
	if imported >= created || created >= idmap || idmap >= started || started >= installed || installed >= verified ||
		verified >= generic || generic >= personalised || personalised >= ready {
		t.Errorf("the build ran out of order:\n%s", strings.Join(inc.commands(t), "\n"))
	}
	if at(inc.commands(t)[imported:], "image|delete|agentbox-base-download|") < 0 {
		t.Errorf("the downloaded image should be deleted from Incus once next is made from it")
	}
	inc.neverRan(t, "/root/provision.sh")
	inc.neverRan(t, "init|"+image.Source)
	for _, name := range []string{"claude", "npm:@agentclientprotocol/claude-agent-acp", "npm:@cursor/sdk"} {
		if !strings.Contains(log.String(), image.Pin(name)) {
			t.Errorf("the build should install %s, which the published image leaves out:\n%s", image.Pin(name), log.String())
		}
	}
	if strings.Contains(log.String(), "Installing "+image.Pin("go")) {
		t.Errorf("Go is in the published image, at the pinned version, and shouldn't be installed again:\n%s", log.String())
	}
	// It records every tool tools.txt pins, as a local build does.
	inc.ran(t, "user.agentbox.tools-version="+image.ToolsVersion(image.ToolsFor(image.Components{}))+"|")
	if entries, _ := os.ReadDir(opts.DownloadDir); len(entries) != 0 {
		t.Errorf("the download should be gone once Incus has it, found %v", entries)
	}
}

// tools.txt moves on without a new image version, so the published image may
// have older tools than this AgentBox pins: those are installed too.
func TestBuildFromThePublishedImageMovesItsToolsOn(t *testing.T) {
	tools := specs(image.PublishedTools())
	for i, spec := range tools {
		if strings.HasPrefix(spec, "gh@") {
			tools[i] = "gh@1.0.0"
		}
	}
	p := publish(t, tools, nil)
	inc := fakeIncus(t, "")
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, publishedOptions(t, p), &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), image.Pin("gh")) {
		t.Errorf("the build should move gh on to %s:\n%s", image.Pin("gh"), log.String())
	}
	inc.neverRan(t, "/root/provision.sh")
}

// fellBack checks that Build built the image here after the published image
// failed, and said why.
func fellBack(t *testing.T, inc *fake, log, why string) {
	t.Helper()
	inc.ran(t, "init|"+image.Source+"|agentbox-base-next")
	inc.ran(t, "/root/provision.sh|agent|1000|1000")
	inc.ran(t, "rename|agentbox-base-next|agentbox-base")
	if !strings.Contains(log, "Building it here instead") || !strings.Contains(log, why) {
		t.Errorf("the log should say the published image failed (%q) and that it builds here instead:\n%s", why, log)
	}
}

func TestBuildFallsBackWhenNothingIsPublished(t *testing.T) {
	p := publish(t, nil, func(files map[string][]byte) { clear(files) })
	inc := fakeIncus(t, "")
	opts := publishedOptions(t, p)
	var sources []string
	opts.Source = func(s string) { sources = append(sources, s) }
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, opts, &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}
	if !slices.Equal(sources, []string{image.SourcePublished, image.SourceLocalAfterPublished}) {
		t.Errorf("sources = %v", sources)
	}
	fellBack(t, inc, log.String(), "404")
	inc.neverRan(t, "image|import")
}

func TestBuildFallsBackWhenTheImageDoesntMatchItsSum(t *testing.T) {
	p := publish(t, specs(image.PublishedTools()), func(files map[string][]byte) {
		files[imageName+".001"] = []byte("mage export writes iT")
	})
	inc := fakeIncus(t, "")
	opts := publishedOptions(t, p)
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, opts, &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}
	fellBack(t, inc, log.String(), "doesn't match its SHA256SUMS line")
	inc.neverRan(t, "image|import")
	if entries, _ := os.ReadDir(opts.DownloadDir); len(entries) != 0 {
		t.Errorf("a download that doesn't match should be deleted, found %v", entries)
	}
}

func TestBuildFallsBackWhenImageJSONDoesntMatchItsSum(t *testing.T) {
	p := publish(t, specs(image.PublishedTools()), func(files map[string][]byte) {
		name := "image-" + runtime.GOARCH + ".json"
		files[name] = bytes.Replace(files[name], []byte(`"tools":[`), []byte(`"tools":["evil@1",`), 1)
	})
	inc := fakeIncus(t, "")
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, publishedOptions(t, p), &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}
	fellBack(t, inc, log.String(), "image-"+runtime.GOARCH+".json doesn't match")
}

func TestBuildFallsBackWhenAPartIsMissingOrTruncated(t *testing.T) {
	for name, edit := range map[string]func(map[string][]byte){
		"missing":   func(files map[string][]byte) { delete(files, imageName+".001") },
		"truncated": func(files map[string][]byte) { files[imageName+".001"] = files[imageName+".001"][:5] },
		"longer":    func(files map[string][]byte) { files[imageName+".001"] = append(files[imageName+".001"], '!') },
	} {
		t.Run(name, func(t *testing.T) {
			p := publish(t, specs(image.PublishedTools()), edit)
			inc := fakeIncus(t, "")
			opts := publishedOptions(t, p)
			var log bytes.Buffer
			if err := image.Build(t.Context(), inc.Client, host, opts, &log); err != nil {
				t.Fatalf("Build() = %v\n%s", err, log.String())
			}
			fellBack(t, inc, log.String(), "Building it here instead")
			inc.neverRan(t, "image|import")
			if entries, _ := os.ReadDir(opts.DownloadDir); len(entries) != 0 {
				t.Errorf("a failed download should leave nothing, found %v", entries)
			}
		})
	}
}

// Parts that each match their sum but join into something else than the
// manifest says (swapped, say) are refused by the whole's sum.
func TestBuildFallsBackWhenThePartsJoinIntoTheWrongImage(t *testing.T) {
	p := publish(t, specs(image.PublishedTools()), func(files map[string][]byte) {
		var m image.Published
		if err := json.Unmarshal(files["image-"+runtime.GOARCH+".json"], &m); err != nil {
			t.Fatal(err)
		}
		m.SHA256 = strings.Repeat("0", 64)
		files["image-"+runtime.GOARCH+".json"], _ = json.Marshal(m)
		files["SHA256SUMS"] = append(files["SHA256SUMS"], sumLine(files["image-"+runtime.GOARCH+".json"], "image-"+runtime.GOARCH+".json")...)
	})
	inc := fakeIncus(t, "")
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, publishedOptions(t, p), &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}
	fellBack(t, inc, log.String(), "doesn't match its SHA256SUMS line")
}

func TestBuildJoinsThePartsInOrder(t *testing.T) {
	p := publish(t, specs(image.PublishedTools()), nil)
	inc := fakeIncus(t, `  image) [ "$2" = import ] && cp "$3" "$TMPDIR/imported" ;;`)
	t.Setenv("TMPDIR", t.TempDir())
	if err := image.Build(t.Context(), inc.Client, host, publishedOptions(t, p), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(os.Getenv("TMPDIR"), "imported"))
	if err != nil || string(got) != "an incus image, as image export writes it" {
		t.Errorf("the imported image = %q, %v", got, err)
	}
}

func TestBuildFallsBackWhenTheImportFails(t *testing.T) {
	p := publish(t, specs(image.PublishedTools()), nil)
	inc := fakeIncus(t, `  image) [ "$2" = import ] && { echo 'Error: not an image' >&2; exit 1; } ;;`)
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, publishedOptions(t, p), &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}
	fellBack(t, inc, log.String(), "not an image")
}

// A tool that fails to install on the published image (Claude Code's registry
// down, say) leaves no half-made next behind: the local build starts clean.
func TestBuildFallsBackWhenTheToolsFail(t *testing.T) {
	p := publish(t, specs(image.PublishedTools()), nil)
	inc := fakeIncus(t, `  exec) case "$*" in *tools.sh*install*) exit 1 ;; esac ;;`)
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, publishedOptions(t, p), &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}
	fellBack(t, inc, log.String(), "tools.sh install")
	commands := inc.commands(t)
	imported, local := at(commands, "image|import"), at(commands, "init|"+image.Source)
	if imported < 0 || local < imported || at(commands[imported:local], "delete|--force|agentbox-base-next") < 0 {
		t.Errorf("next should be deleted before the local build makes it again:\n%s", strings.Join(commands, "\n"))
	}
}

// Codex and OpenCode are only tools, so a build with them downloads the
// published image as one without does, and installs their tools on top with
// the ones it leaves out, at their pins, into home directories of the user's.
func TestBuildWithCodexOrOpenCodeAddsThemToThePublishedImage(t *testing.T) {
	for name, c := range map[string]image.Components{
		"codex":    {Codex: true},
		"opencode": {OpenCode: true},
		"both":     {Codex: true, OpenCode: true},
	} {
		t.Run(name, func(t *testing.T) {
			p := publish(t, specs(image.PublishedTools()), nil)
			inc := fakeIncus(t, "")
			opts := publishedOptions(t, p)
			opts.Components = c
			var log bytes.Buffer
			if err := image.Build(t.Context(), inc.Client, host, opts, &log); err != nil {
				t.Fatalf("Build() = %v\n%s", err, log.String())
			}
			inc.ran(t, "image|import|")
			inc.neverRan(t, "/root/provision.sh")
			inc.neverRan(t, "init|"+image.Source)
			dirs := inc.ran(t, "runuser|-l|agent|-c|mkdir -p ")
			installed := inc.ran(t, "/root/tools.sh|install|agent|/root/tools.list|")
			if dirs >= installed {
				t.Errorf("the home directories should be made before the tools.sh run that cleans up:\n%s", strings.Join(inc.commands(t), "\n"))
			}
			for _, tool := range image.ToolsFor(c) {
				if tool.Option != "" && !strings.Contains(log.String(), tool.Spec) {
					t.Errorf("the build should install %s:\n%s", tool.Spec, log.String())
				}
			}
			// It records the components and their tools, as a local build
			// does, so PlanFor finds it up to date.
			inc.ran(t, "user.agentbox.with-codex="+map[bool]string{true: "1", false: "0"}[c.Codex]+"|user.agentbox.with-opencode="+map[bool]string{true: "1", false: "0"}[c.OpenCode]+"|")
			inc.ran(t, "user.agentbox.tools-version="+image.ToolsVersion(image.ToolsFor(c))+"|")
		})
	}
}

// No published image has the components provision.sh puts in, so a build with
// one, or one asked to be local, doesn't look, even with Codex on too.
func TestBuildWithAComponentOrLocalBuildsHere(t *testing.T) {
	p := publish(t, specs(image.PublishedTools()), nil)
	for name, opts := range map[string]image.Options{
		"dev-caches": func() image.Options { o := publishedOptions(t, p); o.Components.DevCaches = true; return o }(),
		"incus and codex": func() image.Options {
			o := publishedOptions(t, p)
			o.Components.Incus, o.Components.Codex = true, true
			return o
		}(),
		"local": {},
	} {
		t.Run(name, func(t *testing.T) {
			inc := fakeIncus(t, "")
			if err := image.Build(t.Context(), inc.Client, host, opts, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			inc.ran(t, "/root/provision.sh|")
			inc.neverRan(t, "image|import")
		})
	}
	if n := p.requests.Load(); n != 0 {
		t.Errorf("nothing should have been downloaded, but %d requests were made", n)
	}
}

func TestBuildCancelledDuringTheDownloadDoesntBuildHere(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}))
	defer srv.Close()
	inc := fakeIncus(t, "")
	err := image.Build(ctx, inc.Client, host, image.Options{ImagesURL: srv.URL, DownloadDir: t.TempDir(), HTTP: srv.Client()}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("a cancelled build should fail")
	}
	inc.neverRan(t, "/root/provision.sh")
}

func TestImagesURL(t *testing.T) {
	for env, want := range map[string]string{
		"":                           image.DefaultImagesURL,
		"off":                        "",
		"https://mirror.example/im/": "https://mirror.example/im",
	} {
		t.Setenv("AGENTBOX_IMAGES_URL", env)
		if got := image.ImagesURL(); got != want {
			t.Errorf("ImagesURL() with AGENTBOX_IMAGES_URL=%q = %q, want %q", env, got, want)
		}
	}
}

// BuildForPublishing builds with only the tools AgentBox may publish, and
// writes the image with everything that goes next to it.
func TestBuildForPublishing(t *testing.T) {
	inc := fakeIncus(t, `  list) echo '[{"name":"agentbox-base-publish","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]' ;;
  file) [ "$2" = pull ] && printf 'Package: bash\nStatus: install ok installed\nArchitecture: amd64\nVersion: 5.2.37-2\n\nPackage: libc6\nStatus: install ok installed\nSource: glibc\nVersion: 2.41-12\n' >"$4" ;;
  image) [ "$2" = export ] && printf 'the image' >"$4/0123abcd.tar.gz" ;;`)
	defer image.SetPartSize(4)()
	dir := filepath.Join(t.TempDir(), "out")
	var log bytes.Buffer
	if err := image.BuildForPublishing(t.Context(), inc.Client, host, dir, "abc123", &log); err != nil {
		t.Fatalf("BuildForPublishing() = %v\n%s", err, log.String())
	}
	provisioned := inc.ran(t, "/root/provision.sh|agent|1000|1000")
	published := inc.ran(t, "publish|agentbox-base-publish|--alias|agentbox-base-publish|--compression|gzip|")
	inc.ran(t, "image|export|agentbox-base-publish|")
	if provisioned > published {
		t.Error("the image should be published after it is provisioned")
	}
	// Nothing is left in Incus.
	inc.ran(t, "delete|--force|agentbox-base-publish")
	inc.ran(t, "image|delete|agentbox-base-publish|")
	inc.neverRan(t, "personalise.sh")

	manifestName := "image-" + runtime.GOARCH + ".json"
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	var p image.Published
	if err := json.Unmarshal([]byte(read(manifestName)), &p); err != nil {
		t.Fatal(err)
	}
	if p.File != imageName || len(p.Parts) != 3 || p.Version != image.Version || p.Commit != "abc123" || p.Size != int64(len("the image")) {
		t.Errorf("image.json = %+v", p)
	}
	for _, spec := range p.Tools {
		for _, name := range []string{"claude@", "npm:@agentclientprotocol/claude-agent-acp@", "npm:@cursor/sdk@"} {
			if strings.HasPrefix(spec, name) {
				t.Errorf("the published image has %s, which AgentBox may not redistribute", spec)
			}
		}
	}
	if !strings.Contains(read("packages-"+runtime.GOARCH+".txt"), "libc6\t2.41-12\t\tglibc\t2.41-12") {
		t.Errorf("packages.txt:\n%s", read("packages-"+runtime.GOARCH+".txt"))
	}
	if !strings.Contains(read("SOURCES-"+runtime.GOARCH+".md"), "https://snapshot.debian.org/package/glibc/2.41-12/") {
		t.Errorf("SOURCES.md:\n%s", read("SOURCES-"+runtime.GOARCH+".md"))
	}
	// "the image" in parts of four bytes, which join into it.
	var joined []byte
	for i, part := range p.Parts {
		if part.File != fmt.Sprintf("%s.%03d", imageName, i) {
			t.Errorf("part %d is %s", i, part.File)
		}
		joined = append(joined, read(part.File)...)
	}
	whole := sha256.Sum256(joined)
	if string(joined) != "the image" || p.SHA256 != hex.EncodeToString(whole[:]) {
		t.Errorf("the parts join into %q, with sum %s in image.json", joined, p.SHA256)
	}
	// SHA256SUMS covers the parts and what goes with them, in sha256sum's format.
	sums := read("SHA256SUMS")
	names := []string{manifestName, "packages-" + runtime.GOARCH + ".txt", "SOURCES-" + runtime.GOARCH + ".md"}
	for _, part := range p.Parts {
		names = append(names, part.File)
	}
	for _, name := range names {
		sum := sha256.Sum256([]byte(read(name)))
		if !strings.Contains(sums, hex.EncodeToString(sum[:])+"  "+name+"\n") {
			t.Errorf("SHA256SUMS has no right line for %s:\n%s", name, sums)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != len(names)+1 {
		t.Errorf("dir should hold %d files and nothing else, found %v", len(names)+1, entries)
	}
}
