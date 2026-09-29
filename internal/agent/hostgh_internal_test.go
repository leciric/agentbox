package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/paths"
)

// ghRelease is a stand-in for GitHub's release downloads: the tarball for this
// machine, laid out the way the real one is, and the checksums file beside it.
type ghRelease struct {
	*httptest.Server
	binary []byte // the gh inside the tarball
}

func newGHRelease(t *testing.T, tamper bool) *ghRelease {
	t.Helper()
	asset, err := ghAsset()
	if err != nil {
		t.Skip(err)
	}
	// Random, so it doesn't compress, and big enough for progress to show.
	binary := make([]byte, 5<<19)
	if _, err := rand.Read(binary); err != nil {
		t.Fatal(err)
	}
	binary = append([]byte("#!/bin/sh\necho gh version "+ghVersion+"\nexit 0\n"), binary...)
	top := strings.TrimSuffix(asset, ".tar.gz")
	var tarball bytes.Buffer
	gz := gzip.NewWriter(&tarball)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		body []byte
	}{
		{top + "/LICENSE", []byte("MIT")},
		{top + "/share/man/man1/gh.1", []byte(".TH GH 1")},
		{top + "/bin/gh", binary},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(tarball.Bytes())
	if tamper {
		sum[0] ^= 0xff
	}
	checksums := fmt.Sprintf("%x  gh_%s_windows_amd64.zip\n%s  %s\n", sha256.Sum256(nil), ghVersion, hex.EncodeToString(sum[:]), asset)

	mux := http.NewServeMux()
	mux.HandleFunc("/v"+ghVersion+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
		// As GitHub does, so the progress can say how much is left.
		w.Header().Set("Content-Length", fmt.Sprint(tarball.Len()))
		_, _ = w.Write(tarball.Bytes())
	})
	mux.HandleFunc(fmt.Sprintf("/v%s/gh_%s_checksums.txt", ghVersion, ghVersion), func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(checksums))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &ghRelease{Server: srv, binary: binary}
}

// statusLog records what the chat's status line was told.
type statusLog struct {
	mu    sync.Mutex
	lines []string
}

func (s *statusLog) set(detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, detail)
}

// noGHOnPath leaves the test a PATH with no gh on it, whatever the machine has.
func noGHOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
}

// In VM mode the host has no gh, so the lead gets AgentBox's own: the release
// tarball for this machine, checked against the release's checksums, with only
// bin/gh kept, and whatever an older pin left behind cleared out.
func TestEnsureGHInstallsThePinnedRelease(t *testing.T) {
	noGHOnPath(t)
	release := newGHRelease(t, false)
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}, ghReleases: release.URL}
	old := filepath.Join(filepath.Dir(m.ghDir()), "2.0.0", "bin", "gh")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var status statusLog
	got, err := m.ensureGH(context.Background(), status.set)
	if err != nil {
		t.Fatal(err)
	}
	if got != m.ghPath() {
		t.Errorf("ensureGH() = %q, want AgentBox's own %q", got, m.ghPath())
	}
	if !strings.Contains(got, ghVersion) {
		t.Errorf("ensureGH() = %q, want it under the pinned version %s", got, ghVersion)
	}
	if !usable(got) {
		t.Fatalf("%s isn't an executable", got)
	}
	if body, _ := os.ReadFile(got); !bytes.Equal(body, release.binary) {
		t.Error("the installed gh isn't the tarball's bin/gh")
	}
	entries, _ := os.ReadDir(filepath.Dir(m.ghDir()))
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if !slices.Equal(left, []string{ghVersion}) {
		t.Errorf("the tools' gh directory holds %v, want only %s: no older version, download or staging left", left, ghVersion)
	}
	if _, err := os.Stat(filepath.Join(m.ghDir(), "share")); err == nil {
		t.Error("the manual was unpacked too; only bin/gh is kept")
	}
	// The status line says what it is doing, and moves while it downloads.
	if len(status.lines) < 3 || !strings.Contains(status.lines[0], "Downloading the GitHub CLI") {
		t.Fatalf("status = %q, want the download announced and then its progress", status.lines)
	}
	// About 2.5 MiB, so two MiB are reported of three.
	if last := status.lines[len(status.lines)-1]; !strings.HasSuffix(last, ": 2 of 3 MiB") {
		t.Errorf("last status = %q, want the download's progress in MiB", last)
	}

	// Once there, it's used as it is, with nothing downloaded again.
	release.Close()
	status = statusLog{}
	if again, err := m.ensureGH(context.Background(), status.set); err != nil || again != got {
		t.Errorf("ensureGH() the second time = %q, %v, want %q with no download", again, err, got)
	}
	if len(status.lines) != 0 {
		t.Errorf("status = %q the second time, want nothing: it's already installed", status.lines)
	}
}

