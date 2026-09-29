package hostvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/hostvm/chv"
	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/paths"
	"agentbox/internal/state"
)

// `agentbox vm migrate` moves a Linux machine that runs AgentBox itself (host
// setup) into AgentBox's VM, taking everything along: the state.db (projects,
// settings, notes' records, memory, chats and history), the files beside it
// (notes, media, the leads' homes), the accounts and the secrets key, and
// every agent, whose worktree and branch stay where they are on the home the
// VM shares, and whose machine is made again in the VM, from its base image.
//
// It runs in steps, each recorded in a Migration file, so a run stopped
// anywhere carries on from where it was when it's run again:
//
//  1. The host's AgentBox stops: its agents, its daemon, its service. What the
//     agents were (running, their limits) is read from the host's Incus first.
//  2. The host's state.db is backed up (VACUUM INTO, a consistent copy), and
//     the agents' chat sessions are copied out of their machines.
//  3. The VM is made, as vm init makes it, without its daemon.
//  4. The host's files are copied into the VM, the state.db from the backup.
//     This is the one step that is never done twice: after it, the VM's
//     state.db is the one that's used, and may have moved on.
//  5. The VM's daemon starts, which migrates the state.db from whatever
//     release made it; the VM gets a base image, and each agent a machine.
//  6. The VM's daemon compares what it has with the backup
//     (POST /v1/migration/check). Only a migration that passes is verified.
//
// Nothing of the host's is deleted by any of that: its state.db, its files and
// its agents' old machines (stopped) all stay. `vm migrate --remove-old`,
// which the user runs once they've checked the VM, removes the old machines
// from the host's Incus — AgentBox's own, by name, and nothing else there —
// and only once the migration is verified. The host's Incus, its bridge, its
// pool and the budget unit stay installed. Until then, `vm delete --yes` goes
// back to host mode as it was.

// Migration is what `agentbox vm migrate` has done so far.
type Migration struct {
	Started time.Time `json:"started"`
	// Backup is the copy of the host's state.db the VM's was made from.
	Backup string `json:"backup,omitempty"`
	// Service is set when the host's daemon was a systemd user service,
	// which the migration disabled.
	Service bool         `json:"service,omitempty"`
	Agents  []MovedAgent `json:"agents,omitempty"`
	// Copied is when the host's state and files went into the VM.
	Copied time.Time `json:"copied,omitzero"`
	// Verified is when the VM's daemon was found to have all of it.
	Verified time.Time `json:"verified,omitzero"`
	// Found is what the check found, for the summary.
	Found []string `json:"found,omitempty"`
	// Removed is when the old machines were removed from the host's Incus.
	Removed time.Time `json:"removed,omitzero"`
}

// MovedAgent is one of the host's agents, and how far its move has got.
type MovedAgent struct {
	Project  string `json:"project"`
	Name     string `json:"name"`
	Instance string `json:"instance"`
	AI       string `json:"ai"`
	// Running is whether it ran when the migration stopped it: it's left
	// running in the VM too.
	Running bool `json:"running,omitempty"`
	// Limits are its machine's, from the host's Incus; nil when its machine
	// wasn't there to read, which gives the new one what new agents get.
	Limits *MovedLimits `json:"limits,omitempty"`
	// Home holds what was copied out of the old machine's home for the new
	// one: its chat sessions.
	Home string `json:"home,omitempty"`
	// Skipped says why it isn't moved: an agent still being made.
	Skipped string `json:"skipped,omitempty"`
	// Made is set once its machine in the VM is made.
	Made bool `json:"made,omitempty"`
}

// MovedLimits are an agent machine's limits; "" is none.
type MovedLimits struct {
	CPU       string `json:"cpu"`
	Allowance string `json:"allowance"`
	Memory    string `json:"memory"`
}

func (a MovedAgent) Ref() string { return a.Project + "/" + a.Name }

// migrationFile is where the Migration is kept: beside the VM's Config.
func migrationFile(p paths.Paths) string { return filepath.Join(p.Config, "vm", "migration.json") }

