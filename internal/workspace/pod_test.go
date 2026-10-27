package workspace_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentbox/internal/gitrepo"
	"agentbox/internal/machine"
	"agentbox/internal/testutil"
	"agentbox/internal/workspace"
)

// hostPod is a pod's machine in a test: UserExec runs the command on this host,
// against the very directory the test's Local workspace stands on. It records
// every command line, which is how a test asserts what a checkpoint did and, as
// importantly, did not run.
type hostPod struct {
	// Machine is embedded so hostPod is a machine.Machine: the workspace only
	// ever calls UserExec, and a method a test did not expect panics loudly.
	machine.Machine
	commands []string
}

func (m *hostPod) Ref() string { return "hello-stack/agent-01" }

func (m *hostPod) UserExec(ctx context.Context, user, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	m.commands = append(m.commands, command)
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}

// podFor is a Pod standing on local's directory through hostPod: the same tree
// seen by the two workspaces, which is what makes the parity checks parity.
func podFor(t *testing.T, dir string) (workspace.Pod, *hostPod) {
	t.Helper()
	fake := &hostPod{}
	return workspace.Pod{Machine: fake, Dir: dir, User: "agent", Log: io.Discard}, fake
}

// gite runs git without failing the test, for the checks that expect a failure.
func gite(dir string, args ...string) error {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return testutil.Git(t, dir, args...)
}

// TestPodDiffMatchesLocal is the parity the pod half is built for: a pod's diff
// is what Local's diff is, for a committed change, an uncommitted edit, an
// untracked file, the stat summary, a path filter and a base that no longer
// resolves.
func TestPodDiffMatchesLocal(t *testing.T) {
	ctx := context.Background()
	edit := func(t *testing.T, dir string) {
		write(t, filepath.Join(dir, "README.md"), "# agent work\n")
	}
	scenarios := []struct {
		name   string
		change func(t *testing.T, dir string)
		opts   func(base string) workspace.DiffOptions
	}{
		{
			name:   "a clean worktree",
			change: func(*testing.T, string) {},
			opts: func(base string) workspace.DiffOptions {
				return workspace.DiffOptions{BaseRef: "main", BaseCommit: base}
			},
		},
		{
			name:   "an uncommitted edit",
			change: edit,
			opts: func(base string) workspace.DiffOptions {
				return workspace.DiffOptions{BaseRef: "main", BaseCommit: base}
			},
		},
		{
			name: "an untracked file next to an ignored one",
			change: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "notes.md"), "untracked\n")
				write(t, filepath.Join(dir, ".env"), "TOKEN=ignored\n")
				write(t, filepath.Join(dir, "node_modules", "kept.txt"), "ignored\n")
			},
			opts: func(base string) workspace.DiffOptions {
				return workspace.DiffOptions{BaseRef: "main", BaseCommit: base}
			},
		},
		{
			name: "a commit on the branch",
			change: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "notes.md"), "committed\n")
				testutil.Git(t, dir, "add", "notes.md")
				testutil.Git(t, dir, "commit", "-q", "-m", "notes")
			},
			opts: func(base string) workspace.DiffOptions {
				return workspace.DiffOptions{BaseRef: "main", BaseCommit: base}
			},
		},
		{
			name:   "the stat summary",
			change: edit,
			opts: func(base string) workspace.DiffOptions {
				return workspace.DiffOptions{BaseRef: "main", BaseCommit: base, Stat: true}
			},
		},
		{
			name: "one path",
			change: func(t *testing.T, dir string) {
				edit(t, dir)
				write(t, filepath.Join(dir, "notes.md"), "untracked\n")
			},
			opts: func(base string) workspace.DiffOptions {
				return workspace.DiffOptions{BaseRef: "main", BaseCommit: base, Paths: []string{"notes.md"}}
			},
		},
		{
			name:   "a base ref that no longer resolves",
			change: edit,
			opts: func(base string) workspace.DiffOptions {
				return workspace.DiffOptions{BaseRef: "gone", BaseCommit: base}
			},
		},
		{
			name:   "no base ref at all",
			change: edit,
			opts:   func(base string) workspace.DiffOptions { return workspace.DiffOptions{BaseCommit: base} },
		},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			_, local := setup(t)
			base := gitOut(t, local.Path(), "rev-parse", "HEAD")
			sc.change(t, local.Path())
			pod, _ := podFor(t, local.Path())

			want, err := local.Diff(ctx, sc.opts(base))
			if err != nil {
				t.Fatalf("Local.Diff() = %v", err)
			}
			got, err := pod.Diff(ctx, sc.opts(base))
			if err != nil {
				t.Fatalf("Pod.Diff() = %v, want Local's %q", err, want)
			}
			if got != want {
				t.Errorf("Pod.Diff() =\n%s\nwant Local's\n%s", got, want)
			}
		})
	}
}

