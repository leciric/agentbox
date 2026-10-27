package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"agentbox/internal/gitrepo"
	"agentbox/internal/machine"
)

// Pod is a Workspace inside an agent's pod: a clone of one of the project's git
// profiles on the pod's volume, which this host has no path to. Every operation
// runs in the pod as the pod's user, through machine.Machine.UserExec, which
// agentd answers over the agent gateway; nothing here opens a host path, and
// Path is "" for that reason.
//
// A stopped pod has no gateway session, so every operation fails until the agent
// is started. Its tree is on the claim and comes back with the pod; the master's
// mirror, which could answer for a stopped pod from the last checkpoint, is not
// built yet.
type Pod struct {
	// Machine is the pod.
	Machine machine.Machine
	// Dir is the tree inside the pod, like /workspace.
	Dir string
	// User is the pod's user, image.Placeholder.Name ("agent"), which owns the
	// tree and everything in it.
	User string
	// Remote is the remote a checkpoint is pushed to, "origin" when empty: the
	// one git clone made for the tree.
	Remote string
	// Log, when set, receives what did not fail the call: a checkpoint the
	// remote refused is still a checkpoint inside the pod.
	Log io.Writer
}

var _ Workspace = Pod{}

// checkpointIdentity is the identity gitrepo gives a snapshot's commit, which
// never lands on a branch and so must not depend on the agent's own git
// identity either.
var checkpointIdentity = []string{
	"GIT_AUTHOR_NAME=AgentBox", "GIT_AUTHOR_EMAIL=agentbox@localhost",
	"GIT_COMMITTER_NAME=AgentBox", "GIT_COMMITTER_EMAIL=agentbox@localhost",
}

// Path is "", because a pod's tree is not on this host at all.
func (p Pod) Path() string { return "" }

// Prepare refuses: a pod's tree is a clone of the project's git profile, made
// inside the pod by the master when the agent is configured (gitprofile.go),
// before anything reaches this workspace. The manager knows the branch, the
// base and the profile; this value only ever sees the tree afterwards.
func (p Pod) Prepare(context.Context, gitrepo.Repo, PrepareOptions) error {
	return fmt.Errorf("a pod's tree is cloned inside it when the agent is configured, and this host does not make it: %s is not a path here", p.Dir)
}

// Diff is what the pod's tree changed against its base: Local's commands, run
// in the pod. The base is the project's base branch as it is now, so work taken
// in by a merge or a rebase is not the agent's, and the comparison is against a
// tree of the worktree's files, which is why uncommitted and untracked files
// show up in it too.
func (p Pod) Diff(ctx context.Context, opts DiffOptions) (string, error) {
	base := opts.BaseCommit
	if opts.BaseRef != "" && opts.BaseRef != "HEAD" {
		// gitrepo.DiffBase: where the tree's HEAD left the base branch, and
		// the commit it started at when they share no history.
		out, err := p.git(ctx, "merge-base", "HEAD", "refs/heads/"+opts.BaseRef)
		if err == nil && strings.TrimSpace(out) != "" {
			base = strings.TrimSpace(out)
		}
	}
	tree, err := p.tree(ctx)
	if err != nil {
		return "", err
	}
	args := []string{"diff"}
	if opts.Stat {
		args = append(args, "--stat")
	}
	args = append(args, base, tree)
	if len(opts.Paths) > 0 {
		args = append(append(args, "--"), opts.Paths...)
	}
	return p.git(ctx, args...)
}

// Files lists the tree's tracked and untracked files, at most limit of them.
func (p Pod) Files(ctx context.Context, limit int) ([]string, bool, error) {
	out, err := p.git(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, false, err
	}
	var files []string
	for _, name := range strings.Split(out, "\x00") {
		if name == "" {
			continue
		}
		if len(files) >= limit {
			return files, true, nil
		}
		files = append(files, name)
	}
	return files, false, nil
}

