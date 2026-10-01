// Package datamove moves AgentBox's data from where earlier versions kept it,
// ~/.local/share/agentbox (paths.Legacy), to ~/.agentbox (paths.Home), the
// first time a version that keeps it there runs.
//
// The directory is renamed, never copied: it holds the VM's disks, every
// agent's worktree and the state, tens of gigabytes, and a rename on one file
// system is instant and all or nothing. Whatever runs from it (the VM, a
// daemon) is stopped first, by the caller's Stop. When the rename can't
// happen — the two are on different file systems, or ~/.agentbox is already
// there with something in it — nothing has been changed, and the error says
// how to move it by hand.
//
// What names a path in it outside it is put right after: the repositories'
// pointers to the worktrees in it (git worktree repair), the link
// "Install command-line tool" made in ~/.local/bin, files the caller names
// (a Lima VM's definition, the daemon's systemd unit), and the directories
// Claude Code keeps a project's sessions in, which are named after its working
// directory. state.db's paths are a migration (package state), and an agent
// machine's Incus devices are the daemon's to change, as it starts
// (Marker).
//
// Nothing is left at the old path: everything that used it is AgentBox's own,
// and is changed here or by the daemon.
package datamove

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"agentbox/internal/state"
)

// Move is AgentBox's data moving from From to To.
type Move struct {
	From, To string
	// Home is the user's home directory, whose ~/.local/bin/agentbox
	// follows the move when it is a link into From.
	Home string
	// Also are other directories moved the same way, by another AgentBox,
	// whose paths this one's data names: in AgentBox's VM, the host's, where
	// its agents' worktrees are. Each is a pair, from and to.
	Also [][2]string
	// Files are files outside From that name paths in it, rewritten for To
	// (and Also).
	Files []string
	// Stop stops whatever runs from From before it is renamed: the VM, the
	// daemon. It is called only when there is something to move.
	Stop func(ctx context.Context) error
	// Log is where progress goes.
	Log io.Writer
}

// Pending reports whether From is still there to move: a directory of its
// own, rather than nothing or a link someone made to the new one.
func (m Move) Pending() bool {
	if m.From == "" || m.To == "" || filepath.Clean(m.From) == filepath.Clean(m.To) {
		return false
	}
	fi, err := os.Lstat(m.From)
	return err == nil && fi.IsDir()
}

