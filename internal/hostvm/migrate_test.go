package hostvm

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/incus"
	"agentbox/internal/paths"
	"agentbox/internal/state"
)

// hostStateDB makes a host-mode state.db at p with two projects, an agent
// each, and shop's chat (its lead), as `agentbox` itself would.
func hostStateDB(t *testing.T, p paths.Paths) {
	t.Helper()
	st, err := state.Open(p.StateDB())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	for _, project := range []string{"shop", "blog"} {
		if err := st.AddProject(ctx, state.Project{Name: project, Root: "/home/u/" + project, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		a := state.Agent{Project: project, Name: "agent-01", Instance: "ab-" + project + "-agent-01", AI: "claude", Branch: "agentbox/x-" + project,
			BaseRef: "main", BaseCommit: "abc", Worktree: p.Worktree(project, "agent-01"), Status: state.AgentReady, CreatedAt: time.Now()}
		if err := st.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	lead := state.Agent{Project: "shop", Name: state.LeadName, Role: state.RoleLead, Instance: "ab-shop-lead", AI: "claude", Branch: "agentbox/lead",
		BaseRef: "main", BaseCommit: "abc", Worktree: p.Worktree("shop", state.LeadName), Status: state.AgentReady, CreatedAt: time.Now()}
	if err := st.AddAgent(ctx, lead); err != nil {
		t.Fatal(err)
	}
}

func TestReadHostInstallLeavesLeadsOutAndChangesNothing(t *testing.T) {
	p := clearEnv(t)
	hostStateDB(t, p)
	before, err := os.ReadFile(p.StateDB())
	if err != nil {
		t.Fatal(err)
	}
	inst, err := readHostInstall(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(inst.Projects, ","); got != "blog,shop" {
		t.Errorf("projects = %s", got)
	}
	var refs []string
	for _, a := range inst.Agents {
		refs = append(refs, a.Project+"/"+a.Name+"="+a.Instance)
	}
	if got := strings.Join(refs, ","); got != "blog/agent-01=ab-blog-agent-01,shop/agent-01=ab-shop-agent-01" {
		t.Errorf("agents = %s", got)
	}
	after, _ := os.ReadFile(p.StateDB())
	if !bytes.Equal(before, after) {
		t.Error("reading the host's state.db changed it")
	}
}

// vm init on a machine with projects of its own points at vm migrate rather
// than leaving them behind.
func TestInitPointsAHostInstallAtMigrate(t *testing.T) {
	p := clearEnv(t)
	hostStateDB(t, p)
	err := hostModeInUse(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "agentbox vm migrate") || !strings.Contains(err.Error(), "2 project(s) (blog, shop)") {
		t.Fatalf("hostModeInUse = %v", err)
	}
}

func TestMigrationStatus(t *testing.T) {
	p := clearEnv(t)
	ctx := context.Background()
	if st, err := migrationStatus(ctx, p); err != nil || st.State != "none" {
		t.Fatalf("with nothing: %+v, %v", st, err)
	}
	hostStateDB(t, p)
	st, err := migrationStatus(ctx, p)
	if err != nil || st.State != "available" || len(st.Agents) != 2 {
		t.Fatalf("with a host install: %+v, %v", st, err)
	}
	rec := &Migration{Started: time.Now(), Backup: "/b.db", Agents: []MovedAgent{{Project: "shop", Name: "agent-01", Instance: "ab-shop-agent-01"}}}
	if err := rec.save(p); err != nil {
		t.Fatal(err)
	}
	if st, _ := migrationStatus(ctx, p); st.State != "started" {
		t.Errorf("started: %+v", st)
	}
	loaded, err := LoadMigration(p)
	if err != nil || loaded.Backup != "/b.db" || loaded.Agents[0].Instance != "ab-shop-agent-01" {
		t.Errorf("LoadMigration = %+v, %v", loaded, err)
	}
}

// fakeHostIncus stands in for the host's Incus: `incus list` answers list, and
// every other command is appended to the returned log.
func fakeHostIncus(t *testing.T, list string) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\ncase \"$1\" in list) cat <<'JSON'\n" + list + "\nJSON\n;; esac\n"
	bin := filepath.Join(dir, "incus")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := hostIncus
	hostIncus = incus.Client{Bin: bin}
	t.Cleanup(func() { hostIncus = saved })
	return log
}

// --remove-old removes AgentBox's own machines by name, and nothing else in
// the host's Incus: not the user's own, even one named like AgentBox's.
func TestRemoveOldTouchesOnlyAgentBoxsOwnMachines(t *testing.T) {
	p := clearEnv(t)
	hostStateDB(t, p)
	calls := fakeHostIncus(t, `[
		{"name": "ab-shop-agent-01", "status": "Stopped"},
		{"name": "ab-blog-agent-01", "status": "Stopped"},
		{"name": "ab-shop-base", "status": "Stopped"},
		{"name": "agentbox-base", "status": "Stopped"},
		{"name": "ab-ubuntu-test", "status": "Stopped"},
		{"name": "kind-control-plane", "status": "Running"}
	]`)
	ctx := context.Background()
	backup := filepath.Join(p.Data, "backups", "state.db")
	_ = os.MkdirAll(filepath.Dir(backup), 0o700)
	copyTestFile(t, p.StateDB(), backup)
	rec := &Migration{Started: time.Now(), Backup: backup, Agents: []MovedAgent{
		{Project: "shop", Name: "agent-01", Instance: "ab-shop-agent-01"},
		{Project: "blog", Name: "agent-01", Instance: "ab-blog-agent-01"},
	}}
	if err := rec.save(p); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := removeOldMachines(ctx, p, func([]string) bool { return true }, &out); err == nil || !strings.Contains(err.Error(), "isn't finished") {
		t.Fatalf("before it's verified: %v", err)
	}
	rec.Verified = time.Now()
	if err := rec.save(p); err != nil {
		t.Fatal(err)
	}
	var asked []string
	if err := removeOldMachines(ctx, p, func(names []string) bool { asked = names; return false }, &out); err == nil {
		t.Fatal("removed with the answer no")
	}
	want := []string{"ab-shop-agent-01", "ab-blog-agent-01", "ab-shop-base", "agentbox-base"}
	if !slices.Equal(asked, want) {
		t.Errorf("asked about %v, want %v", asked, want)
	}
	if data, _ := os.ReadFile(calls); strings.Contains(string(data), "delete") {
		t.Fatalf("deleted with the answer no:\n%s", data)
	}
	if err := removeOldMachines(ctx, p, func([]string) bool { return true }, &out); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(calls)
	var deleted []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if name, ok := strings.CutPrefix(line, "delete --force "); ok {
			deleted = append(deleted, name)
		}
	}
	if !slices.Equal(deleted, want) {
		t.Errorf("deleted %v, want %v", deleted, want)
	}
	if _, err := os.Stat(p.StateDB()); err == nil {
		t.Error("the host's state.db is still where a host-mode daemon would use it")
	}
	if _, err := os.Stat(filepath.Join(p.Data, "state.db.moved-to-vm")); err != nil {
		t.Errorf("the host's state.db wasn't kept aside: %v", err)
	}
	if rec, _ := LoadMigration(p); rec.Removed.IsZero() {
		t.Error("the migration doesn't record the removal")
	}
}

// The copy into the VM takes the host's files but not what is the host's
// alone, and the state.db from its backup.
func TestCopyScript(t *testing.T) {
	dir := t.TempDir()
	data, config, home := filepath.Join(dir, "data"), filepath.Join(dir, "config"), filepath.Join(dir, "vmhome")
	files := map[string]string{
		"data/state.db":                             "live",
		"data/state.db-wal":                         "wal",
		"data/daemon.log":                           "log",
		"data/projects/shop/notes.md":               "notes",
		"data/media/shop/agent-01/a.png":            "png",
		"data/worktrees/shop/agent-01/x":            "worktree",
		"data/vm/agentbox/disk.raw":                 "disk",
		"data/tools/claude":                         "tool",
		"data/backups/state-1.db":                   "backup",
		"config/credentials/claude/work":            "token",
		"config/secrets.key":                        "key",
		"config/vm/agentbox.json":                   "vm",
		"vmhome/.local/share/agentbox/state.db-wal": "stale",
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", "-c", copyScript, "sh", data, config, filepath.Join(data, "backups", "state-1.db"))
	cmd.Env = append(os.Environ(), "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	vmData, vmConfig := filepath.Join(home, ".local/share/agentbox"), filepath.Join(home, ".config/agentbox")
	for path, want := range map[string]string{
		filepath.Join(vmData, "state.db"):                  "backup",
		filepath.Join(vmData, "projects/shop/notes.md"):    "notes",
		filepath.Join(vmData, "media/shop/agent-01/a.png"): "png",
		filepath.Join(vmConfig, "credentials/claude/work"): "token",
		filepath.Join(vmConfig, "secrets.key"):             "key",
	} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", path, got, err, want)
		}
	}
	for _, rel := range []string{"state.db-wal", "daemon.log", "worktrees", "vm", "tools", "backups"} {
		if _, err := os.Stat(filepath.Join(vmData, rel)); err == nil {
			t.Errorf("%s was copied into the VM", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(vmConfig, "vm")); err == nil {
		t.Error("the VM's own config was copied into it")
	}
}

func TestIndent(t *testing.T) {
	var b bytes.Buffer
	w := indent(&b)
	_, _ = w.Write([]byte("one\ntw"))
	_, _ = w.Write([]byte("o\nthree\n"))
	if got, want := b.String(), "    one\n    two\n    three\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func copyTestFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A home reached through a symlink (macOS's /tmp, Silverblue's /home) is the
// same home for a path under it that doesn't exist yet, like a worktrees
// directory no agent has made.
func TestWithinThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(filepath.Join(real, "u"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(real, "u")
	for path, want := range map[string]bool{
		filepath.Join(link, "u", ".local/share/agentbox/worktrees"): true,
		filepath.Join(link, "u"):                                    true,
		filepath.Join(real, "u", "x"):                               true,
		filepath.Join(link, "other"):                                false,
		"/elsewhere/agentbox":                                       false,
	} {
		if got := within(home, path); got != want {
			t.Errorf("within(%s, %s) = %v, want %v", home, path, got, want)
		}
	}
}