// ReadFile reads one file of the tree, by its path relative to the tree's root,
// where the pod has it: cat rather than a pull, so nothing of the pod lands on
// this host. A file that isn't there fails as cat does, with its message, not
// as fs.ErrNotExist.
func (p Pod) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if name == "" || !fs.ValidPath(name) {
		return nil, fmt.Errorf("%q is not a path inside the workspace", name)
	}
	out, err := p.exec(ctx, "cd "+quote(p.Dir)+" && cat -- "+quote(name))
	return []byte(out), err
}

// Checkpoint records the tree's files as a commit on ref, with a temporary
// index inside the pod: the tree's branch, index and files are left exactly as
// they were. It then pushes the ref to the profile's remote, so the checkpoint
// outlives the pod; a refused push is logged and keeps the checkpoint, which is
// in the pod's repository either way.
func (p Pod) Checkpoint(ctx context.Context, ref, message string) (string, error) {
	return p.checkpoint(ctx, ref, message)
}

// Snapshot is Checkpoint for a pod: its tree is the only half a pod has, and
// the claim the tree lives on is archived as a whole once the cluster can do it
// (Stage 5). The interface's Snapshot is the machine's disk plus this commit.
func (p Pod) Snapshot(ctx context.Context, ref, message string) (string, error) {
	return p.checkpoint(ctx, ref, message)
}

// Restore puts the pod's branch and files back where commit has them. On a pod
// this is the whole machine: there is no second disk half beside the tree, so
// nothing else is put back. Ignored files are left alone, as Local leaves them.
func (p Pod) Restore(ctx context.Context, commit string) error {
	head, err := p.ResolveCommit(ctx, commit+"^")
	if err != nil {
		return err
	}
	if _, err := p.git(ctx, "reset", "--quiet", "--hard", head); err != nil {
		return err
	}
	// -d without -x: what the agent built and git ignores, like node_modules
	// or .env, survives the restore.
	if _, err := p.git(ctx, "clean", "--force", "-d", "--quiet"); err != nil {
		return err
	}
	if _, err := p.git(ctx, "read-tree", "-m", "-u", "HEAD", commit); err != nil {
		return err
	}
	_, err = p.git(ctx, "reset", "--quiet")
	return err
}

// ResolveCommit returns the commit a branch, tag or revision names in the pod.
func (p Pod) ResolveCommit(ctx context.Context, rev string) (string, error) {
	out, err := p.git(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%q is not a commit in %s: %w", rev, p.Dir, err)
	}
	return strings.TrimSpace(out), nil
}

// DeleteRef removes a ref from the pod's repository, like a checkpoint past the
// number kept.
func (p Pod) DeleteRef(ctx context.Context, ref string) error {
	_, err := p.git(ctx, "update-ref", "-d", ref)
	return err
}

// Status reads whether the tree has a HEAD and whether git sees changes in it.
// A tree the pod can't be asked about is an error, never a tree that is gone.
func (p Pod) Status(ctx context.Context) (Status, error) {
	if _, err := p.git(ctx, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		if _, ran := exitCode(err); !ran {
			// The pod itself could not be reached, which is not a tree that
			// isn't there.
			return Status{}, err
		}
		// git ran and HEAD resolves to nothing: the volume holds no clone yet.
		return Status{}, nil
	}
	out, err := p.git(ctx, "status", "--porcelain")
	if err != nil {
		return Status{Exists: true}, err
	}
	return Status{Exists: true, Dirty: out != ""}, nil
}

// Destroy is nothing here: the tree lives on the pod's persistent volume claim,
// which belongs to the cluster, is deleted with the pod, and is kept for the
// retention window. This daemon has no name for it to delete.
func (p Pod) Destroy(context.Context, gitrepo.Repo) error { return nil }

// checkpoint is Checkpoint and Snapshot: the tree as a commit on ref, made
// with a temporary index so nothing in the tree moves, then published.
func (p Pod) checkpoint(ctx context.Context, ref, message string) (string, error) {
	tree, err := p.tree(ctx)
	if err != nil {
		return "", err
	}
	out, err := p.exec(ctx, p.gitLine(checkpointIdentity, "commit-tree", tree, "-p", "HEAD", "-m", message))
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(out)
	if commit == "" {
		return "", fmt.Errorf("%s: git made no commit for the checkpoint in %s", p.Machine.Ref(), p.Dir)
	}
	if _, err := p.git(ctx, "update-ref", ref, commit); err != nil {
		return "", err
	}
	p.push(ctx, ref)
	return commit, nil
}