// A tarball that isn't the one the release's checksums name is refused, and
// nothing of it is left where the lead would find it.
func TestEnsureGHRefusesATarballItsChecksumsDontMatch(t *testing.T) {
	noGHOnPath(t)
	release := newGHRelease(t, true)
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}, ghReleases: release.URL}
	_, err := m.ensureGH(context.Background(), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("ensureGH() with a tampered tarball = %v, want a checksum error", err)
	}
	if _, err := os.Stat(m.ghPath()); err == nil {
		t.Error("a gh that failed its checksum was installed anyway")
	}
	entries, _ := os.ReadDir(filepath.Dir(m.ghDir()))
	if len(entries) != 0 {
		t.Errorf("a failed install left %d entries behind", len(entries))
	}
}

// A gh the host already has is fine: nothing is downloaded for it.
func TestEnsureGHUsesTheHostsOwn(t *testing.T) {
	bin := t.TempDir()
	host := filepath.Join(bin, "gh")
	if err := os.WriteFile(host, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	// Nowhere to download from: using the host's gh must not need it.
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}, ghReleases: "http://127.0.0.1:1"}
	got, err := m.ensureGH(context.Background(), func(string) {})
	if err != nil || got != host {
		t.Errorf("ensureGH() = %q, %v, want the host's %q", got, err, host)
	}
}

// stubLeadTools gives m a Claude Code and an adapter at the pinned version, so
// hostTools has nothing but gh to install.
func stubLeadTools(t *testing.T, m *Manager) {
	t.Helper()
	pkg := strings.TrimPrefix(ChatAdapters["claude"].Package, "npm:")
	name := pkg[:strings.LastIndex(pkg, "@")]
	manifest := filepath.Join(m.toolsHome(), "node_modules", name, "package.json")
	for path, body := range map[string]string{
		m.claudePath():  "#!/bin/sh\nexit 0\n",
		m.adapterPath(): "#!/bin/sh\nexit 0\n",
		manifest:        fmt.Sprintf(`{"name":%q,"version":%q}`, name, pinnedVersion(pkg)),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// The lead finds gh on its PATH: AgentBox's own, installed on first use.
func TestHostToolsPutGHOnTheLeadsPath(t *testing.T) {
	noGHOnPath(t)
	release := newGHRelease(t, false)
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}, ghReleases: release.URL}
	stubLeadTools(t, m)
	tools, err := m.hostTools(context.Background(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if dirs := filepath.SplitList(tools.Bin); !slices.Contains(dirs, filepath.Dir(m.ghPath())) {
		t.Errorf("the lead's PATH additions %v don't include gh's %s", dirs, filepath.Dir(m.ghPath()))
	}
}

// Without gh the chat still works (git reaches GitHub through its credential
// helper), so a failed install is logged and the chat starts anyway.
func TestHostToolsStartTheChatWhenGHFailsToInstall(t *testing.T) {
	noGHOnPath(t)
	missing := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(missing.Close)
	var log bytes.Buffer
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}, ghReleases: missing.URL, Log: &log}
	stubLeadTools(t, m)
	tools, err := m.hostTools(context.Background(), func(string) {})
	if err != nil {
		t.Fatalf("hostTools() = %v: a failed gh install must not stop the chat", err)
	}
	if tools.Adapter != m.adapterPath() {
		t.Errorf("Adapter = %q, want %q", tools.Adapter, m.adapterPath())
	}
	if strings.Contains(tools.Bin, filepath.Dir(m.ghDir())) {
		t.Errorf("PATH additions %q name a gh that failed to install", tools.Bin)
	}
	if !strings.Contains(log.String(), "without the GitHub CLI") || !strings.Contains(log.String(), "404") {
		t.Errorf("log = %q, want the failed install and why", log.String())
	}
}
