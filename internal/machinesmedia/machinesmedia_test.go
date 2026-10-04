package machinesmedia

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/paths"
)

func TestWriteListGetDelete(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "media")}
	if got, err := s.List(); err != nil || len(got) != 0 {
		t.Fatalf("empty store: %v, %v", got, err)
	}
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	old, err := s.Write(Item{Kind: Screenshot, Created: t0, Repo: "leciric/agentbox", Branch: "main", Caption: "login"}, ".png", strings.NewReader("png"))
	if err != nil {
		t.Fatal(err)
	}
	if old.Bytes != 3 || !ValidID(old.ID) {
		t.Fatalf("written: %+v", old)
	}
	recent, err := s.Write(Item{Kind: Recording, Created: t0.Add(time.Hour), DurationMs: 1500}, ".mp4", strings.NewReader("video"))
	if err != nil {
		t.Fatal(err)
	}

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != recent.ID || list[1].ID != old.ID {
		t.Fatalf("want newest first, got %+v", list)
	}
	if list[1].Path != filepath.Join(s.Dir, old.ID+".png") || list[1].Caption != "login" || list[1].Repo != "leciric/agentbox" {
		t.Fatalf("entry: %+v", list[1])
	}

	got, err := s.Get(recent.ID)
	if err != nil || got.DurationMs != 1500 || filepath.Ext(got.Path) != ".mp4" {
		t.Fatalf("Get: %+v, %v", got, err)
	}
	if err := s.Delete(recent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(recent.ID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Get after Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, recent.ID+".mp4")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("media file left behind: %v", err)
	}
}

func TestWriteRefuses(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, tc := range []struct {
		item Item
		ext  string
	}{
		{Item{Kind: "video"}, ".mp4"},
		{Item{Kind: Screenshot, ID: "../escape"}, ".png"},
		{Item{Kind: Screenshot}, ".json"},
		{Item{Kind: Screenshot}, "png"},
		{Item{Kind: Screenshot}, "./x"},
	} {
		if _, err := s.Write(tc.item, tc.ext, strings.NewReader("x")); err == nil {
			t.Errorf("Write(%+v, %q) succeeded", tc.item, tc.ext)
		}
	}
	if _, err := s.Write(Item{Kind: Screenshot, ID: "dup"}, ".png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(Item{Kind: Screenshot, ID: "dup"}, ".jpg", strings.NewReader("x")); err == nil {
		t.Error("a second item with the same id was written")
	}
}

func TestListSkipsIncompleteAndBroken(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	write := func(name, data string) {
		if err := os.WriteFile(filepath.Join(s.Dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pending.png", "x")                              // no sidecar yet
	write("orphan.json", `{"kind":"screenshot"}`)          // no media file
	write("broken.json", `{`)                              // unreadable
	write("broken.png", "x")                               //
	write(".tmp-123", "x")                                 // a write in progress
	write("ok.png", "x")                                   //
	write("ok.json", `{"id":"other","kind":"screenshot"}`) // the file's name wins
	if err := os.Mkdir(filepath.Join(s.Dir, "sub.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "ok" {
		t.Fatalf("got %+v", list)
	}
}

func TestListCachedRereadsOnlyChanged(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	a, _ := s.Write(Item{Kind: Screenshot, Caption: "first"}, ".png", strings.NewReader("x"))
	list, _ := s.List()
	prev := map[string]Entry{}
	for _, e := range list {
		e.Caption = "from cache"
		prev[e.ID] = e
	}
	again, _ := s.ListCached(prev)
	if again[0].Caption != "from cache" {
		t.Fatalf("unchanged sidecar was reread: %+v", again[0])
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(s.Dir, a.ID+".json"), later, later); err != nil {
		t.Fatal(err)
	}
	again, _ = s.ListCached(prev)
	if again[0].Caption != "first" {
		t.Fatalf("changed sidecar wasn't reread: %+v", again[0])
	}
}

func TestSaveAndOpen(t *testing.T) {
	p := paths.Paths{Data: t.TempDir()}
	s := Open(p)
	if s.Dir != filepath.Join(p.Data, "machines", "media") {
		t.Fatalf("Dir: %s", s.Dir)
	}
	src := filepath.Join(t.TempDir(), "shot.PNG")
	if err := os.WriteFile(src, []byte("abcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	item, err := s.Save(Item{Kind: Screenshot}, src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, item.ID+".png")); err != nil || item.Bytes != 4 {
		t.Fatalf("saved %+v: %v", item, err)
	}
}

func TestNewIDSortsByTime(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 6e6, time.UTC)
	a, b := NewID(t0), NewID(t0.Add(time.Millisecond))
	if !ValidID(a) || a >= b || !strings.HasPrefix(a, "20260102-030405006-") {
		t.Fatalf("ids %q, %q", a, b)
	}
}

func TestRepoName(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/leciric/agentbox.git": "leciric/agentbox",
		"git@github.com:leciric/agentbox.git":     "leciric/agentbox",
		"ssh://git@host:22/group/sub/repo":        "sub/repo",
		"https://github.com/":                     "",
		"/srv/git/repo.git":                       "",
		"":                                        "",
	} {
		if got := RepoName(in); got != want {
			t.Errorf("RepoName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDescribe(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "myrepo")
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "-b", "trunk")
	run("commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(filepath.Dir(dir), "wt")
	run("worktree", "add", "-q", "-b", "feature", wt)

	worktree, repo, branch := Describe(ctx, filepath.Join(wt))
	if worktree != wt || repo != "myrepo" || branch != "feature" {
		t.Fatalf("worktree: %q %q %q", worktree, repo, branch)
	}
	run("remote", "add", "origin", "git@github.com:me/thing.git")
	if _, repo, _ := Describe(ctx, dir); repo != "me/thing" {
		t.Fatalf("with origin: %q", repo)
	}
	plain := t.TempDir()
	if worktree, repo, branch := Describe(ctx, plain); worktree != plain || repo != "" || branch != "" {
		t.Fatalf("outside git: %q %q %q", worktree, repo, branch)
	}
}
