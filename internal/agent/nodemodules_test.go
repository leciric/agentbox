package agent_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/nodemods"
	"agentbox/internal/state"
)

// nodeModulesWorktree is a git worktree with an app and a fixture no lockfile
// covers, and an older node_modules of its own in the app it already had.
func nodeModulesWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"app/package.json":                         `{"name":"app"}`,
		"app/package-lock.json":                    `{"lockfileVersion":3}`,
		"testdata/fixture/package.json":            `{}`,
		"tools/package.json":                       `{}`,
		"tools/pnpm-lock.yaml":                     "lockfileVersion: '9.0'\n",
		"tools/node_modules/.modules.yaml":         "",
		"tools/node_modules/already-here/index.js": "",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "app", "testdata", "tools/package.json", "tools/pnpm-lock.yaml"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func TestNodeModulesAreKeptForTheNextAgent(t *testing.T) {
	f, calls := packageCacheFixture(t, `{}`)
	store := &nodemods.Store{Dir: t.TempDir()}
	f.m.NodeModules = store
	f.m.NodeModulesOn = func(context.Context) bool { return true }
	ctx := context.Background()

	first := state.Agent{Project: "p", Name: "agent-1", Instance: "ab-p-agent-1", Worktree: nodeModulesWorktree(t)}
	f.m.EnsureNodeModules(ctx, first)
	got := strings.Join(calls(), "\n")
	want := "config device add ab-p-agent-1 node-modules-0 disk source=" + store.MountDir(first.Instance, "app") + " path=" + filepath.Join(first.Worktree, "app", "node_modules")
	if !strings.Contains(got, want) {
		t.Fatalf("no %q in:\n%s", want, got)
	}
	// Not the fixture, which no lockfile covers, nor tools/, whose node_modules
	// the worktree already has.
	if n := strings.Count(got, "config device add"); n != 1 {
		t.Errorf("%d devices added:\n%s", n, got)
	}

	// The agent installs, and is destroyed.
	installed := store.MountDir(first.Instance, "app")
	for _, name := range []string{"left-pad/index.js", ".package-lock.json"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(installed, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(installed, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.m.KeepNodeModules(first)
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Errorf("the destroyed agent's node_modules are still there: %v", err)
	}

	second := state.Agent{Project: "p", Name: "agent-2", Instance: "ab-p-agent-2", Worktree: nodeModulesWorktree(t)}
	f.m.EnsureNodeModules(ctx, second)
	if _, err := os.Stat(filepath.Join(store.MountDir(second.Instance, "app"), "left-pad", "index.js")); err != nil {
		t.Errorf("the next agent didn't get the install: %v", err)
	}
}

// Off, a machine copied from an agent that had them loses its devices.
func TestNodeModulesOffRemovesTheDevices(t *testing.T) {
	f, calls := packageCacheFixture(t, `{"node-modules-0": {"type": "disk", "source": "/vm/x", "path": "/w/app/node_modules"}}`)
	f.m.NodeModules = &nodemods.Store{Dir: t.TempDir()}
	f.m.NodeModulesOn = func(context.Context) bool { return false }
	f.m.EnsureNodeModules(context.Background(), state.Agent{Instance: "ab-p-agent-1", Worktree: nodeModulesWorktree(t)})
	if got := strings.Join(calls(), "\n"); !strings.Contains(got, "config device remove ab-p-agent-1 node-modules-0") {
		t.Errorf("the device wasn't removed:\n%s", got)
	}
}

// Destroy keeps what the agent installed before it's archived, from when a
// trim would take its directories for a gone agent's.
func TestDestroyKeepsTheInstall(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]' ;;
  delete) exit 0 ;;
esac`))
	a := destroyFixture(t, f)
	store := &nodemods.Store{Dir: t.TempDir()}
	f.m.NodeModules = store
	g := nodemods.Group{Lockfile: "package-lock.json", Dirs: []string{"."}}
	lock := []byte(`{"lockfileVersion":3}`)
	if err := os.WriteFile(filepath.Join(a.Worktree, "package-lock.json"), lock, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.MountDir(a.Instance, "."), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.AgentFile(a.Instance, "installs.json"), []byte(`{"groups":[{"lockfile":"package-lock.json","dirs":["."]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.MountDir(a.Instance, "."), ".package-lock.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if !store.HasSnapshot(a.Project, nodemods.Key(g, lock, "")) {
		t.Error("Destroy didn't keep the install")
	}
}

// A stopped agent's machine can't hand back what container root made in its
// node_modules, which a daemon that isn't root then can't keep or delete:
// Destroy starts it for that, and only when something there isn't the user's.
func TestDestroyHandsBackAStoppedAgentsNodeModules(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root deletes anything: nothing to hand back")
	}
	for name, foreign := range map[string]bool{"root's files": true, "all the user's": false} {
		t.Run(name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "calls")
			f := setup(t, fakeIncus(t, `echo "$*" >> `+log+`
case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]' ;;
esac`))
			a := destroyFixture(t, f)
			store := &nodemods.Store{Dir: t.TempDir()}
			f.m.NodeModules = store
			if err := os.MkdirAll(store.MountDir(a.Instance, "."), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(store.AgentFile(a.Instance, "installs.json"), []byte(`{"groups":[{"lockfile":"package-lock.json","dirs":["."]}]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			// Files that aren't the user's are made by container root; here,
			// the user is someone else.
			f.m.User.UID = os.Getuid()
			if foreign {
				f.m.User.UID++
			}
			if err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true}); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(log)
			calls := string(b)
			chown := fmt.Sprintf("exec ab-hello-stack-agent-01 -- chown -R %d:%d %s", f.m.User.UID, f.m.User.GID, filepath.Join(a.Worktree, "node_modules"))
			started := strings.Contains(calls, "start ab-hello-stack-agent-01")
			if started != foreign || strings.Contains(calls, chown) != foreign {
				t.Errorf("started %v, want %v; calls:\n%s", started, foreign, calls)
			}
			if foreign && strings.Index(calls, chown) > strings.Index(calls, "delete") {
				t.Errorf("handed back after the delete:\n%s", calls)
			}
		})
	}
}
