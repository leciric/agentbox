package datamove

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"agentbox/internal/state"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// fakeHome is a home with AgentBox's data where earlier versions kept it:
// the VM's disk, a project's repository outside it with an agent's worktree
// in it, a lead's Claude Code sessions, the app's agentbox linked from
// ~/.local/bin, and a file outside naming a path in it.
type fakeHome struct {
	home, from, to, repo, wt, unit string
}

func newFakeHome(t *testing.T) fakeHome {
	t.Helper()
	home := t.TempDir()
	h := fakeHome{
		home: home,
		from: filepath.Join(home, ".local", "share", "agentbox"),
		to:   filepath.Join(home, ".agentbox"),
		repo: filepath.Join(home, "src", "shop"),
		unit: filepath.Join(home, ".config", "systemd", "user", "agentbox.service"),
	}
	h.wt = filepath.Join(h.from, "worktrees", "shop", "agent-01")
	write(t, filepath.Join(h.from, "vm", "agentbox", "root.raw"), "disk")
	write(t, filepath.Join(h.from, "state.db"), "")
	write(t, filepath.Join(h.from, "bin", "agentbox"), "#!/bin/sh\n")
	write(t, filepath.Join(h.from, "projects", "shop", "lead-home", ".claude", "projects",
		ClaudeProjectName(filepath.Join(h.from, "worktrees", "shop", "lead")), "s.jsonl"), "{}")
	write(t, h.unit, "ExecStart="+filepath.Join(h.from, "bin", "agentbox")+" daemon\n")
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(h.from, "bin", "agentbox"), filepath.Join(home, ".local", "bin", "agentbox")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, h.repo, "init", "-q", "-b", "main")
	git(t, h.repo, "commit", "-q", "--allow-empty", "-m", "first")
	git(t, h.repo, "worktree", "add", "-q", "-b", "agentbox/one", h.wt)
	return h
}

func (h fakeHome) move(stop func(context.Context) error) Move {
	return Move{From: h.from, To: h.to, Home: h.home, Files: []string{h.unit}, Stop: stop}
}

func TestRunMovesTheDataAndWhatNamesIt(t *testing.T) {
	h := newFakeHome(t)
	var stops atomic.Int32
	moved, err := h.move(func(context.Context) error {
		stops.Add(1)
		if _, err := os.Stat(h.from); err != nil {
			t.Error("Stop was called after the move")
		}
		return nil
	}).Run(context.Background())
	if err != nil || !moved {
		t.Fatalf("Run = %v, %v, want moved", moved, err)
	}
	if stops.Load() != 1 {
		t.Errorf("Stop was called %d times, want once", stops.Load())
	}
	if _, err := os.Lstat(h.from); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s is still there: %v", h.from, err)
	}
	if got := read(t, filepath.Join(h.to, "vm", "agentbox", "root.raw")); got != "disk" {
		t.Errorf("the VM's disk = %q after the move", got)
	}

	// The repository knows where its worktree went.
	newWT := filepath.Join(h.to, "worktrees", "shop", "agent-01")
	if list := git(t, h.repo, "worktree", "list", "--porcelain"); !strings.Contains(list, "worktree "+newWT+"\n") || strings.Contains(list, "prunable") {
		t.Errorf("git worktree list after the move:\n%s", list)
	}
	if st := git(t, newWT, "status", "--porcelain", "--branch"); !strings.Contains(st, "agentbox/one") {
		t.Errorf("git status in the moved worktree: %s", st)
	}

	// The command line tool's link, the unit, and the lead's sessions.
	if target, err := os.Readlink(filepath.Join(h.home, ".local", "bin", "agentbox")); err != nil || target != filepath.Join(h.to, "bin", "agentbox") {
		t.Errorf("~/.local/bin/agentbox -> %s, %v", target, err)
	}
	if got, want := read(t, h.unit), "ExecStart="+filepath.Join(h.to, "bin", "agentbox")+" daemon\n"; got != want {
		t.Errorf("unit = %q, want %q", got, want)
	}
	sessions := filepath.Join(h.to, "projects", "shop", "lead-home", ".claude", "projects",
		ClaudeProjectName(filepath.Join(h.to, "worktrees", "shop", "lead")), "s.jsonl")
	if _, err := os.Stat(sessions); err != nil {
		t.Errorf("the lead's sessions didn't follow its worktree: %v", err)
	}

	k, ok := ReadMarker(h.to)
	if !ok || k.From != h.from || k.To != h.to {
		t.Errorf("marker = %+v, %v", k, ok)
	}
	if got := k.Rewrite(filepath.Join(h.from, "run", "agents", "x.sock")); got != filepath.Join(h.to, "run", "agents", "x.sock") {
		t.Errorf("Rewrite = %s", got)
	}
	if got := k.Rewrite(h.from + "-other/x"); got != h.from+"-other/x" {
		t.Errorf("Rewrite of a sibling = %s, want it as it was", got)
	}
	if err := ClearMarker(h.to); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadMarker(h.to); ok {
		t.Error("the marker is still there once cleared")
	}

	// Run again: nothing left to move.
	if moved, err := h.move(func(context.Context) error { t.Error("Stop with nothing to move"); return nil }).Run(context.Background()); moved || err != nil {
		t.Errorf("second Run = %v, %v, want nothing", moved, err)
	}
}

