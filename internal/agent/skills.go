package agent

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"agentbox/internal/skills"
	"agentbox/internal/state"
)

// Skills are delivered like secrets: written whole into the agent's HOME
// whenever what it gets changes, and when it starts. Claude Code, Codex,
// OpenCode and Cursor all watch or rescan their skill folders, so a running
// agent picks a change up without a restart, let alone a rebuild.

// SkillsFor is every skill on in a project, with its files: its override there,
// else the AgentBox-wide switch. project "" (the Home chat) gets the
// AgentBox-wide ones alone.
func (m *Manager) SkillsFor(ctx context.Context, project string) ([]skills.Installed, error) {
	all, err := m.Store.Skills(ctx)
	if err != nil {
		return nil, err
	}
	var out []skills.Installed
	for _, sk := range all {
		if !sk.EnabledFor(project) {
			continue
		}
		files, err := m.Store.SkillFiles(ctx, sk.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, skills.Installed{Name: sk.Name, Files: files})
	}
	return out, nil
}

// skillsProject is the project whose skills an agent gets: the Home chat's
// "project" spans every one, so it gets the AgentBox-wide ones.
func skillsProject(a state.Agent) string {
	if a.IsHome() {
		return ""
	}
	return a.Project
}

// WriteSkills installs an agent's skills, replacing the ones AgentBox put there
// before. A lead's go into its HOME on this machine; an agent's through its
// machine, which must be running.
func (m *Manager) WriteSkills(ctx context.Context, a state.Agent) error {
	list, err := m.SkillsFor(ctx, skillsProject(a))
	if err != nil {
		return err
	}
	if a.IsLead() {
		return skills.InstallLocal(m.Paths.LeadHome(a.Project), list)
	}
	archive, err := skills.Tar(list)
	if err != nil {
		return err
	}
	home := "/home/" + m.User.Name
	owner := strconv.Itoa(m.User.UID) + ":" + strconv.Itoa(m.User.GID)
	_, err = m.Incus.ExecInput(ctx, bytes.NewReader(archive), a.Instance, "sh", "-c", skills.InstallScript, "sh", home, owner)
	return err
}

// RewriteSkills installs skills again wherever a change reaches: every lead
// and every running agent of a project, or of every project when project is
// "" (a change AgentBox-wide). Agents whose machine isn't up are skipped:
// starting one installs them fresh. Like RewriteSecrets, it names every agent
// it couldn't reach rather than stopping at the first.
func (m *Manager) RewriteSkills(ctx context.Context, project string) error {
	agents, err := m.Store.Agents(ctx, project)
	if err != nil {
		return err
	}
	if project == "" {
		// The Home chat isn't any project's, so Agents("") may not list it.
		if home, err := m.Store.Agent(ctx, state.HomeProject, state.LeadName); err == nil && !containsAgent(agents, home) {
			agents = append(agents, home)
		}
	}
	var failed []string
	for _, a := range agents {
		if !a.IsLead() {
			if a.Status != state.AgentReady {
				continue
			}
			if inst, err := m.Incus.Instance(ctx, a.Instance); err != nil || inst.Status != "Running" {
				continue
			}
		}
		if err := m.WriteSkills(ctx, a); err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", a.Ref(), err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("the skills are saved, but couldn't be installed in %s: they get them when they start", strings.Join(failed, ", "))
	}
	return nil
}

func containsAgent(list []state.Agent, a state.Agent) bool {
	for _, b := range list {
		if b.Project == a.Project && b.Name == a.Name {
			return true
		}
	}
	return false
}
