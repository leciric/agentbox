//go:build integration

package agent_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// These tests need Incus, scripts/host-setup.sh and a built base image:
//
//	agentbox image build && go test -tags integration ./internal/agent

func TestAgentsOnIncus(t *testing.T) {
	ctx := context.Background()
	f := incusFixture(t)

	var agents []state.Agent
	for range 2 {
		agents = append(agents, f.create(t, agent.CreateOptions{AI: "none"}))
	}

	statuses, err := f.m.List(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range statuses {
		if s.State != "running" || s.IP == "" {
			t.Fatalf("%s: state %q, ip %q", s.Ref(), s.State, s.IP)
		}
		sh(t, f.m, s.Agent, "tmux new-window -d -t main -n dev 'pnpm dev'")
	}
	for _, s := range statuses {
		// Both agents serve port 3000 at the same time.
		if body := waitHTTP(t, "http://"+s.IP+":3000"); !strings.Contains(body, s.Instance) {
			t.Errorf("%s:3000 served %q, want host %s", s.IP, body, s.Instance)
		}
	}

	a := agents[0]
	sh(t, f.m, a, "touch from-agent")
	info, err := os.Stat(filepath.Join(a.Worktree, "from-agent"))
	if err != nil {
		t.Fatal(err)
	}
	if owner := info.Sys().(*syscall.Stat_t).Uid; int(owner) != f.m.User.UID {
		t.Errorf("file written by the agent is owned by uid %d, want %d", owner, f.m.User.UID)
	}
	if diff, err := f.m.Diff(a, true); err != nil || !strings.Contains(diff, "from-agent") {
		t.Errorf("Diff() = %q, %v; want it to list from-agent", diff, err)
	}
	if err := f.m.Destroy(ctx, a, agent.DestroyOptions{}); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("Destroy() of a dirty agent: got %v, want a refusal", err)
	}
}

func TestProjectBaseOnIncus(t *testing.T) {
	ctx := context.Background()
	f := incusFixture(t)

	prepared := f.create(t, agent.CreateOptions{AI: "none"})
	sh(t, f.m, prepared, "echo set-up > ~/prepared.txt && echo scratch > scratch.txt")

	base, err := f.m.SaveBase(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.m.RemoveBase(context.Background(), "hello-stack") })
	if base.SavedFrom != prepared.Ref() {
		t.Errorf("SavedFrom = %q, want %q", base.SavedFrom, prepared.Ref())
	}
	if got := sh(t, f.m, prepared, "cat ~/prepared.txt"); got != "set-up" {
		t.Errorf("saving the base disturbed the agent: %q", got)
	}

	fromBase := f.create(t, agent.CreateOptions{AI: "none"})
	if fromBase.Source != base.SnapshotRef() {
		t.Errorf("Source = %q, want %q", fromBase.Source, base.SnapshotRef())
	}
	if got := sh(t, f.m, fromBase, "cat ~/prepared.txt"); got != "set-up" {
		t.Errorf("home directory from the base: %q", got)
	}
	if got := sh(t, f.m, fromBase, "test -e scratch.txt && echo leaked || echo fresh"); got != "fresh" {
		t.Error("the saved agent's worktree leaked into the new agent")
	}
	if got := sh(t, f.m, fromBase, "sed -n 's/^- Agent: //p' ~/AGENTBOX.md"); got != fromBase.Name {
		t.Errorf("the new agent's brief is for %q", got)
	}
	if sh(t, f.m, prepared, "cat /etc/machine-id") == sh(t, f.m, fromBase, "cat /etc/machine-id") {
		t.Error("agents share a machine-id")
	}

	clean := f.create(t, agent.CreateOptions{AI: "none", Clean: true})
	if clean.Source != image.SnapshotRef() {
		t.Errorf("Clean: Source = %q, want %q", clean.Source, image.SnapshotRef())
	}
	if got := sh(t, f.m, clean, "test -e ~/prepared.txt && echo yes || echo no"); got != "no" {
		t.Error("an agent created with Clean has files from the project base")
	}
}