func TestRunIntoAnEmptyDirectory(t *testing.T) {
	h := newFakeHome(t)
	if err := os.MkdirAll(h.to, 0o755); err != nil {
		t.Fatal(err)
	}
	if moved, err := h.move(nil).Run(context.Background()); err != nil || !moved {
		t.Fatalf("Run = %v, %v, want moved", moved, err)
	}
	if _, err := os.Stat(filepath.Join(h.to, "state.db")); err != nil {
		t.Error(err)
	}
}

func TestRunLeavesEverythingWhenTheNewDirectoryHasFiles(t *testing.T) {
	h := newFakeHome(t)
	write(t, filepath.Join(h.to, "notes.md"), "mine")
	moved, err := h.move(func(context.Context) error { t.Error("Stop before a move that can't happen"); return nil }).Run(context.Background())
	if moved || err == nil || !strings.Contains(err.Error(), "isn't an empty directory") {
		t.Fatalf("Run = %v, %v, want an error saying why", moved, err)
	}
	if _, err := os.Stat(filepath.Join(h.from, "state.db")); err != nil {
		t.Error("the old data was touched:", err)
	}
	if read(t, filepath.Join(h.to, "notes.md")) != "mine" {
		t.Error("the new directory was touched")
	}
}

func TestRunLeavesEverythingWhenStopFails(t *testing.T) {
	h := newFakeHome(t)
	moved, err := h.move(func(context.Context) error { return errors.New("the VM won't stop") }).Run(context.Background())
	if moved || err == nil || !strings.Contains(err.Error(), "the VM won't stop") {
		t.Fatalf("Run = %v, %v", moved, err)
	}
	if _, err := os.Stat(filepath.Join(h.from, "state.db")); err != nil {
		t.Error(err)
	}
	if _, err := os.Lstat(h.to); !errors.Is(err, os.ErrNotExist) {
		t.Error("made", h.to)
	}
}

func TestRunSaysHowToMoveItWhenTheRenameFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root renames out of a read-only directory")
	}
	h := newFakeHome(t)
	parent := filepath.Dir(h.from)
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	moved, err := h.move(nil).Run(context.Background())
	if moved || err == nil || !strings.Contains(err.Error(), "mv "+`"`+h.from+`"`) {
		t.Fatalf("Run = %v, %v, want an error with the mv to run", moved, err)
	}
	if _, err := os.Stat(filepath.Join(h.from, "state.db")); err != nil {
		t.Error(err)
	}
	if target, _ := os.Readlink(filepath.Join(h.home, ".local", "bin", "agentbox")); target != filepath.Join(h.from, "bin", "agentbox") {
		t.Errorf("the link was changed to %s by a move that didn't happen", target)
	}
}

