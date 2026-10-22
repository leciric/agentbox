package nodemods

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func reader(files map[string]string) func(string) ([]byte, error) {
	return func(rel string) ([]byte, error) {
		if s, ok := files[rel]; ok {
			return []byte(s), nil
		}
		return nil, fs.ErrNotExist
	}
}

func TestPlanThisRepository(t *testing.T) {
	// The layout of AgentBox's own repository: two apps with lockfiles, a
	// script with pnpm's, and a fixture no lockfile covers.
	files := []string{
		"desktop/package.json", "desktop/package-lock.json",
		"mobile/package.json", "mobile/package-lock.json",
		"scripts/record/package.json", "scripts/record/pnpm-lock.yaml",
		"testdata/fixtures/hello-stack/package.json",
		"go.mod",
	}
	got := Plan(files, reader(nil))
	want := []Group{
		{Lockfile: "desktop/package-lock.json", Dirs: []string{"desktop"}},
		{Lockfile: "mobile/package-lock.json", Dirs: []string{"mobile"}},
		{Lockfile: "scripts/record/pnpm-lock.yaml", Dirs: []string{"scripts/record"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Plan = %+v, want %+v", got, want)
	}
}

func TestPlanWorkspaces(t *testing.T) {
	files := []string{
		"package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml",
		"apps/web/package.json", "apps/api/package.json",
		"packages/@scope/ui/package.json", "packages/legacy/package.json",
		"packages/ui/test/fixture/package.json",
		"examples/demo/package.json",
	}
	read := reader(map[string]string{
		"package.json":        `{"name":"root"}`,
		"pnpm-workspace.yaml": "packages:\n  - 'apps/*'\n  - \"packages/**\" # all\n  - '!packages/legacy'\n  - '!**/test/**'\nonlyBuiltDependencies:\n  - esbuild\n",
	})
	got := Plan(files, read)
	want := []Group{{Lockfile: "pnpm-lock.yaml", Dirs: []string{".", "apps/api", "apps/web", "packages/@scope/ui"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Plan = %+v, want %+v", got, want)
	}

	// npm's and Yarn's workspaces, in both forms.
	for _, ws := range []string{`["libs/*"]`, `{"packages":["libs/*"]}`} {
		got := Plan([]string{"package.json", "yarn.lock", "libs/a/package.json", "other/package.json"},
			reader(map[string]string{"package.json": `{"workspaces":` + ws + `}`}))
		want := []Group{{Lockfile: "yarn.lock", Dirs: []string{".", "libs/a"}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("workspaces %s: Plan = %+v, want %+v", ws, got, want)
		}
	}
}

func TestPlanNestedLockfileIsItsOwnGroup(t *testing.T) {
	files := []string{"package.json", "package-lock.json", "tools/x/package.json", "tools/x/package-lock.json"}
	got := Plan(files, reader(map[string]string{"package.json": `{"workspaces":["tools/*"]}`}))
	want := []Group{
		{Lockfile: "package-lock.json", Dirs: []string{"."}},
		{Lockfile: "tools/x/package-lock.json", Dirs: []string{"tools/x"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Plan = %+v, want %+v", got, want)
	}
}

func TestPlanCapsMounts(t *testing.T) {
	files := []string{"package.json", "pnpm-lock.yaml"}
	for i := range 50 {
		files = append(files, fmt.Sprintf("p/%02d/package.json", i), fmt.Sprintf("p/%02d/deep/x/package.json", i))
	}
	got := Plan(files, reader(map[string]string{"package.json": `{"workspaces":["p/**"]}`}))
	if len(got) != 1 || len(got[0].Dirs) != MaxMounts || got[0].Dirs[0] != "." || got[0].Dirs[1] != "p/00" || got[0].Dirs[MaxMounts-1] != "p/30" {
		t.Fatalf("Plan kept %d dirs: %v", len(got[0].Dirs), got[0].Dirs)
	}
}

func write(t *testing.T, path, content string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mod.IsZero() {
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCaptureThenRestore(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	g := Group{Lockfile: "package-lock.json", Dirs: []string{".", "libs/a"}}
	lockTime := time.Now().Add(-time.Hour)
	in := Install{Group: g, Key: Key(g, []byte("lock"), "137"), LockTime: lockTime}

	if restored, err := s.Prepare("ab-p-agent-1", "p", []Install{in}); err != nil || len(restored) != 0 {
		t.Fatalf("Prepare with no snapshot = %v, %v", restored, err)
	}
	root := s.MountDir("ab-p-agent-1", ".")
	write(t, filepath.Join(root, ".package-lock.json"), "{}", time.Time{})
	write(t, filepath.Join(root, "react", "index.js"), "react", time.Time{})
	if err := os.Symlink("react/index.js", filepath.Join(root, ".bin-react")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(s.MountDir("ab-p-agent-1", "libs/a"), "left-pad", "index.js"), "pad", time.Time{})
	write(t, filepath.Join(root, "ro", "file"), "ro", time.Time{})
	if err := os.Chmod(filepath.Join(root, "ro"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(s.Dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(filepath.Join(root, "react"), old, old); err != nil {
		t.Fatal(err)
	}

	s.Capture("ab-p-agent-1", "p", []Install{in})
	if !s.HasSnapshot("p", in.Key) {
		t.Fatal("no snapshot after Capture")
	}
	if _, err := os.Stat(s.agentDir("ab-p-agent-1")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("agent's directory is still there: %v", err)
	}

	restored, err := s.Prepare("ab-p-agent-2", "p", []Install{in})
	if err != nil || len(restored) != 1 {
		t.Fatalf("Prepare = %v, %v", restored, err)
	}
	root2 := s.MountDir("ab-p-agent-2", ".")
	for path, want := range map[string]string{
		filepath.Join(root2, "react", "index.js"):                                   "react",
		filepath.Join(root2, ".bin-react"):                                          "react",
		filepath.Join(s.MountDir("ab-p-agent-2", "libs/a"), "left-pad", "index.js"): "pad",
	} {
		if b, err := os.ReadFile(path); err != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", path, b, err, want)
		}
	}
	if info, err := os.Stat(filepath.Join(root2, "ro")); err != nil || info.Mode().Perm() != 0o555 {
		t.Errorf("a read-only directory came back as %v (%v)", info.Mode(), err)
	}
	if info, err := os.Stat(filepath.Join(root2, "react")); err != nil || !info.ModTime().Equal(old) {
		t.Errorf("a restored directory's time is %v, want %v (%v)", info.ModTime(), old, err)
	}

	// An edit in place in one agent's (patch-package, a postinstall) reaches
	// neither the snapshot nor the next agent given it.
	edited := filepath.Join(root2, "react", "index.js")
	f, err := os.OpenFile(edited, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("patched"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if info, err := os.Stat(edited); err != nil || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		t.Errorf("a restored file has other links: %v", err)
	}
	if _, err := s.Prepare("ab-p-agent-3", "p", []Install{in}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.MountDir("ab-p-agent-3", "."), "react", "index.js")); string(b) != "react" {
		t.Errorf("the next agent got %q, another agent's edit", b)
	}

	// A machine made again keeps what its directories hold.
	write(t, filepath.Join(root2, "mine"), "x", time.Time{})
	if restored, err := s.Prepare("ab-p-agent-2", "p", []Install{in}); err != nil || len(restored) != 0 {
		t.Fatalf("Prepare over a full directory = %v, %v", restored, err)
	}
	if _, err := os.Stat(filepath.Join(root2, "mine")); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureSkipsStaleOrUnfinishedInstalls(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	g := Group{Lockfile: "pnpm-lock.yaml", Dirs: []string{"."}}
	for name, marker := range map[string]time.Time{
		"no marker":                  {},
		"marker older than lockfile": time.Now().Add(-2 * time.Hour),
	} {
		in := Install{Group: g, Key: Key(g, []byte(name), ""), LockTime: time.Now().Add(-time.Hour)}
		if _, err := s.Prepare("i", "p", []Install{in}); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(s.MountDir("i", "."), "x", "index.js"), "x", time.Time{})
		if !marker.IsZero() {
			write(t, filepath.Join(s.MountDir("i", "."), ".modules.yaml"), "", marker)
		}
		s.Capture("i", "p", []Install{in})
		if s.HasSnapshot("p", in.Key) {
			t.Errorf("%s: kept a snapshot", name)
		}
	}
}

func TestTrim(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	now := time.Now()
	s.now = func() time.Time { return now }
	snap := func(project, key, lockfile string, bytes int64, used time.Time) string {
		dir := s.snapshotDir(project, key)
		write(t, filepath.Join(dir, metaFile), fmt.Sprintf(`{"lockfile":%q,"bytes":%d}`, lockfile, bytes), time.Time{})
		if err := os.Chtimes(dir, used, used); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	day := 24 * time.Hour
	a1 := snap("p", "a1", "package-lock.json", 1<<30, now.Add(-1*day))
	a2 := snap("p", "a2", "package-lock.json", 1<<30, now.Add(-2*day))
	a3 := snap("p", "a3", "package-lock.json", 1<<30, now.Add(-3*day)) // a third of one lockfile
	b1 := snap("p", "b1", "web/pnpm-lock.yaml", 1<<30, now.Add(-4*day))
	gone := snap("q", "x", "package-lock.json", 1<<30, now.Add(-1*day)) // a project that's gone
	fresh := snap("q", "y", "package-lock.json", 1<<30, now)            // just made: left alone
	stray := filepath.Join(s.Dir, "agents", "ab-p-old")
	write(t, filepath.Join(stray, "d-.", "x"), "x", time.Time{})
	if err := os.Chtimes(stray, now.Add(-day), now.Add(-day)); err != nil {
		t.Fatal(err)
	}
	liveDir := filepath.Join(s.Dir, "agents", "ab-p-live")
	write(t, filepath.Join(liveDir, "d-.", "x"), "x", time.Time{})

	s.Max = func() int64 { return 2<<30 + 1 }
	s.Trim(map[string]bool{"ab-p-live": true}, map[string]bool{"p": true})
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	for p, want := range map[string]bool{a1: true, a2: false, a3: false, b1: false, gone: false, fresh: true, stray: false, liveDir: true} {
		if exists(p) != want {
			t.Errorf("%s exists = %v, want %v", p, !want, want)
		}
	}
}