// TestPodFilesMatchLocal: the same listing, in git's order, and the same answer
// to a limit that cuts it short.
func TestPodFilesMatchLocal(t *testing.T) {
	ctx := context.Background()
	_, local := setup(t)
	write(t, filepath.Join(local.Path(), "notes.md"), "new\n")
	write(t, filepath.Join(local.Path(), ".env"), "ignored\n")
	write(t, filepath.Join(local.Path(), "node_modules", "kept.txt"), "ignored\n")
	pod, _ := podFor(t, local.Path())

	for _, limit := range []int{1000, 1, 0} {
		want, wantMore, err := local.Files(ctx, limit)
		if err != nil {
			t.Fatalf("Local.Files(%d) = %v", limit, err)
		}
		got, gotMore, err := pod.Files(ctx, limit)
		if err != nil {
			t.Fatalf("Pod.Files(%d) = %v", limit, err)
		}
		if !reflect.DeepEqual(got, want) || gotMore != wantMore {
			t.Errorf("Pod.Files(%d) = %v, %v; want Local's %v, %v", limit, got, gotMore, want, wantMore)
		}
	}
}

// TestPodReadFileMatchesLocal: the file's bytes come from the pod, unchanged,
// including bytes a terminal would swallow; and neither workspace reads outside
// the tree.
func TestPodReadFileMatchesLocal(t *testing.T) {
	ctx := context.Background()
	_, local := setup(t)
	binary := []byte{0x00, 0x01, 0xff, '\n', 0x00, 'x'}
	if err := os.WriteFile(filepath.Join(local.Path(), "blob.bin"), binary, 0o644); err != nil {
		t.Fatal(err)
	}
	pod, _ := podFor(t, local.Path())

	for _, name := range []string{"README.md", "blob.bin", "scripts"} {
		want, wantErr := local.ReadFile(ctx, name)
		got, gotErr := pod.ReadFile(ctx, name)
		if wantErr != nil {
			if gotErr == nil {
				t.Errorf("Pod.ReadFile(%q) = %q, want Local's error %v", name, got, wantErr)
			}
			continue
		}
		if gotErr != nil {
			t.Errorf("Pod.ReadFile(%q) = %v, want Local's %q", name, gotErr, want)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("Pod.ReadFile(%q) = %q, want Local's %q", name, got, want)
		}
	}
	for _, bad := range []string{"", ".", "..", "../outside", "/etc/passwd", "scripts/../../outside"} {
		if _, err := local.ReadFile(ctx, bad); err == nil {
			t.Fatalf("Local.ReadFile(%q) succeeded, so this test can't show the pod refuses it", bad)
		}
		if _, err := pod.ReadFile(ctx, bad); err == nil {
			t.Errorf("Pod.ReadFile(%q) succeeded, want it refused as Local refuses it", bad)
		}
	}
	if _, err := pod.ReadFile(ctx, "nope.md"); err == nil {
		t.Error("Pod.ReadFile(nope.md) succeeded, want cat's failure")
	}
}

