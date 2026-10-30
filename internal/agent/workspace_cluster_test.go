package agent

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	mach "agentbox/internal/machine"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// treePod is the fixture's pod with its commands run on this host: the machine a
// cluster agent's workspace is pointed at here, so a checkpoint, a rollback and
// a diff meet a real repository while still going through the machine's
// UserExec, which is the path a pod's git commands take. Everything but
// UserExec is the fixture's podMachineStub.
type treePod struct {
	*podMachineStub
}

func (m *treePod) UserExec(ctx context.Context, user, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	m.userExecs = append(m.userExecs, command)
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}

// treeRuntime is a cluster whose agents' pods run their commands here.
type treeRuntime struct{ m mach.Machine }

func (r treeRuntime) Kind() mach.Kind             { return mach.KindKubernetes }
func (r treeRuntime) Machine(string) mach.Machine { return r.m }

// clusterTree is a cluster agent with a real tree behind its pod: the row the
// manager reads, a worktree standing for the clone on the pod's claim, and the
// fixture's pod with its commands run on this host.
func clusterTree(t *testing.T) (clusterFixture, state.Agent, string) {
	t.Helper()
	f := newClusterFixture(t, nil)
	dir := filepath.Join(t.TempDir(), "agent-01")
	if err := f.repo.AddWorktree(dir, "agentbox/agent-01", "HEAD", "agentbox: hello-stack/agent-01"); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{
		Project:    "hello-stack",
		Name:       "agent-01",
		Instance:   InstanceName("hello-stack", "agent-01"),
		Branch:     "agentbox/agent-01",
		BaseRef:    f.repo.CurrentBranch(),
		BaseCommit: testutil.Git(t, dir, "rev-parse", "HEAD"),
		Cluster:    "k3d", Runtime: state.RuntimeKubernetes, Workspace: dir,
		Status: state.AgentReady, CreatedAt: time.Now(), DesiredState: state.DesiredRunning,
	}
	if err := f.st.AddAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	f.m.Kubernetes = treeRuntime{m: &treePod{podMachineStub: f.pod}}
	return f, a, dir
}

// ranCommand reports whether one of the commands holds all of parts, which is
// how a test names a git command without reproducing the shell quoting.
func ranCommand(commands []string, parts ...string) bool {
	for _, command := range commands {
		all := true
		for _, part := range parts {
			if !strings.Contains(command, part) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func writeTree(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestClusterAgentWorkspaceRunsInItsPod is the agent half of the pod workspace:
// a cluster agent's tree is read through its machine, so a checkpoint, a diff,
// a dropped checkpoint and a rollback are the pod's git commands. The branch and
// the index are left where a checkpoint found them.
func TestClusterAgentWorkspaceRunsInItsPod(t *testing.T) {
	f, a, dir := clusterTree(t)
	ctx := context.Background()
	head := testutil.Git(t, dir, "rev-parse", "HEAD")

	writeTree(t, filepath.Join(dir, "README.md"), "# from the pod\n")
	writeTree(t, filepath.Join(dir, "notes.md"), "turn one\n")
	cp, err := f.m.Checkpoint(ctx, a, "u1", 1, "Write the notes")
	if err != nil {
		t.Fatalf("Checkpoint() = %v", err)
	}
	if cp.Commit == "" || cp.Head != head {
		t.Fatalf("Checkpoint() = %+v, want a commit whose parent is %s", cp, head)
	}
	if got := testutil.Git(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("the branch moved to %q on a checkpoint, want it still at %q", got, head)
	}
	if !ranCommand(f.pod.userExecs, "write-tree") || !ranCommand(f.pod.userExecs, "commit-tree") ||
		!ranCommand(f.pod.userExecs, "update-ref", cp.Ref) {
		t.Errorf("the checkpoint didn't run the pod's git commands: %v", f.pod.userExecs)
	}
	if ranCommand(f.pod.userExecs, " commit ") {
		t.Errorf("the checkpoint ran a git commit, which moves the branch: %v", f.pod.userExecs)
	}

	// Diff is the pod's, with the checkpoint's uncommitted files in it.
	diff, err := f.m.Diff(a, nil, true)
	if err != nil {
		t.Fatalf("Diff() = %v", err)
	}
	if !strings.Contains(diff, "notes.md") || !strings.Contains(diff, "README.md") {
		t.Errorf("Diff() = %q, want the pod's uncommitted files", diff)
	}
	// Files is the pod's too: what the composer's @ mentions list.
	files, _, err := f.m.Files(ctx, a, 100)
	if err != nil {
		t.Fatalf("Files() = %v", err)
	}
	if !slices.Contains(files, "notes.md") {
		t.Errorf("Files() = %v, want the pod's untracked notes.md", files)
	}

	// A turn after it, then a rollback: the pre-restore checkpoint and the
	// restore are the pod's as well.
	writeTree(t, filepath.Join(dir, "notes.md"), "turn two\n")
	two, err := f.m.Checkpoint(ctx, a, "u2", 2, "Change them")
	if err != nil {
		t.Fatalf("Checkpoint(turn 2) = %v", err)
	}
	saved, err := f.m.RollBack(ctx, a, cp, "the whole conversation")
	if err != nil {
		t.Fatalf("RollBack() = %v", err)
	}
	if saved.Kind != state.CheckpointSaved || saved.Commit == "" {
		t.Errorf("RollBack() saved %+v, want a saved checkpoint with a commit", saved)
	}
	if _, err := f.repo.ResolveRef(saved.Ref); err != nil {
		t.Errorf("the pre-restore checkpoint isn't in the pod's repository: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "notes.md")); string(got) != "turn one\n" {
		t.Errorf("notes.md after the rollback = %q, want the checkpoint's", got)
	}
	if got := testutil.Git(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("the branch is at %q after the rollback, want %q", got, head)
	}
	if _, err := f.repo.ResolveRef(two.Ref); err == nil {
		t.Error("turn 2's ref outlived the rollback")
	}

	// Dropping a checkpoint deletes its ref through the pod too, never in a
	// repository on this host.
	three, err := f.m.Checkpoint(ctx, a, "u3", 1, "Write it again")
	if err != nil {
		t.Fatalf("Checkpoint(turn 3) = %v", err)
	}
	before := len(f.pod.userExecs)
	f.m.dropCheckpoint(ctx, a, three)
	if _, err := f.repo.ResolveRef(three.Ref); err == nil {
		t.Error("the dropped checkpoint's ref is still in the repository")
	}
	if !ranCommand(f.pod.userExecs[before:], "update-ref", three.Ref) {
		t.Errorf("DropCheckpoint didn't run in the pod: %v", f.pod.userExecs[before:])
	}
}