// tree stages the tree's files into a temporary index inside the pod, ignored
// files excluded, and returns the tree object's id. Diff compares it with the
// base and the checkpoint commits it, which is what gitrepo.DiffWorktree and
// gitrepo.SnapshotWorktree do from the worktree's own index. The index here
// always starts from HEAD, gitrepo's fallback rather than its copy of the
// worktree's index: the copy is an optimisation for the host's shared folder,
// and that shortcut is not carried over to the pod.
func (p Pod) tree(ctx context.Context) (string, error) {
	script := `set -e
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export GIT_INDEX_FILE="$tmp/index"
` + p.gitLine(nil, "read-tree", "HEAD") + "\n" +
		p.gitLine(nil, "add", "--all") + "\n" +
		p.gitLine(nil, "write-tree") + "\n"
	out, err := p.exec(ctx, script)
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(out)
	if tree == "" {
		return "", fmt.Errorf("%s: git printed no tree for %s", p.Machine.Ref(), p.Dir)
	}
	return tree, nil
}

// push publishes a checkpoint to the tree's remote. A failure is a warning,
// not the checkpoint's: the commit is in the pod's repository, and its ref is
// what a later start would find there.
func (p Pod) push(ctx context.Context, ref string) {
	remote := p.Remote
	if remote == "" {
		remote = "origin"
	}
	if _, err := p.git(ctx, "push", "--quiet", remote, ref+":"+ref); err != nil && p.Log != nil {
		_, _ = fmt.Fprintf(p.Log, "==> %s: pushing checkpoint %s to %s failed, so it is only in the pod: %v\n", p.Machine.Ref(), ref, remote, err)
	}
}

// git runs one git command against the tree, as the pod's user.
func (p Pod) git(ctx context.Context, args ...string) (string, error) {
	return p.exec(ctx, p.gitLine(nil, args...))
}

// gitLine is the shell command line for one git command, with env in front of
// it and every argument quoted for the shell UserExec runs. GIT_TERMINAL_PROMPT
// is 0, so a credential that is missing fails instead of waiting for a prompt
// nobody can answer; the profile's token lives in the pod's credential helper,
// never in a URL, so nothing git prints can hold it.
func (p Pod) gitLine(env []string, args ...string) string {
	line := "GIT_TERMINAL_PROMPT=0"
	for _, e := range env {
		line += " " + e
	}
	line += " git -C " + quote(p.Dir)
	for _, a := range args {
		line += " " + quote(a)
	}
	return line
}

// exec runs one shell command in the pod as the tree's user, and returns what
// it wrote to stdout. A command that ran and failed carries what it said on
// stderr; one the pod never ran, because a stopped agent has no gateway
// session, says that starting the agent is what makes the tree readable again.
func (p Pod) exec(ctx context.Context, command string) (string, error) {
	var stdout, stderr strings.Builder
	err := p.Machine.UserExec(ctx, p.User, command, nil, &stdout, &stderr)
	if err == nil {
		return stdout.String(), nil
	}
	if detail := strings.TrimSpace(stderr.String()); detail != "" {
		return stdout.String(), fmt.Errorf("%w: %s", err, detail)
	}
	if _, ran := exitCode(err); !ran && ctx.Err() == nil {
		return stdout.String(), fmt.Errorf("%w (start the agent: its tree is on its pod's claim)", err)
	}
	return stdout.String(), err
}

// exitCode is the status a command that ran ended with: machine.UserExec
// reports one for both runtimes, as an error with an ExitCode method.
func exitCode(err error) (int, bool) {
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		return exit.ExitCode(), true
	}
	return 0, false
}

// quote quotes a value for the sh -lc command line UserExec runs.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