// TestPodStatusFollowsTheTree: HEAD resolving is the tree being there, and
// `git status --porcelain` is whether it has changes. A directory the pod's git
// can't read is not a tree, and an unreachable pod is an error, never a tree
// that is gone.
func TestPodStatusFollowsTheTree(t *testing.T) {
	ctx := context.Background()
	_, local := setup(t)
	pod, _ := podFor(t, local.Path())

	st, err := pod.Status(ctx)
	if err != nil || !st.Exists || st.Dirty {
		t.Fatalf("Pod.Status() on a fresh tree = %+v, %v, want there and clean", st, err)
	}
	write(t, filepath.Join(local.Path(), "notes.md"), "one\n")
	if st, err = pod.Status(ctx); err != nil || !st.Exists || !st.Dirty {
		t.Fatalf("Pod.Status() after a new file = %+v, %v, want there and dirty", st, err)
	}

	empty, _ := podFor(t, t.TempDir())
	if st, err = empty.Status(ctx); err != nil || st.Exists {
		t.Errorf("Pod.Status() on a volume with no clone = %+v, %v, want not there without an error", st, err)
	}

	unreachable := workspace.Pod{Machine: downPod{}, Dir: "/workspace", User: "agent"}
	if _, err := unreachable.Status(ctx); err == nil {
		t.Error("Pod.Status() on a pod that isn't connected = nil, want the gateway's error")
	}
}

// downPod is a pod with no gateway session: what a stopped agent looks like to
// everything on this side.
type downPod struct{ machine.Machine }

func (downPod) Ref() string { return "hello-stack/agent-01" }

func (downPod) UserExec(context.Context, string, string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("hello-stack/agent-01: the agent's pod isn't connected")
}

// TestPodSaysToStartAStoppedAgent: a stopped pod's tree cannot be read, and the
// failure says what brings it back instead of pretending the work is gone.
func TestPodSaysToStartAStoppedAgent(t *testing.T) {
	ctx := context.Background()
	pod := workspace.Pod{Machine: downPod{}, Dir: "/workspace", User: "agent"}
	_, err := pod.Diff(ctx, workspace.DiffOptions{BaseRef: "main", BaseCommit: "main"})
	if err == nil || !strings.Contains(err.Error(), "start the agent: its tree is on its pod's claim") {
		t.Errorf("Pod.Diff() on a stopped pod = %v, want it to say the tree is on the claim and the agent must start", err)
	}
}

// TestPodCheckpointLeavesTheBranchAndIndexAlone is the interface's contract on a
// pod: a temporary index inside it, no commit on the branch, nothing staged, the
// files still dirty, and the ref on a commit of them.
func TestPodCheckpointLeavesTheBranchAndIndexAlone(t *testing.T) {
	ctx := context.Background()
	repo, local := setup(t)
	dir := local.Path()
	base := gitOut(t, dir, "rev-parse", "HEAD")
	write(t, filepath.Join(dir, "README.md"), "# agent work\n")
	testutil.Git(t, dir, "add", "README.md")
	write(t, filepath.Join(dir, "notes.md"), "untracked\n")
	write(t, filepath.Join(dir, "node_modules", "kept.txt"), "ignored\n")
	status := gitOut(t, dir, "status", "--porcelain")

	pod, fake := podFor(t, dir)
	const ref = "refs/agentbox/snapshots/agent-01/turn-1"
	commit, err := pod.Checkpoint(ctx, ref, "agentbox checkpoint hello-stack/agent-01@turn-1")
	if err != nil {
		t.Fatal(err)
	}

	if got := gitOut(t, dir, "rev-parse", "HEAD"); got != base {
		t.Errorf("HEAD = %q after a checkpoint, want %q", got, base)
	}
	if got := gitOut(t, dir, "rev-parse", "refs/heads/agentbox/fix-login"); got != base {
		t.Errorf("the branch = %q after a checkpoint, want %q: a checkpoint never moves it", got, base)
	}
	if got := gitOut(t, dir, "status", "--porcelain"); got != status {
		t.Errorf("status after a checkpoint = %q, want the index and files as they were: %q", got, status)
	}
	if got := gitOut(t, dir, "rev-parse", commit+"^"); got != base {
		t.Errorf("the checkpoint's parent = %q, want the branch's commit %q", got, base)
	}
	if got := gitOut(t, dir, "show", commit+":README.md"); got != "# agent work" {
		t.Errorf("the checkpoint's README.md = %q, want the tree's", got)
	}
	if got := gitOut(t, dir, "show", commit+":notes.md"); got != "untracked" {
		t.Errorf("the checkpoint's notes.md = %q, want the untracked file's", got)
	}
	if err := gite(dir, "cat-file", "-e", commit+":node_modules/kept.txt"); err == nil {
		t.Error("the checkpoint recorded an ignored file")
	}
	if got, err := repo.ResolveRef(ref); err != nil || got != commit {
		t.Errorf("the ref = %q, %v, want %q", got, err, commit)
	}
	// The branch is never committed to: the commit is made with commit-tree on
	// a tree, which is what leaves the branch and index alone.
	for _, command := range fake.commands {
		for _, arg := range strings.Fields(command) {
			if arg == "commit" {
				t.Errorf("a checkpoint ran a git commit: %s", command)
			}
		}
	}
}

