package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	base = Base{Instance: name, SavedFrom: config["user.agentbox.saved-from"]}
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
	run := func(args ...string) error {
		_, err := m.Incus.Run(ctx, args...)
		return err
	}

	m.logf("Snapshotting %s", a.Instance)
	if err := run("snapshot", "create", a.Instance, snapshot); err != nil {
		return Base{}, err
	}
	defer m.Incus.Run(cleanup, "snapshot", "delete", a.Instance, snapshot)

	m.Incus.Run(ctx, "delete", "--force", next) // left over by an interrupted save
	m.logf("Copying the snapshot to %s", next)
	if err := run("copy", a.Instance+"/"+snapshot, next); err != nil {
		return Base{}, err
	}
	fail := func(err error) (Base, error) {
		m.Incus.Run(cleanup, "delete", "--force", next)
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
		if err := run("config", "device", "remove", next, device); err != nil {
			return fail(err)
		}
	}
	// `incus copy` copies configuration keys as well as devices, so the base
	// would otherwise carry whatever the agent it was saved from was capped at,
	// and every agent made from that base would silently inherit it — past the
	// installation's own defaults, and past a later change to them. Limits
	// belong to an agent, not to a project's base.
	for _, key := range LimitKeys {
		if _, ok := copied.Config[key]; !ok {
			continue
		}
		if err := run("config", "unset", next, key); err != nil {
			return fail(err)
		}
	}
	if err := run("start", next); err != nil {
		return fail(err)
	}
	if _, err := m.Incus.WaitReady(ctx, next, readyTimeout); err != nil {
		return fail(err)
	}
	m.logf("Removing the agent's own identity, logins and AI sessions from the copy")
	if err := run("exec", next, "--", "sh", "-c", scrubScript(m.User.Name)); err != nil {
		return fail(err)
	}
	savedAt := time.Now().UTC().Truncate(time.Second)
	if err := run("stop", next); err != nil {
		return fail(err)
	}
	if err := run("config", "set", next,
		"user.agentbox.saved-from="+a.Ref(),
		"user.agentbox.saved-at="+savedAt.Format(time.RFC3339)); err != nil {
		return fail(err)
	}
	if err := run("snapshot", "create", next, projectBaseSnapshot); err != nil {
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
		m.Incus.Run(ctx, "delete", "--force", previous) // the one from two saves ago
		if err := run("rename", name, previous); err != nil {
			return fail(err)
		}
		kept = true
	} else if !errors.Is(err, incus.ErrNotFound) {
		return fail(err)
	}
	if err := run("rename", next, name); err != nil {
		// The project would otherwise be left with no base at all, when a
		// moment ago it had a working one under its own name.
		if kept {
			m.Incus.Run(cleanup, "rename", previous, name)
		}
		return fail(err)
	}
	return Base{Instance: name, SavedFrom: a.Ref(), SavedAt: savedAt}, nil
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
		if _, err := m.Incus.Run(ctx, "delete", "--force", name); err != nil {
			return Base{}, err
		}
	} else if !errors.Is(err, incus.ErrNotFound) {
		return Base{}, err
	}
	m.logf("Putting the base saved from %s back", was.SavedFrom)
	if _, err := m.Incus.Run(ctx, "rename", previous, name); err != nil {
		return Base{}, err
	}
	was.Instance = name
	return was, nil
}

// RemoveBase deletes the project's base, and with it the one a save kept: new
// agents start from the plain base image again.
func (m *Manager) RemoveBase(ctx context.Context, project string) error {
	m.Incus.Run(ctx, "delete", "--force", PreviousBaseInstance(project))
	_, err := m.Incus.Run(ctx, "delete", "--force", ProjectBaseInstance(project))
	if err != nil && strings.Contains(err.Error(), "not found") {
		return fmt.Errorf("project %s has no saved base", project)
	}
	return err
}

// RemovePreviousBase drops what a save kept, giving its disk back. The project
// keeps the base it is on, and loses the one step back.
func (m *Manager) RemovePreviousBase(ctx context.Context, project string) error {
	_, err := m.Incus.Run(ctx, "delete", "--force", PreviousBaseInstance(project))
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