func TestSnapshotsOnIncus(t *testing.T) {
	ctx := context.Background()
	f := incusFixture(t)
	a := f.create(t, agent.CreateOptions{AI: "none"})
	const commit = `git -c user.name=test -c user.email=test@agentbox.invalid commit -qam`

	// What a snapshot must capture: a commit, an uncommitted edit, an untracked
	// file, and a file outside the worktree.
	sh(t, f.m, a, "echo committed >> message.txt && "+commit+" 'agent commit' && echo uncommitted >> message.txt && echo untracked > notes.txt && echo before > ~/marker.txt")
	head := sh(t, f.m, a, "git rev-parse HEAD")
	if _, err := f.m.Snapshot(ctx, a, "before", true); err != nil {
		t.Fatal(err)
	}

	sh(t, f.m, a, "rm server.mjs notes.txt && "+commit+" 'break everything' && echo after > ~/marker.txt")
	if err := f.m.Restore(ctx, a, "before"); err != nil {
		t.Fatal(err)
	}
	if got := sh(t, f.m, a, "git rev-parse HEAD"); got != head {
		t.Errorf("HEAD after restore = %s, want %s", got, head)
	}
	const want = "uncommitted\nuntracked\nserver.mjs\nbefore"
	if got := sh(t, f.m, a, "tail -1 message.txt; cat notes.txt; ls server.mjs; cat ~/marker.txt"); got != want {
		t.Errorf("after restore:\n%s\nwant:\n%s", got, want)
	}

	snapshots, err := f.m.Snapshots(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range snapshots {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "initial,before" {
		t.Errorf("Snapshots() = %v, want [initial before]", names)
	}

	fork, err := f.m.Fork(ctx, a, agent.ForkOptions{Snapshot: "before"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.m.Destroy(context.Background(), fork, agent.DestroyOptions{Force: true, DeleteBranch: true})
	})
	if fork.Source != a.Instance+"/before" || fork.BaseCommit != head {
		t.Errorf("fork source %q, base %s; want %s/before, %s", fork.Source, fork.BaseCommit, a.Instance, head)
	}
	if got := sh(t, f.m, fork, "tail -1 message.txt; cat notes.txt; ls server.mjs; cat ~/marker.txt"); got != want {
		t.Errorf("fork files:\n%s\nwant:\n%s", got, want)
	}

	if err := f.m.Pause(ctx, a); err != nil {
		t.Fatal(err)
	}
	if statuses, _ := f.m.List(ctx, "hello-stack"); statuses[0].State != "paused" {
		t.Errorf("state after pause = %q", statuses[0].State)
	}
	host, usage, err := f.m.Usage(ctx, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if host.MemTotal == 0 || host.Cores == 0 || len(usage) != 2 || usage[0].Memory == 0 {
		t.Errorf("Usage() = %+v, %+v", host, usage)
	}
	if err := f.m.Resume(ctx, a); err != nil {
		t.Fatal(err)
	}

	if err := f.m.DeleteSnapshot(ctx, a, "before"); err != nil {
		t.Fatal(err)
	}
	if snapshots, _ := f.m.Snapshots(ctx, a); len(snapshots) != 1 || snapshots[0].Name != "initial" {
		t.Errorf("Snapshots() after delete = %+v", snapshots)
	}
}

func incusFixture(t *testing.T) fixture {
	t.Helper()
	inc := incus.Client{}
	if ready, err := image.Ready(context.Background(), inc); err != nil || !ready {
		t.Skipf("base image not built (ready=%v, err=%v)", ready, err)
	}
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	f := setup(t, inc)
	f.m.User = image.User{Name: u.Username, UID: uid, GID: gid}
	f.m.Log = testLog{t}
	return f
}

// create makes an agent that is destroyed when the test ends.
func (f fixture) create(t *testing.T, opts agent.CreateOptions) state.Agent {
	t.Helper()
	a, err := f.m.Create(context.Background(), "hello-stack", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true, DeleteBranch: true})
	})
	return a
}

// sh runs a command in the agent's worktree and returns its trimmed output.
func sh(t *testing.T, m *agent.Manager, a state.Agent, command string) string {
	t.Helper()
	var out bytes.Buffer
	if err := m.Exec(context.Background(), a, command, nil, &out, &out); err != nil {
		t.Fatalf("%s: %v: %s", command, err, out.String())
	}
	return strings.TrimSpace(out.String())
}

func waitHTTP(t *testing.T, url string) string {
	t.Helper()
	client := http.Client{Timeout: 2 * time.Second}
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(body)
	}
	t.Fatalf("%s never answered", url)
	return ""
}

type testLog struct{ t *testing.T }

func (l testLog) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}