// TestPodCheckpointPushesTheRef: a pod's checkpoint is published to the profile's
// remote, so the work outlives the pod.
func TestPodCheckpointPushesTheRef(t *testing.T) {
	ctx := context.Background()
	_, local := setup(t)
	dir := local.Path()
	bare := filepath.Join(t.TempDir(), "remote.git")
	testutil.Git(t, t.TempDir(), "init", "--quiet", "--bare", bare)
	testutil.Git(t, dir, "remote", "add", "origin", bare)
	write(t, filepath.Join(dir, "notes.md"), "pushed\n")

	pod, _ := podFor(t, dir)
	const ref = "refs/agentbox/snapshots/agent-01/turn-1"
	commit, err := pod.Checkpoint(ctx, ref, "agentbox checkpoint hello-stack/agent-01@turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := gitOut(t, bare, "rev-parse", ref); got != commit {
		t.Errorf("the remote's %s = %q, want the checkpoint %q", ref, got, commit)
	}
}

// TestPodCheckpointSurvivesARemoteThatRefuses: the push is a warning. A tree
// whose project has no remote, or whose login cannot push yet, still has its
// checkpoint inside the pod.
func TestPodCheckpointSurvivesARemoteThatRefuses(t *testing.T) {
	ctx := context.Background()
	repo, local := setup(t)
	write(t, filepath.Join(local.Path(), "notes.md"), "only here\n")
	var log strings.Builder
	fake := &hostPod{}
	pod := workspace.Pod{Machine: fake, Dir: local.Path(), User: "agent", Log: &log}

	const ref = "refs/agentbox/snapshots/agent-01/turn-1"
	commit, err := pod.Checkpoint(ctx, ref, "agentbox checkpoint hello-stack/agent-01@turn-1")
	if err != nil {
		t.Fatalf("Pod.Checkpoint() = %v, want the checkpoint kept when the push fails", err)
	}
	if commit == "" {
		t.Fatal("Pod.Checkpoint() returned no commit")
	}
	if got, err := repo.ResolveRef(ref); err != nil || got != commit {
		t.Errorf("the ref = %q, %v, want the commit %q", got, err, commit)
	}
	if !strings.Contains(log.String(), "pushing checkpoint") {
		t.Errorf("the failed push was not logged: %q", log.String())
	}
}

