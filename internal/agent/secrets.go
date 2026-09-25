package agent

import (
	"context"
	"fmt"
	"strings"

	"agentbox/internal/secrets"
	"agentbox/internal/state"
)

// Secrets are delivered the way the Claude Code token is (D39):
// a file written inside the agent, owned by the agent's user, mode 0600, and
// written again whenever what it holds changes. The agent's env file sources
// it, so its shells, its tmux windows and its ACP adapter all see the same
// variables; the AI tool picks up a change the next time it starts.

// SecretsPath is the file inside an agent holding the secrets it was given, as
// `export NAME='value'` lines. EnvPath sources it.
func (m *Manager) SecretsPath() string {
	return "/home/" + m.User.Name + "/.config/agentbox/secrets.env"
}

// secretsFile renders what goes into one agent: its project's secrets and its
// own. A lead has no machine, so it gets none — its adapter runs on the host,
// where these would land in the user's own environment.
func (m *Manager) secretsFile(ctx context.Context, a state.Agent) (string, error) {
	if a.IsLead() {
		return secrets.EnvFile(nil), nil
	}
	values, err := m.Secrets.ForAgent(ctx, a.Project, a.Name)
	if err != nil {
		return "", err
	}
	return secrets.EnvFile(values), nil
}

// WriteSecrets writes an agent's secrets file, replacing what was there. It is
// called when the agent is created or forked, when it starts, after a restore,
// and whenever a secret it gets is added, changed or removed — so the file is
// the whole set every time, and a removed secret really leaves.
func (m *Manager) WriteSecrets(ctx context.Context, a state.Agent) error {
	content, err := m.secretsFile(ctx, a)
	if err != nil {
		return err
	}
	return m.Incus.WriteFile(ctx, a.Instance, m.SecretsPath(), []byte(content), m.User.UID, m.User.GID, 0o600)
}

// RewriteSecrets writes the secrets file again for every running agent of a
// project, or of every project when project is "". Agents whose machine isn't
// up are skipped: starting one writes the file fresh anyway.
//
// It carries the error of every agent it couldn't reach, rather than stopping
// at the first: a secret you have just changed has either reached an agent or
// not, and you need to know which ones.
func (m *Manager) RewriteSecrets(ctx context.Context, project string) error {
	agents, err := m.Store.Agents(ctx, project)
	if err != nil {
		return err
	}
	var failed []string
	for _, a := range agents {
		if a.IsLead() || a.Status != state.AgentReady {
			continue
		}
		if inst, err := m.Incus.Instance(ctx, a.Instance); err != nil || inst.Status != "Running" {
			continue
		}
		if err := m.WriteSecrets(ctx, a); err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", a.Ref(), err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("the secret is stored, but couldn't be written into %s: they get it when they start", strings.Join(failed, ", "))
	}
	return nil
}
