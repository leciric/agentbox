package agent

import (
	"context"

	"agentbox/internal/state"
)

// BashEnvPath is the file BASH_ENV names in an agent's Claude Code, which
// every Bash tool call's shell reads: the heavy command's join into its run's
// cgroup when one just started (heavyhooks.go), and the secrets the AI tool
// itself was started without (toolcreds.go). Codex and OpenCode are given it
// too when they start without some.
func (m *Manager) BashEnvPath() string { return "/home/" + m.User.Name + "/.config/agentbox/bash_env" }

func (m *Manager) bashEnv() string {
	heavy := shellQuote("/home/" + m.User.Name + "/" + HeavyEnvFile)
	return "# Written by AgentBox.\n" + doNotTrackEnv + m.bashEnvSecrets() + "if [ -r " + heavy + " ]; then . " + heavy + "; fi\n"
}

func (m *Manager) writeBashEnv(ctx context.Context, a state.Agent) error {
	return m.Incus.WriteFile(ctx, a.Instance, m.BashEnvPath(), []byte(m.bashEnv()), m.User.UID, m.User.GID, 0o644)
}

// withBashEnv points BASH_ENV in Claude Code's settings.json at bash_env.
func (m *Manager) withBashEnv(b []byte) ([]byte, error) {
	return withClaudeEnv(b, map[string]string{"BASH_ENV": m.BashEnvPath()})
}

// doNotTrackEnv is the part of every agent's shells' environment (the env file
// and bash_env) that says nothing in the machine may report to anyone.
// AgentBox copies its released binary into each agent as
// /usr/local/bin/agentbox, and a daemon of it started there (`agentbox host
// setup`, a test's) would otherwise count as an install in the usage stats and
// the update check; the daemon refuses to send from an agent anyway
// (update.InAgent), and this keeps the project's own tools quiet too, as
// DO_NOT_TRACK is meant to.
const doNotTrackEnv = "export DO_NOT_TRACK=1\n"

// withDoNotTrack sets DO_NOT_TRACK in Claude Code's settings.json "env", which
// it copies into its own environment and its tools'.
func withDoNotTrack(b []byte) ([]byte, error) {
	return withClaudeEnv(b, map[string]string{"DO_NOT_TRACK": "1"})
}

// doNotTrackConfig is the Incus configuration key that puts DO_NOT_TRACK=1
// into the environment of every command run in the machine with `incus exec`,
// as root or not, whatever shell it starts. (A login shell of the agent's user,
// `runuser -l`, starts from a clean environment and reads the env file instead.)
const doNotTrackConfig = "environment.DO_NOT_TRACK=1"