// Run moves From to To if it is still there, and reports whether it did.
// Several agentbox commands can start at once: the first moves, and the others
// wait for it and find nothing left to move.
func (m Move) Run(ctx context.Context) (bool, error) {
	if m.Log == nil {
		m.Log = io.Discard
	}
	if !m.Pending() {
		return false, nil
	}
	unlock, err := m.lock(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	if !m.Pending() {
		return false, nil // another command moved it while this one waited
	}
	if err := m.checkTo(); err != nil {
		return false, err
	}
	logf(m.Log, "AgentBox keeps its data in %s now: moving it from %s", m.To, m.From)
	if m.Stop != nil {
		if err := m.Stop(ctx); err != nil {
			return false, fmt.Errorf("moving AgentBox's data from %s to %s: %w", m.From, m.To, err)
		}
	}
	if err := m.checkTo(); err != nil { // something made it meanwhile
		return false, err
	}
	if fi, err := os.Lstat(m.To); err == nil && fi.IsDir() {
		_ = os.Remove(m.To) // empty: checkTo said so
	}
	if err := os.Rename(m.From, m.To); err != nil {
		return false, m.renameError(err)
	}
	// For the daemon, when this is its data: a VM's front end has none.
	if _, err := os.Stat(filepath.Join(m.To, "state.db")); err == nil {
		if err := writeMarker(m.To, Marker{From: m.From, To: m.To, Also: m.Also}); err != nil {
			logf(m.Log, "Couldn't note the move for the daemon: %v", err)
		}
	}
	m.fixUp(ctx)
	logf(m.Log, "Moved AgentBox's data to %s", m.To)
	return true, nil
}

// lock holds From against another agentbox command moving it at the same
// time: a lock on the directory itself, which goes with it when it moves.
func (m Move) lock(ctx context.Context) (func(), error) {
	f, err := os.Open(m.From)
	if err != nil {
		return nil, err
	}
	for waited := false; ; waited = true {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("locking %s to move it: %w", m.From, err)
		}
		if !waited {
			logf(m.Log, "Waiting for another agentbox command moving AgentBox's data to %s", m.To)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// checkTo makes sure the rename can't put anything over what is at To: it
// can be missing, or an empty directory, and nothing else.
func (m Move) checkTo() error {
	fi, err := os.Lstat(m.To)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.IsDir() {
		entries, err := os.ReadDir(m.To)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return nil
		}
	}
	return fmt.Errorf("AgentBox keeps its data in %s now, and couldn't move it there from %s: %s is already there, and isn't an empty directory. "+
		"Nothing was changed. Move what you want to keep out of %s, remove it, and run agentbox again", m.To, m.From, m.To, m.To)
}

func (m Move) renameError(err error) error {
	why := err.Error()
	if errors.Is(err, syscall.EXDEV) {
		why = "they are on different file systems"
	}
	return fmt.Errorf("AgentBox keeps its data in %s now, and couldn't move it there from %s: %s. "+
		"Nothing was changed. Stop AgentBox (agentbox vm stop, or agentbox daemon stop), move it yourself with mv %q %q, and run agentbox again",
		m.To, m.From, why, m.From, m.To)
}

// pairs are the directories that moved, From's first.
func (m Move) pairs() [][2]string {
	return append([][2]string{{m.From, m.To}}, m.Also...)
}

// fixUp puts right what outside To named a path in From. None of it stops the
// move, which has happened: what fails is logged, for the user to see.
func (m Move) fixUp(ctx context.Context) {
	for _, err := range []error{
		repairWorktrees(ctx, filepath.Join(m.To, "worktrees"), m.Log),
		m.relinkCLI(),
		m.rewriteFiles(),
		m.moveStatePaths(ctx),
		moveClaudeProjects(filepath.Join(m.To, "projects"), m.pairs()),
	} {
		if err != nil {
			logf(m.Log, "%v", err)
		}
	}
}

// repairWorktrees points each repository back at its worktrees under dir,
// <project>/<agent>, which moved: a worktree's own .git names its repository,
// which didn't move, but the repository names the worktree. git worktree
// repair, run in a worktree, mends that; with no git, the one file it would
// change is written here.
func repairWorktrees(ctx context.Context, dir string, log io.Writer) error {
	projects, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, noGit := exec.LookPath("git")
	var failed []string
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		agents, err := os.ReadDir(filepath.Join(dir, project.Name()))
		if err != nil {
			continue
		}
		for _, a := range agents {
			wt := filepath.Join(dir, project.Name(), a.Name())
			if fi, err := os.Lstat(filepath.Join(wt, ".git")); err != nil || !fi.Mode().IsRegular() {
				continue // not a linked worktree
			}
			if noGit == nil {
				out, err := exec.CommandContext(ctx, "git", "-C", wt, "worktree", "repair").CombinedOutput()
				if err == nil {
					continue
				}
				logf(log, "git worktree repair in %s: %v: %s", wt, err, strings.TrimSpace(string(out)))
			}
			if err := repairWorktree(wt); err != nil {
				failed = append(failed, fmt.Sprintf("%s (%v)", wt, err))
			}
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("couldn't point the repositories back at these worktrees, run git worktree repair in each: %s", strings.Join(failed, ", "))
	}
	return nil
}

// repairWorktree is git worktree repair's one change, without git: the
// repository's gitdir file for the worktree at wt, written with wt's path.
func repairWorktree(wt string) error {
	b, err := os.ReadFile(filepath.Join(wt, ".git"))
	if err != nil {
		return err
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return fmt.Errorf("%s/.git doesn't name a gitdir", wt)
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(wt, gitdir)
	}
	back := filepath.Join(gitdir, "gitdir")
	if _, err := os.Stat(back); err != nil {
		return err
	}
	return os.WriteFile(back, []byte(filepath.Join(wt, ".git")+"\n"), 0o644)
}

// relinkCLI points ~/.local/bin/agentbox at the same file under To, when it
// is a link into From: the app's own copy of agentbox, which moved.
func (m Move) relinkCLI() error {
	if m.Home == "" {
		return nil
	}
	link := filepath.Join(m.Home, ".local", "bin", "agentbox")
	target, err := os.Readlink(link)
	if err != nil {
		return nil // none, or not a link: not the app's
	}
	moved, ok := movePath(target, m.From, m.To)
	if !ok {
		return nil
	}
	tmp := link + ".moving"
	_ = os.Remove(tmp)
	if err := os.Symlink(moved, tmp); err != nil {
		return fmt.Errorf("pointing %s at %s: %w", link, moved, err)
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("pointing %s at %s: %w", link, moved, err)
	}
	return nil
}

// rewriteFiles rewrites each of Files that names a path in a directory that
// moved, keeping its mode.
func (m Move) rewriteFiles() error {
	var failed []string
	for _, file := range m.Files {
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		s := string(b)
		for _, p := range m.pairs() {
			s = strings.ReplaceAll(s, p[0]+"/", p[1]+"/")
		}
		if s == string(b) {
			continue
		}
		fi, err := os.Stat(file)
		if err != nil {
			continue
		}
		tmp := file + ".moving"
		if err := os.WriteFile(tmp, []byte(s), fi.Mode().Perm()); err != nil {
			failed = append(failed, file)
			continue
		}
		if err := os.Rename(tmp, file); err != nil {
			_ = os.Remove(tmp)
			failed = append(failed, file)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("couldn't rewrite these for %s: %s", m.To, strings.Join(failed, ", "))
	}
	return nil
}

// moveStatePaths rewrites the paths state.db keeps, when From isn't
// somewhere/.local/share/agentbox, which a migration rewrites whatever the
// home (package state): the data in a non-default XDG_DATA_HOME.
func (m Move) moveStatePaths(ctx context.Context) error {
	if strings.HasSuffix(filepath.Clean(m.From), filepath.Join("/.local", "share", "agentbox")) {
		return nil
	}
	db := filepath.Join(m.To, "state.db")
	if _, err := os.Stat(db); err != nil {
		return nil
	}
	st, err := state.Open(db)
	if err != nil {
		return fmt.Errorf("rewriting the paths in %s: %w", db, err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.MovePaths(ctx, m.From, m.To); err != nil {
		return fmt.Errorf("rewriting the paths in %s: %w", db, err)
	}
	return nil
}

// moveClaudeProjects renames, in each project's lead's home under projects,
// the directories Claude Code keeps sessions in for a working directory in
// one that moved, so the lead's chat carries on with its session.
func moveClaudeProjects(projects string, pairs [][2]string) error {
	entries, err := os.ReadDir(projects)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		dir := filepath.Join(projects, e.Name(), "lead-home", ".claude", "projects")
		if err := RenameClaudeProjects(dir, pairs); err != nil {
			return err
		}
	}
	return nil
}

// RenameClaudeProjects renames the session directories in dir, a
// ~/.claude/projects, that are named after a working directory in one of
// pairs' from, to the same name for its to. One already there is left be.
func RenameClaudeProjects(dir string, pairs [][2]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		for _, p := range pairs {
			from, to := ClaudeProjectName(p[0]), ClaudeProjectName(p[1])
			rest, ok := strings.CutPrefix(e.Name(), from)
			if !ok || (rest != "" && rest[0] != '-') {
				continue
			}
			target := filepath.Join(dir, to+rest)
			if _, err := os.Lstat(target); err == nil {
				break
			}
			if err := os.Rename(filepath.Join(dir, e.Name()), target); err != nil {
				return fmt.Errorf("moving Claude Code's sessions for %s: %w", e.Name(), err)
			}
			break
		}
	}
	return nil
}

// ClaudeProjectName is the directory Claude Code keeps the sessions of a
// working directory in, under ~/.claude/projects: its path with everything
// but letters and digits made a dash.
func ClaudeProjectName(dir string) string {
	b := []byte(dir)
	for i, c := range b {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		default:
			b[i] = '-'
		}
	}
	return string(b)
}

// movePath is path under to rather than from, when it is in from.
func movePath(path, from, to string) (string, bool) {
	if path == from {
		return to, true
	}
	if rest, ok := strings.CutPrefix(path, from+"/"); ok {
		return filepath.Join(to, rest), true
	}
	return "", false
}

// Marker is what a move leaves in the new directory for the daemon, which
// changes its agents' machines to match as it starts, and then clears it
// (ClearMarker).
type Marker struct {
	From string      `json:"from"`
	To   string      `json:"to"`
	Also [][2]string `json:"also,omitempty"`
}

// Pairs are the directories that moved, From's first.
func (k Marker) Pairs() [][2]string {
	return append([][2]string{{k.From, k.To}}, k.Also...)
}

// Rewrite is path in the directory it moved to, when it was in one that
// moved, and otherwise path.
func (k Marker) Rewrite(path string) string {
	for _, p := range k.Pairs() {
		if moved, ok := movePath(path, p[0], p[1]); ok {
			return moved
		}
	}
	return path
}

func markerFile(data string) string { return filepath.Join(data, "moved.json") }

func writeMarker(data string, k Marker) error {
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(markerFile(data), append(b, '\n'), 0o600)
}

// ReadMarker is the move noted in data, if one is still to be finished.
func ReadMarker(data string) (Marker, bool) {
	b, err := os.ReadFile(markerFile(data))
	if err != nil {
		return Marker{}, false
	}
	var k Marker
	if json.Unmarshal(b, &k) != nil || k.From == "" || k.To == "" {
		return Marker{}, false
	}
	return k, true
}

// ClearMarker notes that the move noted in data is finished.
func ClearMarker(data string) error {
	err := os.Remove(markerFile(data))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func logf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, "==> "+format+"\n", args...)
}
