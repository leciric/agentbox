package worktreefs

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/testutil"
)

// TestMain gives git a configuration of its own, with an identity to commit
// as, rather than the machine's, which CI doesn't have.
func TestMain(m *testing.M) {
	cleanup, err := testutil.IsolateGit()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo is a worktree with a commit on main, a branch "work" off it, and on
// that branch a committed change, an uncommitted one, an untracked file and
// folder, and ignored ones.
func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	testutil.Git(t, root, "init", "-q", "-b", "main")
	write(t, root, ".gitignore", "build/\n*.log\n")
	write(t, root, "README.md", "# hi\n")
	write(t, root, "src/main.go", "package main\n")
	write(t, root, "src/old.go", "package main\n")
	testutil.Git(t, root, "add", "-A")
	testutil.Git(t, root, "commit", "-q", "-m", "first")
	testutil.Git(t, root, "checkout", "-q", "-b", "work")
	write(t, root, "src/new.go", "package main\n")
	testutil.Git(t, root, "add", "-A")
	testutil.Git(t, root, "commit", "-q", "-m", "second")
	write(t, root, "README.md", "# hello\n")
	if err := os.Remove(filepath.Join(root, "src/old.go")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "notes.txt", "n\n")
	write(t, root, "docs/a.md", "a\n")
	write(t, root, "build/out.bin", "x")
	write(t, root, "debug.log", "x")
	return root
}

func TestRelRefusesWhatLeavesTheWorktree(t *testing.T) {
	root := "/w/repo"
	for in, want := range map[string]string{
		"": "", ".": "", "src/a.go": "src/a.go", "./src//a.go": "src/a.go", "/w/repo/src/a.go": "src/a.go", "/w/repo": "",
		".gitignore": ".gitignore", "src/.git": "src/.git",
	} {
		if got, err := Rel(root, in); err != nil || got != want {
			t.Errorf("Rel(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"/", "..", "../x", "src/../../x", "/w/repo2/x", "/etc/passwd", ".git", ".git/config", "a\x00b"} {
		if _, err := Rel(root, in); !errors.Is(err, ErrOutside) {
			t.Errorf("Rel(%q) = %v, want ErrOutside", in, err)
		}
	}
}

func TestResolveRefusesASymlinkOut(t *testing.T) {
	root := repo(t)
	outside := t.TempDir()
	write(t, outside, "secret", "s")
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("src", filepath.Join(root, "srclink")); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(root, "link", false); !errors.Is(err, ErrOutside) {
		t.Errorf("Read(link out) = %v, want ErrOutside", err)
	}
	if _, err := List(root, "srclink", false); err != nil {
		t.Errorf("List(link to src) = %v", err)
	}
	list, err := List(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list.Entries {
		if e.Name == "srclink" && (!e.Dir || !e.Symlink) {
			t.Errorf("srclink = %+v, want a symlinked folder", e)
		}
		if e.Name == "link" && e.Dir {
			t.Errorf("link out = %+v, want a file", e)
		}
	}
}

func TestListSortsAndHidesIgnored(t *testing.T) {
	root := repo(t)
	list, err := List(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list.Entries {
		names = append(names, e.Name)
	}
	if got, want := strings.Join(names, " "), "docs src .gitignore notes.txt README.md"; got != want {
		t.Errorf("List() = %s, want %s", got, want)
	}
	list, err = List(root, "", true)
	if err != nil {
		t.Fatal(err)
	}
	ignored := map[string]bool{}
	for _, e := range list.Entries {
		if e.Ignored {
			ignored[e.Path] = true
		}
	}
	if !ignored["build"] || !ignored["debug.log"] || len(ignored) != 2 {
		t.Errorf("ignored = %v, want build and debug.log", ignored)
	}
	sub, err := List(root, "src", false)
	if err != nil || len(sub.Entries) != 2 || sub.Entries[0].Path != "src/main.go" {
		t.Errorf("List(src) = %+v, %v", sub, err)
	}
	if _, err := List(root, "../", false); !errors.Is(err, ErrOutside) {
		t.Errorf("List(..) = %v", err)
	}
}

func TestReadGuardsBinaryLargeAndImages(t *testing.T) {
	root := t.TempDir()
	write(t, root, "bin.dat", "ab\x00cd")
	write(t, root, "pic.png", "\x89PNG")
	write(t, root, "big.txt", strings.Repeat("line of text\n", TextLimit/13+100))
	write(t, root, "a.go", "package a\n")

	if c, err := Read(root, "bin.dat", false); err != nil || !c.Binary || c.Text != "" {
		t.Errorf("Read(bin) = %+v, %v", c, err)
	}
	c, err := Read(root, "pic.png", false)
	if err != nil || c.Image != "image/png" || c.Data != base64.StdEncoding.EncodeToString([]byte("\x89PNG")) {
		t.Errorf("Read(png) = %+v, %v", c, err)
	}
	c, err = Read(root, "big.txt", false)
	if err != nil || !c.Truncated || len(c.Text) > TextLimit || !strings.HasSuffix(c.Text, "\n") {
		t.Errorf("Read(big) truncated=%v len=%d, %v", c.Truncated, len(c.Text), err)
	}
	if c, err = Read(root, "big.txt", true); err != nil || c.Truncated {
		t.Errorf("Read(big, full) truncated=%v, %v", c.Truncated, err)
	}
	if c, err = Read(root, "a.go", false); err != nil || c.Text != "package a\n" || c.Size != 10 {
		t.Errorf("Read(a.go) = %+v, %v", c, err)
	}
	if _, err := Read(root, "missing", false); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Read(missing) = %v", err)
	}
	if _, err := Read(root, "", false); err == nil {
		t.Error("Read(root folder) succeeded")
	}
}

func TestStatusAgainstTheBase(t *testing.T) {
	root := repo(t)
	commit, name := Base(root, "main")
	if commit == "" || name != "main" {
		t.Fatalf("Base(main) = %q, %q", commit, name)
	}
	changes, truncated, err := Status(root, commit)
	if err != nil || truncated {
		t.Fatal(err)
	}
	want := map[string]string{
		"README.md":  api.FileModified,
		"src/new.go": api.FileAdded, // committed on the branch: still a change against main
		"src/old.go": api.FileDeleted,
		"notes.txt":  api.FileUntracked,
		"docs/":      api.FileUntracked,
	}
	if len(changes) != len(want) {
		t.Errorf("Status() = %v, want %v", changes, want)
	}
	for p, st := range want {
		if changes[p] != st {
			t.Errorf("Status()[%s] = %q, want %q", p, changes[p], st)
		}
	}

	// Against HEAD, the branch's own commit is no change.
	head, name := Base(root, "")
	if name != "HEAD" {
		t.Fatalf("Base('') = %q, %q", head, name)
	}
	changes, _, _ = Status(root, head)
	if _, ok := changes["src/new.go"]; ok {
		t.Errorf("Status(HEAD) = %v, has the committed src/new.go", changes)
	}

	text, missing, err := BaseText(root, commit, "README.md")
	if err != nil || missing || text != "# hi\n" {
		t.Errorf("BaseText(README) = %q, %v, %v", text, missing, err)
	}
	if _, missing, _ := BaseText(root, commit, "src/new.go"); !missing {
		t.Error("BaseText(new file) not missing")
	}
}

func TestStatusOutsideARepository(t *testing.T) {
	if _, _, err := Status(t.TempDir(), ""); err == nil {
		t.Error("Status() outside a repository succeeded")
	}
	commit, _ := Base(t.TempDir(), "main")
	if commit != "" {
		t.Errorf("Base() outside a repository = %q", commit)
	}
}