// LoadMigration reads the Migration, or nil when no migration was started.
func LoadMigration(p paths.Paths) (*Migration, error) {
	data, err := os.ReadFile(migrationFile(p))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Migration
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("reading %s: %w", migrationFile(p), err)
	}
	return &m, nil
}

func (m *Migration) save(p paths.Paths) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	file := migrationFile(p)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".new"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// hostInstall is what a host-mode state.db holds that the migration moves.
type hostInstall struct {
	Projects []string
	// Agents are the agents with machines: every agent but the leads.
	Agents []hostAgent
}

type hostAgent struct {
	Project, Name, Instance, AI, Status string
}

func (i hostInstall) describe() string {
	return fmt.Sprintf("%d project(s) (%s) and %d agent(s)", len(i.Projects), strings.Join(i.Projects, ", "), len(i.Agents))
}

// readHostInstall reads this machine's own state.db, without changing it.
func readHostInstall(ctx context.Context, p paths.Paths) (hostInstall, error) {
	if _, err := os.Stat(p.StateDB()); err != nil {
		return hostInstall{}, err
	}
	return readInstall(ctx, p.StateDB())
}

// readInstall reads the projects and agents of a state.db from any release:
// only columns every release had.
func readInstall(ctx context.Context, path string) (hostInstall, error) {
	db, err := state.OpenReadOnly(path)
	if err != nil {
		return hostInstall{}, err
	}
	defer func() { _ = db.Close() }()
	var inst hostInstall
	rows, err := db.QueryContext(ctx, "SELECT name FROM projects ORDER BY name")
	if err != nil {
		return inst, fmt.Errorf("reading %s: %w", path, err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return inst, err
		}
		inst.Projects = append(inst.Projects, name)
	}
	_ = rows.Close()
	rows, err = db.QueryContext(ctx, "SELECT project, name, instance, ai, status FROM agents WHERE name != ? ORDER BY project, name", state.LeadName)
	if err != nil {
		return inst, fmt.Errorf("reading %s: %w", path, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a hostAgent
		if err := rows.Scan(&a.Project, &a.Name, &a.Instance, &a.AI, &a.Status); err != nil {
			return inst, err
		}
		inst.Agents = append(inst.Agents, a)
	}
	return inst, rows.Err()
}

// hostIncus is the host's own Incus, where host mode's agents are. A variable
// so tests can stand in for it.
var hostIncus = incus.Client{}

// sessionDirs are where each AI tool keeps its chat sessions in an agent's
// home, relative to it: copied into the new machine, so a chat resumes the
// session it had instead of starting a new one.
var sessionDirs = map[string][]string{
	"claude":   {".claude/projects"},
	"codex":    {".codex/sessions"},
	"opencode": {".local/share/opencode"},
}

// MigrateOptions are migrateCHV's.
type MigrateOptions struct {
	// Want is the VM to make, when there's none yet; SizesSet says the user
	// chose its size, which an existing VM keeps.
	Want     chv.Config
	SizesSet bool
	Log      io.Writer
}

// migrateCHV is `agentbox vm migrate`: every step not done yet, in order.
func migrateCHV(ctx context.Context, p paths.Paths, opts MigrateOptions) error {
	log := opts.Log
	began := time.Now()
	rec, err := LoadMigration(p)
	if err != nil {
		return err
	}
	if rec != nil && !rec.Verified.IsZero() {
		_, _ = fmt.Fprintln(log, "This machine's AgentBox is in its VM already, and was checked there.")
		printMoved(log, rec)
		return nil
	}
	if rec == nil || rec.Copied.IsZero() {
		if _, err := os.Stat(p.StateDB()); err != nil {
			if chv.Exists(p, opts.Want.Name) {
				return errors.New("this machine's AgentBox runs in its VM already, and has nothing of its own left to move")
			}
			return errors.New("this machine has no AgentBox of its own to move (no state.db): agentbox vm init makes the VM")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	for _, dir := range []string{p.Data, p.Worktrees()} {
		if !within(home, dir) {
			return fmt.Errorf("%s isn't in your home directory, %s, which is all the VM sees of this machine: it can't be moved", dir, home)
		}
	}
	if !chv.Exists(p, opts.Want.Name) {
		if err := checkKVM(); err != nil {
			return err
		}
	}
	if rec == nil {
		rec = &Migration{Started: time.Now().UTC()}
	}

	if rec.Copied.IsZero() {
		if err := stopHost(ctx, p, rec, log); err != nil {
			return err
		}
		if err := backUp(ctx, p, rec, log); err != nil {
			return err
		}
		if err := stageHomes(ctx, p, rec, log); err != nil {
			return err
		}
		if err := rec.save(p); err != nil {
			return err
		}
	}

	vm, err := migrationVM(ctx, p, opts)
	if err != nil {
		return err
	}
	if rec.Copied.IsZero() {
		if err := copyIntoVM(ctx, vm, rec, log); err != nil {
			return err
		}
		rec.Copied = time.Now().UTC()
		if err := rec.save(p); err != nil {
			return err
		}
	}
	step(log, "Starting the VM's daemon, which brings the state.db up to date")
	start := time.Now()
	if err := vm.daemonUp(ctx); err != nil {
		return err
	}
	vm.took(start)
	c := api.NewClient(p.Socket())
	if err := ensureImage(ctx, c, log); err != nil {
		return err
	}
	if err := makeMachines(ctx, p, c, rec, log); err != nil {
		return err
	}
	if err := verify(ctx, p, c, rec, log); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(log, "==> Moved into AgentBox's VM (took %s in all)\n", time.Since(began).Round(100*time.Millisecond))
	printMoved(log, rec)
	return nil
}

func step(log io.Writer, what string) { _, _ = fmt.Fprintf(log, "==> %s\n", what) }

// within reports whether path is dir or under it, dir having no symlinks in
// it: path's are resolved as far as it exists, since a directory not made yet
// (the worktrees, before any agent) is under a home reached through a
// symlink all the same.
func within(dir, path string) bool {
	path = resolveExisting(filepath.Clean(path))
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// resolveExisting resolves the symlinks in the longest part of path that
// exists, and keeps the rest as it is.
func resolveExisting(path string) string {
	rest := ""
	for p := path; ; p = filepath.Dir(p) {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(real, rest)
		}
		if filepath.Dir(p) == p {
			return path
		}
		rest = filepath.Join(filepath.Base(p), rest)
	}
}

// stopHost stops the host's AgentBox, so nothing changes its state or its
// agents' worktrees while they're moved: what each agent was is read first,
// then the daemon stops every agent and itself, and the service that would
// start it again is disabled. An agent machine the daemon didn't stop (it
// wasn't running) is stopped here, by name. A daemon running jobs is left to
// finish them.
func stopHost(ctx context.Context, p paths.Paths, rec *Migration, log io.Writer) error {
	step(log, "Stopping this machine's own AgentBox: its agents, and its daemon")
	start := time.Now()
	inst, err := readHostInstall(ctx, p)
	if err != nil {
		return err
	}
	running := map[string]bool{}
	machines := map[string]incus.Instance{}
	if all, err := hostIncus.Instances(ctx); err == nil {
		for _, i := range all {
			machines[i.Name] = i
			running[i.Name] = i.Status == "Running"
		}
	} else if len(inst.Agents) > 0 {
		return fmt.Errorf("reading this machine's Incus, where its agents are: %w", err)
	}
	// A rerun before the copy finds everything stopped by the first: what
	// the agents were then is what counts.
	was := map[string]MovedAgent{}
	for _, a := range rec.Agents {
		was[a.Ref()] = a
	}
	rec.Agents = nil
	for _, a := range inst.Agents {
		moved := MovedAgent{Project: a.Project, Name: a.Name, Instance: a.Instance, AI: a.AI, Running: running[a.Instance]}
		if before, ok := was[moved.Ref()]; ok {
			moved.Running = before.Running
		}
		if a.Status != state.AgentReady {
			moved.Skipped = "it was still being made on this machine"
		}
		if m, ok := machines[a.Instance]; ok {
			l := agent.LimitsOf(m.Config)
			moved.Limits = &MovedLimits{CPU: l.ConfiguredCPU, Allowance: l.Allowance, Memory: l.Memory}
		}
		rec.Agents = append(rec.Agents, moved)
	}
	// Before anything is stopped: a run stopped from here on finds them
	// stopped, and has to know what they were.
	if err := rec.save(p); err != nil {
		return err
	}

	c := api.NewClient(p.Socket())
	if pingWithin(ctx, c, 3*time.Second) {
		jobs, err := c.Jobs(ctx)
		if err != nil {
			return err
		}
		for _, j := range jobs {
			if j.Status == api.JobRunning {
				return fmt.Errorf("this machine's daemon is running a job (%s %s): let it finish, then run agentbox vm migrate again", j.Kind, j.Target)
			}
		}
		j, err := c.StopAgents(ctx, api.StopAgentsRequest{})
		if err != nil {
			return fmt.Errorf("stopping this machine's agents: %w", err)
		}
		if err := c.FollowJobLog(ctx, j.ID, indent(log)); err != nil {
			return err
		}
	}
	if serviceEnabled(ctx) {
		_, _ = fmt.Fprintln(log, "    disabling agentbox.service, which would start it again")
		if out, err := exec.CommandContext(ctx, "systemctl", "--user", "disable", "--now", "agentbox.service").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl --user disable --now agentbox.service: %w: %s", err, strings.TrimSpace(string(out)))
		}
		rec.Service = true
	}
	if pingWithin(ctx, c, 3*time.Second) {
		if err := c.Shutdown(ctx); err != nil {
			return fmt.Errorf("stopping this machine's daemon: %w", err)
		}
		for deadline := time.Now().Add(30 * time.Second); pingWithin(ctx, c, time.Second); {
			if time.Now().After(deadline) {
				return errors.New("this machine's daemon didn't stop: stop it (agentbox daemon stop), then run agentbox vm migrate again")
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	// What the daemon stopped is stopped now; what it didn't (it wasn't
	// running, or a machine was paused) is stopped here.
	if all, err := hostIncus.Instances(ctx); err == nil {
		moved := map[string]bool{}
		for _, a := range rec.Agents {
			moved[a.Instance] = true
		}
		for _, i := range all {
			if !moved[i.Name] || i.Status == "Stopped" {
				continue
			}
			if err := hostIncus.StopWithin(ctx, i.Name, 30*time.Second); err != nil {
				if err := hostIncus.ForceStop(ctx, i.Name); err != nil {
					return fmt.Errorf("stopping %s: %w", i.Name, err)
				}
			}
		}
	}
	took(log, start)
	return nil
}

func pingWithin(ctx context.Context, c *api.Client, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.Ping(ctx) == nil
}

// serviceEnabled reports whether the host's daemon is a systemd user service
// that starts with the session (agentbox daemon install).
func serviceEnabled(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, "systemctl", "--user", "is-enabled", "agentbox.service").Output()
	return err == nil && strings.TrimSpace(string(out)) == "enabled"
}

// backUp copies the host's state.db, consistent, to where the VM can read it:
// the migration's own backups directory, beside it. The host's state.db
// itself isn't changed.
func backUp(ctx context.Context, p paths.Paths, rec *Migration, log io.Writer) error {
	dir := filepath.Join(p.Data, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file := filepath.Join(dir, "state-"+time.Now().UTC().Format("20060102-150405")+".db")
	step(log, "Backing up this machine's state.db to "+file)
	db, err := state.OpenReadOnly(p.StateDB())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", file); err != nil {
		return fmt.Errorf("backing up %s: %w", p.StateDB(), err)
	}
	rec.Backup = file
	return nil
}

// stageHomes copies each agent's chat sessions out of its old machine, into
// the migration's directory on the share, for its new machine.
func stageHomes(ctx context.Context, p paths.Paths, rec *Migration, log io.Writer) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	said := false
	for i := range rec.Agents {
		a := &rec.Agents[i]
		if a.Skipped != "" || a.Limits == nil { // no machine to copy from
			continue
		}
		dir := filepath.Join(p.Data, "migration", "homes", a.Project, a.Name)
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		copied := false
		for _, rel := range sessionDirs[a.AI] {
			if !said {
				step(log, "Copying the agents' chat sessions out of their old machines")
				said = true
			}
			parent := filepath.Join(dir, filepath.Dir(rel))
			if err := os.MkdirAll(parent, 0o700); err != nil {
				return err
			}
			err := hostIncus.PullDir(ctx, a.Instance, "/home/"+u.Username+"/"+rel, parent)
			switch {
			case err == nil:
				copied = true
			case isNotFound(err):
			default:
				_, _ = fmt.Fprintf(log, "    %s: couldn't copy its %s (%v): its chat starts a new session\n", a.Ref(), rel, err)
			}
		}
		a.Home = ""
		if copied {
			a.Home = dir
		}
	}
	return nil
}

func isNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, incus.ErrNotFound) ||
		strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no such file")
}

// migrationVM makes the VM, without its daemon, unless there's one; an
// existing one is started. What its daemon needs from the host isn't in it yet.
func migrationVM(ctx context.Context, p paths.Paths, opts MigrateOptions) (*VM, error) {
	c, err := chv.Load(p, opts.Want.Name)
	made := false
	switch {
	case errors.Is(err, chv.ErrNotCreated):
		c = opts.Want
		c.Created = time.Now().UTC()
		if err := c.Save(p); err != nil {
			return nil, err
		}
		made = true
		_, _ = fmt.Fprintf(opts.Log, "==> AgentBox's VM %s: %d CPUs, %s of memory growing to at most %s, a %s disk (%s)\n",
			c.Name, c.CPUs, sizeWords(c.MemoryMin), sizeWords(c.MemoryCap), sizeWords(c.Disk), chv.ConfigFile(p, c.Name))
	case err != nil:
		return nil, err
	case opts.SizesSet:
		_, _ = fmt.Fprintln(opts.Log, "note: AgentBox's VM exists already and keeps its size: change it with agentbox vm resize")
	}
	vm, err := NewCHV(p, c)
	if err != nil {
		return nil, err
	}
	vm.Log = opts.Log
	if made || chvStatus(ctx, c, vm.CHV.Layout, p).State != api.VMRunning {
		// Safe to run again, like vm init: done steps are quick.
		if err := vm.CHV.setUp(ctx, vm, false); err != nil {
			return nil, fmt.Errorf("%w\nNothing of this machine's AgentBox was changed or removed: run agentbox vm migrate again to carry on", err)
		}
		return vm, nil
	}
	if err := vm.Ready(ctx); err != nil {
		return nil, err
	}
	return vm, nil
}

// copyScript copies the host's AgentBox files into the VM user's own, run in
// the VM, where the host's home is shared at its own path: everything in the
// host's data directory but what is the host's alone (its worktrees, which
// the VM uses where they are; its VM; sockets; logs; the lead's tools, which
// the VM installs for itself; the migration's own files), everything in its
// config directory but the VM's config, and the state.db from its backup,
// replacing whatever state a daemon of the VM's own made.
const copyScript = `set -eu
src_data=$1 src_config=$2 backup=$3
data=$HOME/.local/share/agentbox config=$HOME/.config/agentbox
mkdir -p "$data" "$config"
chmod 700 "$data" "$config"
for f in "$src_data"/* "$src_data"/.[!.]*; do
  [ -e "$f" ] || [ -L "$f" ] || continue
  case "${f##*/}" in worktrees|vm|run|tools|backups|migration|daemon.log*|state.db*) continue ;; esac
  cp -a "$f" "$data/"
done
for f in "$src_config"/* "$src_config"/.[!.]*; do
  [ -e "$f" ] || [ -L "$f" ] || continue
  case "${f##*/}" in vm) continue ;; esac
  cp -a "$f" "$config/"
done
rm -f "$data/state.db-wal" "$data/state.db-shm" "$data/state.db.moving"
cp "$backup" "$data/state.db.moving"
chmod 600 "$data/state.db.moving"
mv -f "$data/state.db.moving" "$data/state.db"
du -sh "$data" | cut -f1
`

// copyIntoVM stops the VM's daemon if it runs (a daemon with state of its own,
// from a VM made before), and copies the host's files into the VM.
func copyIntoVM(ctx context.Context, v *VM, rec *Migration, log io.Writer) error {
	c := api.NewClient(v.Paths.Socket())
	if pingWithin(ctx, c, 3*time.Second) {
		step(log, "Stopping the VM's daemon, for the state it takes over")
		if err := c.Shutdown(ctx); err != nil {
			return err
		}
		for deadline := time.Now().Add(30 * time.Second); pingWithin(ctx, c, time.Second); {
			if time.Now().After(deadline) {
				return errors.New("the VM's daemon didn't stop")
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	step(log, "Copying this machine's projects, settings, accounts, notes, memory, chats and media into the VM")
	start := time.Now()
	out, err := v.exec(ctx, nil, "sh", "-c", copyScript, "sh", v.Paths.Data, v.Paths.Config, rec.Backup)
	if err != nil {
		return fmt.Errorf("copying into the VM: %w", err)
	}
	_, _ = fmt.Fprintf(log, "    %s in the VM\n", strings.TrimSpace(out))
	v.took(start)
	return nil
}

// ensureImage builds the VM's base image unless it has one, with the optional
// parts the host's had.
func ensureImage(ctx context.Context, c *api.Client, log io.Writer) error {
	ready, err := c.ImageReady(ctx)
	if err != nil || ready {
		return err
	}
	req := api.BuildImageRequest{}
	if installed, err := image.InstalledBuild(ctx, hostIncus); err == nil {
		comp := installed.Components
		req = api.BuildImageRequest{Android: &comp.Android, Codex: &comp.Codex, OpenCode: &comp.OpenCode, DevCaches: &comp.DevCaches, Incus: &comp.Incus}
	}
	step(log, "Building the VM's base image, which every agent's new machine is made from (a few minutes)")
	start := time.Now()
	if err := runJob(ctx, c, log, func() (api.Job, error) { return c.BuildImage(ctx, req) }); err != nil {
		return fmt.Errorf("building the VM's base image: %w", err)
	}
	took(log, start)
	return nil
}

// makeMachines gives every moved agent its machine in the VM, one at a time,
// recording each one made.
func makeMachines(ctx context.Context, p paths.Paths, c *api.Client, rec *Migration, log io.Writer) error {
	var failed []string
	for i := range rec.Agents {
		a := &rec.Agents[i]
		if a.Made || a.Skipped != "" {
			continue
		}
		step(log, fmt.Sprintf("Making %s's machine in the VM", a.Ref()))
		start := time.Now()
		req := api.RecreateRequest{Home: a.Home, Stopped: !a.Running}
		if a.Limits != nil {
			req.CPU, req.CPUAllowance, req.Memory = &a.Limits.CPU, &a.Limits.Allowance, &a.Limits.Memory
		}
		err := runJob(ctx, c, log, func() (api.Job, error) { return c.Recreate(ctx, a.Ref(), req) })
		if err != nil && strings.Contains(err.Error(), agent.ErrHasMachine.Error()) {
			err = nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			_, _ = fmt.Fprintf(log, "    %s: %v\n", a.Ref(), err)
			failed = append(failed, a.Ref())
			continue
		}
		a.Made = true
		if err := rec.save(p); err != nil {
			return err
		}
		took(log, start)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d agent(s) have no machine in the VM yet (%s): run agentbox vm migrate again to try again. Nothing of this machine's was removed", len(failed), strings.Join(failed, ", "))
	}
	return nil
}

// runJob starts a job in the VM's daemon, follows its log and returns its error.
func runJob(ctx context.Context, c *api.Client, log io.Writer, start func() (api.Job, error)) error {
	j, err := start()
	if err != nil {
		return err
	}
	if err := c.FollowJobLog(ctx, j.ID, indent(log)); err != nil {
		return err
	}
	final, err := c.Job(context.WithoutCancel(ctx), j.ID)
	if err != nil {
		return err
	}
	if final.Status != api.JobSucceeded {
		return errors.New(final.Error)
	}
	return nil
}

// verify has the VM's daemon compare what it has with the backup.
func verify(ctx context.Context, p paths.Paths, c *api.Client, rec *Migration, log io.Writer) error {
	step(log, "Checking that everything arrived")
	check, err := c.CheckMigration(ctx, api.MigrationCheckRequest{Backup: rec.Backup})
	if err != nil {
		return err
	}
	for _, line := range check.Found {
		_, _ = fmt.Fprintf(log, "    %s\n", line)
	}
	if !check.OK {
		for _, line := range check.Problems {
			_, _ = fmt.Fprintf(log, "    missing: %s\n", line)
		}
		return fmt.Errorf("the VM doesn't have everything this machine had (%d problem(s), above): nothing of this machine's was removed, and agentbox vm migrate carries on when it's run again", len(check.Problems))
	}
	rec.Found = check.Found
	rec.Verified = time.Now().UTC()
	return rec.save(p)
}

// notCarried is what the migration can't take along, said plainly.
const notCarried = `What didn't come along: anything installed or changed inside the agents' old
machines outside their worktrees — packages, their home directories (apart from
their chat sessions), running processes — and the machine half of their snapshots,
except each agent's first. The new machines are fresh from the VM's base image.`

func printMoved(log io.Writer, rec *Migration) {
	var running, stopped, skipped []string
	for _, a := range rec.Agents {
		switch {
		case a.Skipped != "":
			skipped = append(skipped, a.Ref()+" ("+a.Skipped+")")
		case a.Running:
			running = append(running, a.Ref())
		default:
			stopped = append(stopped, a.Ref())
		}
	}
	if len(running) > 0 {
		_, _ = fmt.Fprintf(log, "Running in the VM, as they were: %s\n", strings.Join(running, ", "))
	}
	if len(stopped) > 0 {
		_, _ = fmt.Fprintf(log, "Stopped, as they were: %s\n", strings.Join(stopped, ", "))
	}
	if len(skipped) > 0 {
		_, _ = fmt.Fprintf(log, "Not moved: %s\n", strings.Join(skipped, ", "))
	}
	_, _ = fmt.Fprintln(log, notCarried)
	if rec.Removed.IsZero() {
		_, _ = fmt.Fprintf(log, `
This machine's own copy is untouched: its state.db (backed up to %s), and its
agents' old machines, stopped, in its Incus. Once you've checked everything works
in the VM, remove the old machines with:
  agentbox vm migrate --remove-old
To go back to running AgentBox on this machine instead: agentbox vm delete --yes
`, rec.Backup)
		if rec.Service {
			_, _ = fmt.Fprintln(log, "(and agentbox daemon install, which makes its daemon a service again: the move turned agentbox.service off)")
		}
	}
}

// oldMachines are the host Incus instances the migration may remove: each
// moved agent's old machine, the projects' saved bases and the base image,
// by the names AgentBox gave them, and only those of them that exist. Any
// other instance, even one named like AgentBox's, is left alone.
func oldMachines(ctx context.Context, rec *Migration) ([]string, error) {
	all, err := hostIncus.Instances(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading this machine's Incus: %w", err)
	}
	want := map[string]bool{image.Base: true, image.Base + "-next": true}
	for _, a := range rec.Agents {
		want[a.Instance] = true
	}
	if inst, err := readInstall(ctx, rec.Backup); err == nil {
		for _, project := range inst.Projects {
			want[agent.InstanceName(project, "base")] = true
			want[agent.InstanceName(project, "base-previous")] = true
		}
	}
	var out []string
	for _, i := range all {
		if want[i.Name] {
			out = append(out, i.Name)
		}
	}
	return out, nil
}

// removeOldMachines removes the old machines from the host's Incus, once the
// migration is verified, and puts the host's state.db aside, so that going
// back to host mode later starts afresh rather than with agents whose
// machines are gone. confirm is asked with the list first.
func removeOldMachines(ctx context.Context, p paths.Paths, confirm func([]string) bool, log io.Writer) error {
	rec, err := LoadMigration(p)
	if err != nil {
		return err
	}
	switch {
	case rec == nil:
		return errors.New("no migration to finish: agentbox vm migrate moves this machine's AgentBox into the VM first")
	case rec.Verified.IsZero():
		return errors.New("the migration isn't finished and checked yet, so nothing of this machine's is removed: run agentbox vm migrate first")
	}
	names, err := oldMachines(ctx, rec)
	if err != nil {
		return err
	}
	if len(names) > 0 && !confirm(names) {
		return errors.New("nothing was removed")
	}
	for _, name := range names {
		_, _ = fmt.Fprintf(log, "==> Removing %s from this machine's Incus\n", name)
		if err := hostIncus.Delete(ctx, name); err != nil && !errors.Is(err, incus.ErrNotFound) {
			return fmt.Errorf("removing %s: %w", name, err)
		}
	}
	if err := os.RemoveAll(filepath.Join(p.Data, "migration")); err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		from := p.StateDB() + suffix
		if _, err := os.Stat(from); err == nil {
			if err := os.Rename(from, filepath.Join(p.Data, "state.db.moved-to-vm"+suffix)); err != nil {
				return err
			}
		}
	}
	rec.Removed = time.Now().UTC()
	if err := rec.save(p); err != nil {
		return err
	}
	if len(names) == 0 {
		_, _ = fmt.Fprintln(log, "This machine's Incus has none of AgentBox's old machines left.")
	}
	_, _ = fmt.Fprintf(log, "Done. This machine's Incus, its network and storage pool, and everything else in it stay as they were. The state.db from before is kept at %s.\n", rec.Backup)
	return nil
}

// MigrationStatus is `agentbox vm migrate --status --json`, for the app: what
// there is to move, how far a migration got, and what it would remove.
type MigrationStatus struct {
	// State is "none" (nothing to move), "available" (a host-mode install
	// with projects), "started", "verified" or "removed".
	State    string   `json:"state"`
	Projects []string `json:"projects,omitempty"`
	Agents   []string `json:"agents,omitempty"`
	// OldMachines are what --remove-old would remove, once verified.
	OldMachines []string   `json:"oldMachines,omitempty"`
	Backup      string     `json:"backup,omitempty"`
	Found       []string   `json:"found,omitempty"`
	Migration   *Migration `json:"migration,omitempty"`
}

func migrationStatus(ctx context.Context, p paths.Paths) (MigrationStatus, error) {
	rec, err := LoadMigration(p)
	if err != nil {
		return MigrationStatus{}, err
	}
	if rec == nil {
		inst, err := readHostInstall(ctx, p)
		if err != nil || len(inst.Projects) == 0 {
			return MigrationStatus{State: "none"}, nil
		}
		st := MigrationStatus{State: "available", Projects: inst.Projects}
		for _, a := range inst.Agents {
			st.Agents = append(st.Agents, a.Project+"/"+a.Name)
		}
		return st, nil
	}
	st := MigrationStatus{State: "started", Backup: rec.Backup, Found: rec.Found, Migration: rec}
	for _, a := range rec.Agents {
		st.Agents = append(st.Agents, a.Ref())
	}
	switch {
	case !rec.Removed.IsZero():
		st.State = "removed"
	case !rec.Verified.IsZero():
		st.State = "verified"
		if names, err := oldMachines(ctx, rec); err == nil {
			st.OldMachines = names
		}
	}
	return st, nil
}

// indent writes what a job logs beneath the step it's part of.
func indent(w io.Writer) io.Writer { return &indenter{w: w, start: true} }

type indenter struct {
	w     io.Writer
	start bool
}

func (i *indenter) Write(b []byte) (int, error) {
	for _, line := range strings.SplitAfter(string(b), "\n") {
		if line == "" {
			continue
		}
		if i.start {
			if _, err := io.WriteString(i.w, "    "); err != nil {
				return 0, err
			}
		}
		if _, err := io.WriteString(i.w, line); err != nil {
			return 0, err
		}
		i.start = strings.HasSuffix(line, "\n")
	}
	return len(b), nil
}

func took(log io.Writer, start time.Time) {
	_, _ = fmt.Fprintf(log, "    (took %s)\n", time.Since(start).Round(100*time.Millisecond))
}
