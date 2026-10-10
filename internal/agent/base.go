package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// A project base is the machine of an agent that already set the project up,
// saved so new agents for the project start from it instead of the plain base
// image. It keeps what the agent installed outside its worktree: packages,
// runtimes, Docker images and caches.

const (
	baseName = "base" // reserved: the base's instance is named like an agent called "base"
	// previousBaseName holds the base one save ago, so a save can be undone.
	// Keeping it costs a rename rather than a copy, and on the btrfs pool
	// host-setup makes it shares its extents with the base that replaced it
	// (D81), so what it holds on disk is the difference between the two, not a
	// second machine.
	previousBaseName    = "base-previous" // reserved, like baseName
	projectBaseSnapshot = "ready"
)

func ProjectBaseInstance(project string) string { return InstanceName(project, baseName) }

// PreviousBaseInstance is where the base a save replaced is kept.
func PreviousBaseInstance(project string) string { return InstanceName(project, previousBaseName) }

type Base struct {
	Instance  string
	SavedFrom string // agent ref
	SavedAt   time.Time
	// Built is what the base image the base descends from was built with: its
	// image version, components and agent tools. A base is a copy of an agent,
	// and an agent a copy of the base image or of an earlier base, and `incus
	// copy` carries configuration along, so the keys the image records on
	// itself (image.InstalledFrom) reach every base without being written
	// again — including bases saved before this was read. A refresh that
	// caught the machine up (CatchUp) moved them on before it was saved.
	Built image.Installed
}

// SnapshotRef is what new agents are copied from.
func (b Base) SnapshotRef() string { return b.Instance + "/" + projectBaseSnapshot }

// ProjectBase returns the project's saved base; ok is false when there is none.
func (m *Manager) ProjectBase(ctx context.Context, project string) (base Base, ok bool, err error) {
	return m.baseAt(ctx, ProjectBaseInstance(project))
}

// PreviousBase returns the base the last save replaced, which RevertBase puts
// back; ok is false when there is none to go back to. A project has one only
// between saves: the save before last is deleted when a new one is kept.
func (m *Manager) PreviousBase(ctx context.Context, project string) (base Base, ok bool, err error) {
	return m.baseAt(ctx, PreviousBaseInstance(project))
}

func (m *Manager) baseAt(ctx context.Context, name string) (base Base, ok bool, err error) {
	ready, err := m.Incus.HasSnapshot(ctx, name, projectBaseSnapshot)
	if err != nil || !ready {
		return Base{}, false, err
	}
	config, err := m.Incus.Config(ctx, name)
	if err != nil {
		return Base{}, false, err
	}
	base = Base{Instance: name, SavedFrom: config["user.agentbox.saved-from"], Built: image.InstalledFrom(config)}
	base.SavedAt, _ = time.Parse(time.RFC3339, config["user.agentbox.saved-at"])
	return base, true, nil
}

