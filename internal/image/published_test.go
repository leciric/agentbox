package image_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"agentbox/internal/image"
)

// published serves images/<version>/<arch>/ as CI publishes it, with the
// image file's content and the tools image.json lists; edit changes what is
// served after the sums are worked out, to break one.
type published struct {
	*httptest.Server
	files    map[string][]byte
	requests atomic.Int32
}

func publish(t *testing.T, tools []string, edit func(files map[string][]byte)) *published {
	t.Helper()
	content := []byte("an incus image, as image export writes it")
	sum := sha256.Sum256(content)
	manifest, err := json.Marshal(image.Published{
		Version: image.Version, Arch: runtime.GOARCH, File: "agentbox-base.tar.gz",
		Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:]), Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"agentbox-base.tar.gz": content, "image.json": manifest}
	var sums strings.Builder
	for _, name := range []string{"agentbox-base.tar.gz", "image.json"} {
		s := sha256.Sum256(files[name])
		sums.WriteString(hex.EncodeToString(s[:]) + "  " + name + "\n")
	}
	files["SHA256SUMS"] = []byte(sums.String())
	if edit != nil {
		edit(files)
	}
	p := &published{files: files}
	prefix := "/images/" + image.Version + "/" + runtime.GOARCH + "/"
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
	return image.Options{ImagesURL: p.URL + "/images", DownloadDir: filepath.Join(t.TempDir(), "images"), HTTP: p.Client()}
}

func TestBuildFromThePublishedImage(t *testing.T) {
	p := publish(t, specs(image.PublishedTools()), nil)
	inc := fakeIncus(t, "")
	opts := publishedOptions(t, p)
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, opts, &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}

	file := filepath.Join(opts.DownloadDir, "agentbox-base.tar.gz")
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
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, publishedOptions(t, p), &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}
	fellBack(t, inc, log.String(), "404")
	inc.neverRan(t, "image|import")
}

func TestBuildFallsBackWhenTheImageDoesntMatchItsSum(t *testing.T) {
	p := publish(t, specs(image.PublishedTools()), func(files map[string][]byte) {
		files["agentbox-base.tar.gz"] = []byte("an incus image, as image export writes iT")
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
		files["image.json"] = bytes.Replace(files["image.json"], []byte(`"tools":[`), []byte(`"tools":["evil@1",`), 1)
	})
	inc := fakeIncus(t, "")
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, publishedOptions(t, p), &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}
	fellBack(t, inc, log.String(), "image.json doesn't match")
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

// No published image has an optional component, so a build with one, or one
// asked to be local, doesn't look.
func TestBuildWithAComponentOrLocalBuildsHere(t *testing.T) {
	p := publish(t, specs(image.PublishedTools()), nil)
	for name, opts := range map[string]image.Options{
		"codex": func() image.Options { o := publishedOptions(t, p); o.Components.Codex = true; return o }(),
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

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	var p image.Published
	if err := json.Unmarshal([]byte(read("image.json")), &p); err != nil {
		t.Fatal(err)
	}
	if p.File != "agentbox-base.tar.gz" || p.Version != image.Version || p.Commit != "abc123" || p.Size != int64(len("the image")) {
		t.Errorf("image.json = %+v", p)
	}
	for _, spec := range p.Tools {
		for _, name := range []string{"claude@", "npm:@agentclientprotocol/claude-agent-acp@", "npm:@cursor/sdk@"} {
			if strings.HasPrefix(spec, name) {
				t.Errorf("the published image has %s, which AgentBox may not redistribute", spec)
			}
		}
	}
	if !strings.Contains(read("packages.txt"), "libc6\t2.41-12\t\tglibc\t2.41-12") {
		t.Errorf("packages.txt:\n%s", read("packages.txt"))
	}
	if !strings.Contains(read("SOURCES.md"), "https://snapshot.debian.org/package/glibc/2.41-12/") {
		t.Errorf("SOURCES.md:\n%s", read("SOURCES.md"))
	}
	// SHA256SUMS covers the image and what goes with it, in sha256sum's format.
	sums := read("SHA256SUMS")
	for _, name := range []string{"agentbox-base.tar.gz", "image.json", "packages.txt", "SOURCES.md"} {
		sum := sha256.Sum256([]byte(read(name)))
		if !strings.Contains(sums, hex.EncodeToString(sum[:])+"  "+name+"\n") {
			t.Errorf("SHA256SUMS has no right line for %s:\n%s", name, sums)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 5 {
		t.Errorf("dir should hold the five files and nothing else, found %v", entries)
	}
}