func TestRunOnceForCommandsStartedTogether(t *testing.T) {
	h := newFakeHome(t)
	var moves, stops atomic.Int32
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			moved, err := h.move(func(context.Context) error { stops.Add(1); return nil }).Run(context.Background())
			if err != nil {
				t.Error(err)
			}
			if moved {
				moves.Add(1)
			}
		})
	}
	wg.Wait()
	if moves.Load() != 1 || stops.Load() != 1 {
		t.Errorf("%d moves and %d stops, want one of each", moves.Load(), stops.Load())
	}
}

func TestPending(t *testing.T) {
	home := t.TempDir()
	from, to := filepath.Join(home, "old"), filepath.Join(home, "new")
	m := Move{From: from, To: to}
	if m.Pending() {
		t.Error("Pending with nothing there")
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(to, from); err != nil {
		t.Fatal(err)
	}
	if m.Pending() {
		t.Error("Pending for a link to the new directory")
	}
	if (Move{From: to, To: to}).Pending() {
		t.Error("Pending when AGENTBOX_HOME names the old directory")
	}
}

// Without git, the repository's pointer to the worktree is written directly.
func TestRepairWorktreeWithoutGit(t *testing.T) {
	h := newFakeHome(t)
	t.Setenv("PATH", "")
	if moved, err := h.move(nil).Run(context.Background()); err != nil || !moved {
		t.Fatalf("Run = %v, %v", moved, err)
	}
	newWT := filepath.Join(h.to, "worktrees", "shop", "agent-01")
	back := filepath.Join(h.repo, ".git", "worktrees", "agent-01", "gitdir")
	if got := strings.TrimSpace(read(t, back)); got != filepath.Join(newWT, ".git") {
		t.Errorf("%s = %s, want the moved worktree's .git", back, got)
	}
}

// From a non-default XDG_DATA_HOME, which no migration knows, the paths in
// state.db are rewritten by the move.
func TestRunRewritesStateFromElsewhere(t *testing.T) {
	home := t.TempDir()
	from, to := filepath.Join(home, "xdg", "agentbox"), filepath.Join(home, ".agentbox")
	st, err := state.Open(filepath.Join(from, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO projects (name, root, created_at) VALUES ('shop', '/src/shop', 1)`,
		`INSERT INTO agents (project, name, instance, ai, autonomous, branch, base_ref, base_commit, worktree, status, created_at)
			VALUES ('shop', 'agent-01', 'ab-shop-agent-01', 'claude', 1, 'b', 'main', 'abc', '` + from + `/worktrees/shop/agent-01', 'ready', 1)`,
	} {
		if _, err := st.DB().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()
	if moved, err := (Move{From: from, To: to}).Run(ctx); err != nil || !moved {
		t.Fatalf("Run = %v, %v", moved, err)
	}
	st, err = state.Open(filepath.Join(to, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	a, err := st.Agent(ctx, "shop", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(to, "worktrees", "shop", "agent-01"); a.Worktree != want {
		t.Errorf("worktree = %s, want %s", a.Worktree, want)
	}
}

func TestRenameClaudeProjects(t *testing.T) {
	dir := t.TempDir()
	pairs := [][2]string{{"/home/u/.local/share/agentbox", "/home/u/.agentbox"}}
	for _, name := range []string{
		"-home-u--local-share-agentbox-worktrees-shop-lead",
		"-home-u--local-share-agentbox2-x", // another directory, named alike
		"-home-u-src-shop",
	} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := RenameClaudeProjects(dir, pairs); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"-home-u--agentbox-worktrees-shop-lead", "-home-u--local-share-agentbox2-x", "-home-u-src-shop"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := ClaudeProjectName("/home/lint/.local/share/agentbox/worktrees/agentbox/agent-191"); got != "-home-lint--local-share-agentbox-worktrees-agentbox-agent-191" {
		t.Errorf("ClaudeProjectName = %s", got)
	}
}