// TestPodRestoreMatchesLocal: a pod's restore ends where Local's does, branch
// and files, and it keeps what git ignores, since a pod has no second disk half
// to put back.
func TestPodRestoreMatchesLocal(t *testing.T) {
	ctx := context.Background()
	// prepare runs the same sequence against any tree: a checkpoint, work after
	// it, and ignored files.
	prepare := func(t *testing.T, dir string, w workspace.Workspace) string {
		t.Helper()
		write(t, filepath.Join(dir, "notes.md"), "at the checkpoint\n")
		commit, err := w.Checkpoint(ctx, "refs/agentbox/snapshots/agent-01/turn-1", "checkpoint")
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "README.md"), "# later work\n")
		testutil.Git(t, dir, "add", "README.md")
		testutil.Git(t, dir, "commit", "-q", "-m", "later")
		write(t, filepath.Join(dir, "later.md"), "later\n")
		write(t, filepath.Join(dir, "node_modules", "kept.txt"), "kept\n")
		write(t, filepath.Join(dir, ".env"), "TOKEN=kept\n")
		return commit
	}

	_, local := setup(t)
	localDir := local.Path()
	localCommit := prepare(t, localDir, local)
	if err := local.Restore(ctx, localCommit); err != nil {
		t.Fatal(err)
	}

	_, podLocal := setup(t)
	podDir := podLocal.Path()
	pod, _ := podFor(t, podDir)
	podCommit := prepare(t, podDir, pod)
	if err := pod.Restore(ctx, podCommit); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"notes.md", "README.md"} {
		if got, want := read(t, filepath.Join(podDir, name)), read(t, filepath.Join(localDir, name)); got != want {
			t.Errorf("%s after the pod's restore = %q, want Local's %q", name, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(podDir, "later.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("later.md outlived the pod's restore: %v", err)
	}
	if got := read(t, filepath.Join(podDir, "node_modules", "kept.txt")); got != "kept\n" {
		t.Error("the pod's restore removed an ignored file")
	}
	if got := read(t, filepath.Join(podDir, ".env")); got != "TOKEN=kept\n" {
		t.Error("the pod's restore removed the project's env file")
	}
	if got, want := gitOut(t, podDir, "status", "--porcelain"), gitOut(t, localDir, "status", "--porcelain"); got != want {
		t.Errorf("status after the pod's restore = %q, want Local's %q", got, want)
	}
}

// TestPodResolveCommitAndDeleteRef: the two operations the checkpoint code
// needs, against the pod's own repository.
func TestPodResolveCommitAndDeleteRef(t *testing.T) {
	ctx := context.Background()
	repo, local := setup(t)
	base := gitOut(t, local.Path(), "rev-parse", "HEAD")
	pod, _ := podFor(t, local.Path())

	for _, rev := range []string{"HEAD", "refs/heads/agentbox/fix-login", "agentbox/fix-login"} {
		want, err := local.ResolveCommit(ctx, rev)
		if err != nil {
			t.Fatalf("Local.ResolveCommit(%q) = %v", rev, err)
		}
		got, err := pod.ResolveCommit(ctx, rev)
		if err != nil || got != want || want != base {
			t.Errorf("Pod.ResolveCommit(%q) = %q, %v; want %q", rev, got, err, want)
		}
	}
	if _, err := pod.ResolveCommit(ctx, "no-such-ref"); err == nil {
		t.Error("Pod.ResolveCommit(no-such-ref) succeeded, want it refused")
	}

	const ref = "refs/agentbox/snapshots/agent-01/turn-1"
	if _, err := local.Checkpoint(ctx, ref, "checkpoint"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveRef(ref); err != nil {
		t.Fatalf("the checkpoint's ref is not in the repository: %v", err)
	}
	if err := pod.DeleteRef(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveRef(ref); err == nil {
		t.Error("the ref outlived Pod.DeleteRef")
	}
}

// TestPodPrepareRefusesAndDestroyIsTheClusters: the tree is made when the agent
// is configured, not here, and the claim is the cluster's to delete.
func TestPodPrepareRefusesAndDestroyIsTheClusters(t *testing.T) {
	ctx := context.Background()
	repo := gitrepo.Repo{Root: t.TempDir()}
	fake := &hostPod{}
	pod := workspace.Pod{Machine: fake, Dir: "/workspace", User: "agent"}
	if got := pod.Path(); got != "" {
		t.Errorf("Path() = %q, want \"\" for a tree that is not on this host", got)
	}
	if err := pod.Prepare(ctx, repo, workspace.PrepareOptions{Branch: "agentbox/agent-01", Base: "HEAD"}); err == nil {
		t.Error("Pod.Prepare() = nil, want it refused: the pod clones its own tree at configure time")
	} else if !strings.Contains(err.Error(), "cloned inside") {
		t.Errorf("Pod.Prepare() = %v, want it to say the tree is cloned inside the pod", err)
	}
	if err := pod.Destroy(ctx, repo); err != nil {
		t.Errorf("Pod.Destroy() = %v, want nothing: the claim is the cluster's", err)
	}
	if len(fake.commands) != 0 {
		t.Errorf("Prepare and Destroy ran commands in the pod: %v", fake.commands)
	}
}