// SaveBase saves an agent's machine as its project's base. The agent keeps
// running: the base is built from a snapshot of it, then stripped of what
// belongs to that agent alone.
func (m *Manager) SaveBase(ctx context.Context, a state.Agent) (Base, error) {
	name := ProjectBaseInstance(a.Project)
	next := name + "-next"
	snapshot := "base-" + time.Now().UTC().Format("20060102-150405")
	cleanup := context.WithoutCancel(ctx)

	m.logf("Snapshotting %s", a.Instance)
	if err := m.Incus.CreateSnapshot(ctx, a.Instance, snapshot); err != nil {
		return Base{}, err
	}
	defer func() { _ = m.Incus.DeleteSnapshot(cleanup, a.Instance, snapshot) }()

	_ = m.Incus.Delete(ctx, next) // left over by an interrupted save
	m.logf("Copying the snapshot to %s", next)
	if err := m.Incus.Copy(ctx, a.Instance+"/"+snapshot, next); err != nil {
		return Base{}, err
	}
	fail := func(err error) (Base, error) {
		_ = m.Incus.Delete(cleanup, next)
		return Base{}, fmt.Errorf("saving the base of %s: %w", a.Project, err)
	}

	copied, err := m.Incus.Details(ctx, next)
	if err != nil {
		return fail(err)
	}
	for _, device := range copiedDevices {
		if _, ok := copied.Devices[device]; !ok {
			continue
		}
		if err := m.Incus.RemoveDevice(ctx, next, device); err != nil {
			return fail(err)
		}
	}
	// The agent's CPU share comes along in the copy (cpushare.go): a base boots
	// with every core, and each agent made from it gets a share of its own.
	if _, ok := copied.Config[cpuShareKey]; ok {
		if err := m.Incus.UnsetConfig(ctx, next, cpuShareKey); err != nil {
			return fail(err)
		}
	}
	// `incus copy` copies configuration keys as well as devices, so a base
	// saved from a machine an earlier release made would carry the limits it
	// set, and its place in the shared budget's cgroup: started with that
	// raw.lxc, it would try to run in the very directory the agent runs in.
	for _, step := range oldLimitSteps(next, copied.Config, copied.Devices) {
		if err := step(ctx, m.Incus); err != nil {
			return fail(err)
		}
	}
	if err := m.Incus.Start(ctx, next); err != nil {
		return fail(err)
	}
	if _, err := m.Incus.WaitReady(ctx, next, readyTimeout); err != nil {
		return fail(err)
	}
	m.logf("Removing the agent's own identity, logins and AI sessions from the copy")
	if _, err := m.Incus.Exec(ctx, next, "sh", "-c", scrubScript(m.User.Name)); err != nil {
		return fail(err)
	}
	savedAt := time.Now().UTC().Truncate(time.Second)
	// What the image the agent descends from was built with came along with
	// the copy (Base.Built), and is kept as it is: limits are an agent's, but
	// that is the machine's.
	built := image.InstalledFrom(copied.Config)
	if err := m.Incus.Stop(ctx, next); err != nil {
		return fail(err)
	}
	if err := m.Incus.SetConfig(ctx, next,
		"user.agentbox.saved-from="+a.Ref(),
		"user.agentbox.saved-at="+savedAt.Format(time.RFC3339)); err != nil {
		return fail(err)
	}
	if err := m.Incus.CreateSnapshot(ctx, next, projectBaseSnapshot); err != nil {
		return fail(err)
	}

	// The base this one replaces is kept rather than deleted, so one save can
	// be undone (RevertBase). A rename moves no data, and the two bases share
	// their extents on a copy-on-write pool, so this is the difference between
	// them on disk rather than a second machine. Only one step back is kept:
	// what the save before last replaced goes now.
	previous := PreviousBaseInstance(a.Project)
	kept := false
	if _, err := m.Incus.Instance(ctx, name); err == nil {
		m.logf("Keeping the base this replaces as %s, so the save can be undone", previous)
		_ = m.Incus.Delete(ctx, previous) // the one from two saves ago
		if err := m.Incus.Rename(ctx, name, previous); err != nil {
			return fail(err)
		}
		kept = true
	} else if !errors.Is(err, incus.ErrNotFound) {
		return fail(err)
	}
	if err := m.Incus.Rename(ctx, next, name); err != nil {
		// The project would otherwise be left with no base at all, when a
		// moment ago it had a working one under its own name.
		if kept {
			_ = m.Incus.Rename(cleanup, previous, name)
		}
		return fail(err)
	}
	return Base{Instance: name, SavedFrom: a.Ref(), SavedAt: savedAt, Built: built}, nil
}

// BaseBehind is how far a project base is behind the base image this AgentBox
// makes now; its Any is false when it is up to date.
func (m *Manager) BaseBehind(ctx context.Context, base Base) (image.Behind, error) {
	built, err := image.InstalledBuild(ctx, m.Incus)
	if err != nil {
		return image.Behind{}, err
	}
	return image.BehindImage(base.Built, built), nil
}

// CatchUp brings an agent's machine up to the base image this AgentBox makes
// now, as image.CatchUp does it, and says what it did for the agent to read.
// It is for the agent that refreshes a project base: made from the base, its
// machine is as far behind as the base is, and whatever it catches up is in
// the base saved from it. An agent that isn't behind is left alone, and ""
// comes back. What it set out to do comes back even when it fails.
func (m *Manager) CatchUp(ctx context.Context, a state.Agent) (string, error) {
	config, err := m.Incus.Config(ctx, a.Instance)
	if err != nil {
		return "", err
	}
	built, err := image.InstalledBuild(ctx, m.Incus)
	if err != nil {
		return "", err
	}
	behind := image.BehindImage(image.InstalledFrom(config), built)
	if !behind.Any() {
		return "", nil
	}
	what := DescribeBehind(behind)
	m.logf("Catching %s up with the base image: %s", a.Ref(), what)
	log := m.Log
	if log == nil {
		log = io.Discard
	}
	if err := image.CatchUp(ctx, m.Incus, a.Instance, m.User, behind, log); err != nil {
		return what, fmt.Errorf("catching %s up with the base image: %w", a.Ref(), err)
	}
	return what, nil
}

// DescribeBehind says in a sentence what catching up with the image changes.
func DescribeBehind(b image.Behind) string {
	var parts []string
	switch {
	case b.Image && b.From == "":
		parts = append(parts, "the system packages and settings of image "+image.Version+" (the base didn't record which image it came from)")
	case b.Image:
		var what []string
		for _, c := range b.Changes {
			what = append(what, c.What)
		}
		part := "image " + b.From + " → " + image.Version
		if len(what) > 0 {
			part += " (" + strings.Join(what, "; ") + ")"
		}
		parts = append(parts, part)
	}
	if len(b.Components) > 0 {
		parts = append(parts, strings.Join(b.Components, " and ")+", which the image has now")
	}
	if b.ToolsUnknown {
		parts = append(parts, "the agent tools at the versions tools.txt pins (the base didn't record its own)")
	} else {
		var moves []string
		for _, c := range b.ToolChanges() {
			switch {
			case c.From == "":
				moves = append(moves, c.Name+" "+c.To)
			case c.To == "":
				moves = append(moves, c.Name+" "+c.From+" removed")
			default:
				moves = append(moves, c.Name+" "+c.From+" → "+c.To)
			}
		}
		if len(moves) > 0 {
			parts = append(parts, strings.Join(moves, ", "))
		}
	}
	return strings.Join(parts, "; ")
}

// RevertBase puts the base the last save replaced back, and drops the one that
// replaced it. There is only ever one step to go back: a project that has
// reverted has nothing left to revert to.
func (m *Manager) RevertBase(ctx context.Context, project string) (Base, error) {
	name, previous := ProjectBaseInstance(project), PreviousBaseInstance(project)
	was, ok, err := m.PreviousBase(ctx, project)
	if err != nil {
		return Base{}, err
	}
	if !ok {
		return Base{}, fmt.Errorf("project %s has no previous base to go back to", project)
	}
	if _, err := m.Incus.Instance(ctx, name); err == nil {
		m.logf("Dropping the base saved from %s", project)
		if err := m.Incus.Delete(ctx, name); err != nil {
			return Base{}, err
		}
	} else if !errors.Is(err, incus.ErrNotFound) {
		return Base{}, err
	}
	m.logf("Putting the base saved from %s back", was.SavedFrom)
	if err := m.Incus.Rename(ctx, previous, name); err != nil {
		return Base{}, err
	}
	was.Instance = name
	return was, nil
}

// RemoveBase deletes the project's base, and with it the one a save kept: new
// agents start from the plain base image again.
func (m *Manager) RemoveBase(ctx context.Context, project string) error {
	_ = m.Incus.Delete(ctx, PreviousBaseInstance(project))
	err := m.Incus.Delete(ctx, ProjectBaseInstance(project))
	if err != nil && strings.Contains(err.Error(), "not found") {
		return fmt.Errorf("project %s has no saved base", project)
	}
	return err
}

// RemovePreviousBase drops what a save kept, giving its disk back. The project
// keeps the base it is on, and loses the one step back.
func (m *Manager) RemovePreviousBase(ctx context.Context, project string) error {
	err := m.Incus.Delete(ctx, PreviousBaseInstance(project))
	if err != nil && strings.Contains(err.Error(), "not found") {
		return fmt.Errorf("project %s has no previous base", project)
	}
	return err
}

// scrubScript deletes what belongs to one agent rather than to the project.
// AgentBox writes all of it again when it creates an agent from the base.
//
// The secrets file goes with the logins: a project base is a machine every
// future agent of the project is copied from, and it is kept until you save
// another one, so a key left in it would outlive the agent it was given to and
// reach agents nobody gave it to. Agent snapshots and forks are the other case
// and keep it: they are copies of *that* agent, which had the secret.
func scrubScript(user string) string {
	return fmt.Sprintf(`set -e
cd %s
rm -rf .config/agentbox/env .config/agentbox/secrets.env .codex/auth.json .claude.json .gitconfig AGENTBOX.md \
  .claude/CLAUDE.md .claude/projects .claude/sessions .claude/todos .claude/shell-snapshots \
  .codex/AGENTS.md .codex/sessions .codex/history.jsonl .bash_history \
  .config/agentbox/browser .local/state/agentbox .android .local/share/android-sdk
truncate -s0 /etc/machine-id
rm -f /var/lib/dbus/machine-id`, shellQuote("/home/"+user))
}
